package terminal

import (
	"context"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

const (
	eventBackoffMin = 500 * time.Millisecond
	eventBackoffMax = 10 * time.Second
	// notifyTimeout bounds one Notifier call.
	notifyTimeout = 10 * time.Second
)

// relayEvents subscribes to ptyd events and republishes them on the bus
// until ctx is done, reconnecting with backoff when the daemon goes away.
// After every (re)connect the current sessions are published as
// terminal.updated so browsers catch up on anything missed.
func (s *Service) relayEvents(ctx context.Context) {
	backoff := eventBackoffMin
	for ctx.Err() == nil {
		ch, err := s.pty.Events(ctx)
		if err != nil {
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, eventBackoffMax)
			continue
		}
		backoff = eventBackoffMin
		s.resync(ctx)
		for ev := range ch {
			s.handleEvent(ctx, ev)
		}
		// Channel closed: daemon restarted or connection dropped.
		if !sleepCtx(ctx, eventBackoffMin) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// resync publishes every known session after a (re)connect.
func (s *Service) resync(ctx context.Context) {
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	list, err := s.pty.List(lctx)
	if err != nil {
		return
	}
	for i := range list {
		s.d.Bus.Publish(api.EvTerminalUpdated, &list[i])
	}
}

// handleEvent maps one ptyd event onto the bus, the notifier and the
// clipboard hook.
func (s *Service) handleEvent(ctx context.Context, ev ptyclient.PtyEvent) {
	switch ev.Type {
	case "created":
		if ev.Session != nil {
			s.d.Bus.Publish(api.EvTerminalCreated, ev.Session)
		}
	case "updated":
		if ev.Session != nil {
			s.d.Bus.Publish(api.EvTerminalUpdated, ev.Session)
		}
	case "exited":
		if ev.Session != nil {
			s.d.Bus.Publish(api.EvTerminalExited, ev.Session)
		}
	case "removed":
		s.d.Bus.Publish(api.EvTerminalRemoved, map[string]string{"id": ev.ID})
	case "notify", "bell":
		s.notifyAttention(ctx, ev)
	case "clip":
		if s.OnClip != nil && ev.Text != "" {
			s.OnClip(ctx, ev.ID, ev.Text)
		}
	case "open":
		if ev.Path != "" {
			s.d.Bus.Publish(api.EvOpen, map[string]string{"path": ev.Path})
		}
	}
}

// notifyAttention sends an "attention" notification for agent sessions
// that rang the bell or sent an OSC 9/777/99 notification. Shells ringing
// the bell (tab completion) never notify.
func (s *Service) notifyAttention(ctx context.Context, ev ptyclient.PtyEvent) {
	sess := ev.Session
	if sess == nil || sess.Kind != api.KindAgent {
		return
	}
	n := s.d.Notifier // read at call time: wiring may set it later
	if n == nil {
		return
	}
	req := api.NotifyRequest{
		Kind:      "attention",
		Title:     attentionTitle(sess, ev),
		Body:      attentionBody(sess, ev),
		Link:      "/terminal/" + sess.ID,
		SessionID: sess.ID,
		Agent:     sess.Agent,
		Severity:  "info",
	}
	nctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	if _, err := n.Notify(nctx, req); err != nil {
		s.d.Log.Debug("attention notification failed", "session", sess.ID, "err", err)
	}
}

func attentionTitle(sess *api.TerminalSession, ev ptyclient.PtyEvent) string {
	if ev.Type == "notify" && ev.Title != "" && ev.Body != "" {
		return truncate(ev.Title, 120)
	}
	name := sess.Name
	if name == "" {
		name = sess.Agent
	}
	return truncate(name+" needs you", 120)
}

func attentionBody(sess *api.TerminalSession, ev ptyclient.PtyEvent) string {
	switch {
	case ev.Type == "notify" && ev.Body != "":
		return truncate(ev.Body, 500)
	case ev.Type == "notify" && ev.Title != "":
		return truncate(ev.Title, 500)
	}
	return truncate(lastLine(sess.Preview), 500)
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n ")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

// truncate cuts s to at most n bytes on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
