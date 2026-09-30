package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/aduthekaddu/relay/internal/api"
)

const (
	webhookTimeout = 5 * time.Second
	ntfyTimeout    = 10 * time.Second
)

// deliver fans one job out to every configured channel concurrently.
func (s *Service) deliver(ctx context.Context, j job) {
	var wg sync.WaitGroup
	run := func(name string, f func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(ctx); err != nil && !errors.Is(err, errNoDevices) {
				s.log().Warn("notify: delivery failed", "channel", name, "kind", j.n.Kind, "err", err)
			}
		}()
	}
	if s.hasSubscriptions() {
		run("push", func(ctx context.Context) error { _, err := s.sendPush(ctx, j.n); return err })
	}
	if j.settings.NtfyURL != "" {
		run("ntfy", func(ctx context.Context) error { return s.sendNtfy(ctx, j.settings.NtfyURL, j.n) })
	}
	if j.settings.WebhookURL != "" {
		run("webhook", func(ctx context.Context) error { return s.sendWebhook(ctx, j.settings.WebhookURL, j.n) })
	}
	wg.Wait()
}

// sendNtfy publishes to an ntfy topic URL (https://ntfy.sh/<topic> or a
// self-hosted server). Credentials may be embedded in the URL userinfo.
func (s *Service) sendNtfy(ctx context.Context, topicURL string, n api.Notification) error {
	ctx, cancel := context.WithTimeout(ctx, ntfyTimeout)
	defer cancel()
	msg := n.Body
	if msg == "" {
		msg = n.Title
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, topicURL, strings.NewReader(msg))
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", headerValue(n.Title))
	req.Header.Set("Priority", ntfyPriority(n.Kind))
	req.Header.Set("Tags", ntfyTags(n))
	if click := s.absoluteLink(n.Link); click != "" {
		req.Header.Set("Click", click)
	}
	return s.do(req, "ntfy")
}

// webhookBody is the generic webhook JSON. text/content make it directly
// usable with Slack- and Discord-compatible incoming webhooks.
type webhookBody struct {
	api.Notification
	URL     string `json:"url,omitempty"`
	Text    string `json:"text"`
	Content string `json:"content"`
	Source  string `json:"source"`
}

func (s *Service) sendWebhook(ctx context.Context, hookURL string, n api.Notification) error {
	ctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()
	text := n.Title
	if n.Body != "" {
		text += " — " + n.Body
	}
	b, err := json.Marshal(webhookBody{Notification: n, URL: s.absoluteLink(n.Link), Text: text, Content: truncate(text, 2000), Source: "relay"})
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hookURL, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req, "webhook")
}

func (s *Service) do(req *http.Request, name string) error {
	req.Header.Set("User-Agent", "Relay/"+s.version())
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", name, redactURLError(err))
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%s: %s returned %s", name, req.URL.Host, res.Status)
	}
	return nil
}

// redactURLError drops the URL (which may carry credentials or a secret
// webhook path) from *url.Error messages.
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s %s: %w", ue.Op, endpointHost(ue.URL), ue.Err)
	}
	return err
}

func (s *Service) version() string {
	if s.d.Version != "" {
		return s.d.Version
	}
	return "dev"
}

// absoluteLink turns an in-app path into a URL on the public origin.
func (s *Service) absoluteLink(link string) string {
	if link == "" || s.d.Cfg == nil {
		return ""
	}
	o := s.d.Cfg.Origin()
	if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
		return ""
	}
	return strings.TrimRight(o, "/") + link
}

func ntfyPriority(kind string) string {
	switch kind {
	case "security":
		return "5"
	case "attention":
		return "4"
	case "preview", "exited":
		return "2"
	}
	return "3"
}

func ntfyTags(n api.Notification) string {
	tag := map[string]string{
		"attention": "bell",
		"done":      "white_check_mark",
		"exited":    "stop_sign",
		"preview":   "globe_with_meridians",
		"security":  "lock",
		"system":    "computer",
		"schedule":  "alarm_clock",
	}[n.Kind]
	tags := []string{"relay", n.Kind}
	if tag != "" {
		tags = append([]string{tag}, tags...)
	}
	if n.Agent != "" {
		tags = append(tags, headerValue(n.Agent))
	}
	return strings.Join(tags, ",")
}

// headerValue makes s safe for an HTTP header: control characters are
// removed and non-ASCII text is RFC 2047 encoded (ntfy decodes it).
func headerValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	for _, r := range s {
		if r > unicode.MaxASCII {
			return mime.BEncoding.Encode("UTF-8", s)
		}
	}
	return s
}
