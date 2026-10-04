package ptyd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Exercise the daemon snapshot/event boundary without starting a PTY or service.
func TestSessionTimestampSnapshots(t *testing.T) {
	at := time.Date(2026, 3, 12, 12, 0, 0, 123456789, time.UTC)
	zero := 0
	for _, tc := range []struct {
		name    string
		info    api.TerminalSession
		present []string
	}{
		{"partial", api.TerminalSession{ID: "fixture-partial"}, nil},
		{"unused-running", api.TerminalSession{ID: "fixture-running", CreatedAt: at}, nil},
		{"active", api.TerminalSession{ID: "fixture-active", CreatedAt: at, LastInputAt: at, LastOutputAt: at}, []string{"lastInputAt", "lastOutputAt"}},
		{"exited", api.TerminalSession{ID: "fixture-exited", CreatedAt: at, ExitedAt: at, ExitCode: &zero}, []string{"exitedAt", "exitCode"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{info: tc.info}
			for _, snapshot := range []*api.TerminalSession{s.Info(), s.updatedLocked().Session} {
				data, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				var obj map[string]json.RawMessage
				if err := json.Unmarshal(data, &obj); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"lastOutputAt", "lastInputAt", "exitedAt", "exitCode"} {
					want := false
					for _, present := range tc.present {
						want = want || key == present
					}
					if _, got := obj[key]; got != want {
						t.Errorf("%s present = %v, want %v in %s", key, got, want, data)
					}
				}
				var decoded api.TerminalSession
				if err := json.Unmarshal(data, &decoded); err != nil {
					t.Fatal(err)
				}
				if !decoded.CreatedAt.Equal(tc.info.CreatedAt) || !decoded.ExitedAt.Equal(tc.info.ExitedAt) || !decoded.LastInputAt.Equal(tc.info.LastInputAt) || !decoded.LastOutputAt.Equal(tc.info.LastOutputAt) {
					t.Fatalf("snapshot times changed: %s", data)
				}
			}
		})
	}
}
