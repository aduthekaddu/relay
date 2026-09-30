package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const settingsKey = "notify.settings"

// settings is the persisted, user-editable part of api.NotifySettings.
// Defaults come from relay.toml [notify]; once the user saves settings in
// the UI they live in the KV table (relay.toml is owned by the settings
// feature and is not rewritten from here).
type settings struct {
	Rules      map[string]bool `json:"rules"`
	QuietStart string          `json:"quietStart,omitempty"`
	QuietEnd   string          `json:"quietEnd,omitempty"`
	NtfyURL    string          `json:"ntfyUrl,omitempty"`
	WebhookURL string          `json:"webhookUrl,omitempty"`
}

// settingsPatch is Partial<NotifySettings>. Devices and vapidKey are
// read-only and accepted (ignored) so clients can send back what they got.
type settingsPatch struct {
	Rules      map[string]bool `json:"rules"`
	QuietStart *string         `json:"quietStart"`
	QuietEnd   *string         `json:"quietEnd"`
	NtfyURL    *string         `json:"ntfyUrl"`
	WebhookURL *string         `json:"webhookUrl"`
	Devices    *int            `json:"devices"`
	VAPIDKey   *string         `json:"vapidKey"`
}

// defaultRules: everything on except the noisy kinds.
func defaultRules() map[string]bool {
	return map[string]bool{
		"attention": true,
		"done":      true,
		"exited":    false,
		"preview":   false,
		"security":  true,
		"system":    true,
		"schedule":  true,
		"custom":    true,
	}
}

func (s *Service) defaultSettings() settings {
	st := settings{Rules: defaultRules()}
	if s.d.Cfg != nil {
		n := s.d.Cfg.Notify
		st.NtfyURL, st.WebhookURL = n.NtfyURL, n.WebhookURL
		if validClock(n.QuietStart) && validClock(n.QuietEnd) {
			st.QuietStart, st.QuietEnd = n.QuietStart, n.QuietEnd
		}
	}
	return st
}

// loadSettings returns the cached settings, reading them once from KV.
func (s *Service) loadSettings(ctx context.Context) (settings, error) {
	s.mu.Lock()
	if s.cached != nil {
		st := s.cached.clone()
		s.mu.Unlock()
		return st, nil
	}
	s.mu.Unlock()
	st := s.defaultSettings()
	raw, ok, err := s.d.Store.GetKV(ctx, settingsKey)
	if err != nil {
		return st, fmt.Errorf("notify: read settings: %w", err)
	}
	if ok {
		var saved settings
		if err := json.Unmarshal([]byte(raw), &saved); err != nil {
			return st, fmt.Errorf("notify: decode settings: %w", err)
		}
		for k, v := range saved.Rules {
			if isKnownKind(k) {
				st.Rules[k] = v
			}
		}
		st.QuietStart, st.QuietEnd = saved.QuietStart, saved.QuietEnd
		st.NtfyURL, st.WebhookURL = saved.NtfyURL, saved.WebhookURL
	}
	s.mu.Lock()
	c := st.clone()
	s.cached = &c
	s.mu.Unlock()
	return st, nil
}

// updateSettings validates and applies a patch, then persists it.
func (s *Service) updateSettings(ctx context.Context, p settingsPatch) (settings, error) {
	st, err := s.loadSettings(ctx)
	if err != nil {
		return st, err
	}
	for k, v := range p.Rules {
		if !isKnownKind(k) {
			return st, &httpx.Err{Status: 400, Code: "bad_request", Message: "unknown notification kind " + strconv.Quote(k), Field: "rules"}
		}
		st.Rules[k] = v
	}
	if p.QuietStart != nil {
		st.QuietStart = strings.TrimSpace(*p.QuietStart)
	}
	if p.QuietEnd != nil {
		st.QuietEnd = strings.TrimSpace(*p.QuietEnd)
	}
	for _, f := range []struct{ name, v string }{{"quietStart", st.QuietStart}, {"quietEnd", st.QuietEnd}} {
		if f.v != "" && !validClock(f.v) {
			return st, &httpx.Err{Status: 400, Code: "bad_request", Message: "use 24-hour HH:MM", Field: f.name}
		}
	}
	if (st.QuietStart == "") != (st.QuietEnd == "") {
		return st, &httpx.Err{Status: 400, Code: "bad_request", Message: "quiet hours need both a start and an end", Field: "quietEnd"}
	}
	if p.NtfyURL != nil {
		u, err := validOutboundURL(*p.NtfyURL, "ntfyUrl")
		if err != nil {
			return st, err
		}
		st.NtfyURL = u
	}
	if p.WebhookURL != nil {
		u, err := validOutboundURL(*p.WebhookURL, "webhookUrl")
		if err != nil {
			return st, err
		}
		st.WebhookURL = u
	}
	b, err := json.Marshal(st)
	if err != nil {
		return st, fmt.Errorf("notify: encode settings: %w", err)
	}
	if err := s.d.Store.SetKV(ctx, settingsKey, string(b)); err != nil {
		return st, fmt.Errorf("notify: save settings: %w", err)
	}
	s.mu.Lock()
	c := st.clone()
	s.cached = &c
	s.mu.Unlock()
	return st, nil
}

// apiSettings renders the public view.
func (s *Service) apiSettings(ctx context.Context, st settings) api.NotifySettings {
	n, _ := s.countSubscriptions(ctx)
	return api.NotifySettings{
		Rules:      st.Rules,
		QuietStart: st.QuietStart,
		QuietEnd:   st.QuietEnd,
		NtfyURL:    st.NtfyURL,
		WebhookURL: st.WebhookURL,
		Devices:    n,
		VAPIDKey:   s.vapidPublic,
	}
}

func (st settings) clone() settings {
	c := st
	c.Rules = make(map[string]bool, len(st.Rules))
	for k, v := range st.Rules {
		c.Rules[k] = v
	}
	return c
}

// hasChannel reports whether any external channel could receive anything.
func (st settings) hasChannel(push bool) bool {
	return push || st.NtfyURL != "" || st.WebhookURL != ""
}

// inQuietHours reports whether t (in local time) falls in [start, end).
// Windows may wrap midnight ("22:00"–"07:00"). Equal or empty bounds mean
// no quiet hours.
func (st settings) inQuietHours(t time.Time) bool {
	start, ok1 := parseClock(st.QuietStart)
	end, ok2 := parseClock(st.QuietEnd)
	if !ok1 || !ok2 || start == end {
		return false
	}
	lt := t.Local()
	m := lt.Hour()*60 + lt.Minute()
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end
}

func validClock(s string) bool { _, ok := parseClock(s); return ok }

// parseClock parses "HH:MM" into minutes after midnight.
func parseClock(s string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(h) == 0 || len(h) > 2 || len(m) != 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// validOutboundURL accepts "" (disable) or an absolute http(s) URL.
func validOutboundURL(raw, field string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(raw) > maxLink {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "must be an http(s) URL", Field: field}
	}
	return u.String(), nil
}
