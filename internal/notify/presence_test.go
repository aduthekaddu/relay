package notify

import (
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
)

func TestCanonicalPresenceStartupAndNil(t *testing.T) {
	s, d := newTestService(t)
	s.subs.Store(1)
	st := settings{Rules: defaultRules()}
	n := api.Notification{Kind: "attention", SessionID: "t_fixture"}
	if !s.shouldDeliver(n, st, time.Now()) {
		t.Fatal("nil startup presence suppressed delivery")
	}
	// Wiring can install the owner after construction, before concurrent work.
	d.Presence = &fakePresence{watching: map[string]bool{"t_fixture": true}}
	if s.shouldDeliver(n, st, time.Now()) {
		t.Fatal("canonical presence did not suppress delivery")
	}
	n.SessionID = "t_other"
	if !s.shouldDeliver(n, st, time.Now()) {
		t.Fatal("other terminal suppressed")
	}
	n.SessionID = ""
	if !s.shouldDeliver(n, st, time.Now()) {
		t.Fatal("sessionless notification suppressed")
	}
	for _, option := range []Option{WithPresence(nil), WithPresence(func() core.Presence { return nil })} {
		option(s)
		n.SessionID = "t_fixture"
		if !s.shouldDeliver(n, st, time.Now()) {
			t.Fatal("nil override suppressed delivery")
		}
	}
}
