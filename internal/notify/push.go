package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// KV keys for the VAPID key pair. The info feature reads the public key's
// presence to report push support.
const (
	KVVAPIDPublic  = "notify.vapid.public"
	KVVAPIDPrivate = "notify.vapid.private"
)

const (
	pushTTL          = 12 * 60 * 60 // seconds
	pushTimeout      = 10 * time.Second
	pushParallel     = 4
	maxSubscriptions = 50
	maxPushBody      = 1000 // runes; keeps the encrypted record under 4 KiB
)

// pushPayload is what the service worker receives.
type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Link  string `json:"link,omitempty"`
	Tag   string `json:"tag"`
	Kind  string `json:"kind"`
}

// loadVAPID reads the key pair, generating and storing it on first run.
func (s *Service) loadVAPID(ctx context.Context) error {
	pub, ok1, err := s.d.Store.GetKV(ctx, KVVAPIDPublic)
	if err != nil {
		return err
	}
	priv, ok2, err := s.d.Store.GetKV(ctx, KVVAPIDPrivate)
	if err != nil {
		return err
	}
	if ok1 && ok2 && pub != "" && priv != "" {
		s.vapidPublic, s.vapidPrivate = pub, priv
		return nil
	}
	priv, pub, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return err
	}
	// Private first: a crash in between leaves no public key, so the next
	// start regenerates a consistent pair.
	if err := s.d.Store.SetKV(ctx, KVVAPIDPrivate, priv); err != nil {
		return err
	}
	if err := s.d.Store.SetKV(ctx, KVVAPIDPublic, pub); err != nil {
		return err
	}
	s.vapidPublic, s.vapidPrivate = pub, priv
	return nil
}

// VAPIDPublicKey returns the application server key for PushManager.subscribe.
func (s *Service) VAPIDPublicKey() string { return s.vapidPublic }

func (s *Service) hasSubscriptions() bool { return s.subs.Load() > 0 }

func (s *Service) countSubscriptions(ctx context.Context) (int, error) {
	var n int
	if err := s.d.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_subscriptions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("notify: count subscriptions: %w", err)
	}
	s.subs.Store(int64(n))
	return n, nil
}

// Subscribe stores (or refreshes) a browser push subscription.
func (s *Service) Subscribe(ctx context.Context, sub api.PushSubscription, userAgent string) error {
	if err := validateSubscription(sub); err != nil {
		return err
	}
	device := strings.TrimSpace(sub.Device)
	if device == "" {
		device = deviceLabel(userAgent)
	}
	device = truncate(device, 80)
	n, err := s.countSubscriptions(ctx)
	if err != nil {
		return err
	}
	var exists int
	_ = s.d.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_subscriptions WHERE endpoint=?`, sub.Endpoint).Scan(&exists)
	if exists == 0 && n >= maxSubscriptions {
		return httpx.Conflict("too many push subscriptions; remove a device first")
	}
	_, err = s.d.Store.DB.ExecContext(ctx, `INSERT INTO push_subscriptions(endpoint,p256dh,auth,device,created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET p256dh=excluded.p256dh, auth=excluded.auth, device=excluded.device, failures=0`,
		sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth, device, ts(s.now()))
	if err != nil {
		return fmt.Errorf("notify: subscribe: %w", err)
	}
	_, err = s.countSubscriptions(ctx)
	return err
}

// Unsubscribe removes a subscription by endpoint (no error if unknown).
func (s *Service) Unsubscribe(ctx context.Context, endpoint string) error {
	if _, err := s.d.Store.DB.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint=?`, endpoint); err != nil {
		return fmt.Errorf("notify: unsubscribe: %w", err)
	}
	_, err := s.countSubscriptions(ctx)
	return err
}

func validateSubscription(sub api.PushSubscription) error {
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Host == "" || len(sub.Endpoint) > maxLink {
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid push endpoint", Field: "endpoint"}
	}
	// Push services are HTTPS. Plain HTTP is accepted for loopback only
	// (local push-service emulators and tests).
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "push endpoint must be https", Field: "endpoint"}
	}
	if b, err := decodeB64(sub.Keys.P256dh); err != nil || len(b) != 65 || b[0] != 4 {
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid p256dh key", Field: "keys.p256dh"}
	}
	if b, err := decodeB64(sub.Keys.Auth); err != nil || len(b) != 16 {
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid auth secret", Field: "keys.auth"}
	}
	return nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// decodeB64 accepts URL-safe or standard base64, padded or not.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	return base64.RawURLEncoding.DecodeString(s)
}

type storedSub struct {
	endpoint, p256dh, auth string
}

func (s *Service) listSubs(ctx context.Context) ([]storedSub, error) {
	rows, err := s.d.Store.DB.QueryContext(ctx, `SELECT endpoint,p256dh,auth FROM push_subscriptions`)
	if err != nil {
		return nil, fmt.Errorf("notify: list subscriptions: %w", err)
	}
	defer rows.Close()
	var out []storedSub
	for rows.Next() {
		var ss storedSub
		if err := rows.Scan(&ss.endpoint, &ss.p256dh, &ss.auth); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// errNoDevices is returned by sendPush when nobody is subscribed.
var errNoDevices = errors.New("no devices subscribed to push")

// sendPush delivers n to every subscription and returns how many
// succeeded. Gone subscriptions (404/410) are deleted.
func (s *Service) sendPush(ctx context.Context, n api.Notification) (int, error) {
	subs, err := s.listSubs(ctx)
	if err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, errNoDevices
	}
	payload, err := json.Marshal(buildPushPayload(n))
	if err != nil {
		return 0, err
	}
	urgency := webpush.UrgencyNormal
	if n.Kind == "attention" || n.Kind == "security" {
		urgency = webpush.UrgencyHigh
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ok      int
		lastErr error
		sem     = make(chan struct{}, pushParallel)
	)
	for _, sub := range subs {
		sub := sub
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			err := s.pushOne(ctx, sub, payload, urgency)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				lastErr = err
				return
			}
			ok++
		}()
	}
	wg.Wait()
	if ok == 0 && lastErr != nil {
		return 0, lastErr
	}
	return ok, nil
}

func (s *Service) pushOne(ctx context.Context, sub storedSub, payload []byte, urgency webpush.Urgency) error {
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	// webpush-go appends padding into the message's backing array, so each
	// concurrent send needs its own copy.
	msg := make([]byte, len(payload))
	copy(msg, payload)
	res, err := webpush.SendNotificationWithContext(ctx, msg, &webpush.Subscription{
		Endpoint: sub.endpoint,
		Keys:     webpush.Keys{P256dh: sub.p256dh, Auth: sub.auth},
	}, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.subscriber(),
		TTL:             pushTTL,
		Urgency:         urgency,
		VAPIDPublicKey:  s.vapidPublic,
		VAPIDPrivateKey: s.vapidPrivate,
	})
	db := s.d.Store.DB
	if err != nil {
		_, _ = db.ExecContext(context.WithoutCancel(ctx), `UPDATE push_subscriptions SET failures=failures+1 WHERE endpoint=?`, sub.endpoint)
		return fmt.Errorf("push: %w", redactURLError(err))
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	bg := context.WithoutCancel(ctx)
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		_, _ = db.ExecContext(bg, `UPDATE push_subscriptions SET failures=0, last_ok_at=? WHERE endpoint=?`, ts(s.now()), sub.endpoint)
		return nil
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		_, _ = db.ExecContext(bg, `DELETE FROM push_subscriptions WHERE endpoint=?`, sub.endpoint)
		_, _ = s.countSubscriptions(bg)
		s.log().Info("notify: removed expired push subscription", "host", endpointHost(sub.endpoint))
		return fmt.Errorf("push to %s: subscription expired", endpointHost(sub.endpoint))
	default:
		_, _ = db.ExecContext(bg, `UPDATE push_subscriptions SET failures=failures+1 WHERE endpoint=?`, sub.endpoint)
		return fmt.Errorf("push to %s: %s", endpointHost(sub.endpoint), res.Status)
	}
}

func buildPushPayload(n api.Notification) pushPayload {
	tag := n.ID
	if n.SessionID != "" {
		tag = n.Kind + ":" + n.SessionID
	}
	return pushPayload{
		Title: n.Title,
		Body:  truncate(n.Body, maxPushBody),
		Link:  n.Link,
		Tag:   tag,
		Kind:  n.Kind,
	}
}

// subscriber is the VAPID "sub" claim: the public origin when it is HTTPS,
// otherwise the project URL (push services require mailto: or https:).
func (s *Service) subscriber() string {
	if s.d.Cfg != nil {
		if o := s.d.Cfg.Origin(); strings.HasPrefix(o, "https://") {
			return o
		}
	}
	return "https://github.com/aduthekaddu/relay"
}

// endpointHost is safe to log (endpoint paths are capability URLs).
func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "?"
	}
	return u.Host
}

// deviceLabel derives "iPhone · Safari" style labels from a User-Agent.
func deviceLabel(ua string) string {
	os := "Device"
	switch {
	case strings.Contains(ua, "iPhone"):
		os = "iPhone"
	case strings.Contains(ua, "iPad"):
		os = "iPad"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "Mac OS X") || strings.Contains(ua, "Macintosh"):
		os = "Mac"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "CrOS"):
		os = "ChromeOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	browser := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	if browser == "" {
		return os
	}
	return os + " · " + browser
}
