package ptyd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

func TestDeleteMutationOutcome(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	ctx := context.Background()
	for _, tc := range []struct{ name, signal string }{{"default", ""}, {"HUP", "HUP"}, {"TERM", "TERM"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := e.create(shSpec(`trap "" HUP TERM; echo ready; read x`))
			e.waitSession(s.ID, time.Second*3, "ready", func(*api.TerminalSession) bool {
				snap, err := e.c.Snapshot(ctx, s.ID, 10)
				return err == nil && strings.Contains(snap.Text, "ready")
			})
			var wg sync.WaitGroup
			results := make(chan bool, 12)
			for range 12 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					res, err := e.c.Delete(ctx, s.ID, ptyclient.DeleteSpec{Signal: tc.signal})
					if err != nil {
						t.Error(err)
						return
					}
					results <- res.Changed
				}()
			}
			wg.Wait()
			close(results)
			n := 0
			for changed := range results {
				if changed {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("changed close outcomes = %d, want 1", n)
			}
			// Compatibility wrapper shares the same lifecycle.
			if err := e.c.Kill(ctx, s.ID, tc.signal); err != nil {
				t.Fatal(err)
			}
			if err := e.c.Kill(ctx, s.ID, "KILL"); err != nil {
				t.Fatal(err)
			}
			e.waitExit(s.ID, 3*time.Second)
			res, err := e.c.Delete(ctx, s.ID, ptyclient.DeleteSpec{})
			if err != nil || res.Changed {
				t.Fatalf("exited close = %+v, %v", res, err)
			}
		})
	}
	s := e.create(shSpec("exit 0"))
	e.waitExit(s.ID, 3*time.Second)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.c.Delete(ctx, s.ID, ptyclient.DeleteSpec{Forget: true})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	n := 0
	for err := range results {
		if err == nil {
			n++
		} else if !errors.Is(err, ptyclient.ErrNotFound) {
			t.Error(err)
		}
	}
	if n != 1 {
		t.Fatalf("successful forgets = %d, want 1", n)
	}
}

func TestKillNoProcessDoesNotClaimMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pid    int
		exited bool
	}{{"missing pid", 0, false}, {"exited", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{done: make(chan struct{}), info: api.TerminalSession{Pid: tc.pid}}
			if tc.exited {
				close(s.done)
			}
			for _, sig := range []syscall.Signal{0, syscall.SIGTERM, syscall.SIGKILL} {
				changed, err := s.Kill(sig)
				if changed || err != nil {
					t.Fatalf("kill = %v, %v", changed, err)
				}
			}
			if s.closing {
				t.Fatal("no process started escalation")
			}
		})
	}
}

func TestDeleteDifferentSignalStillDelivered(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	ctx := context.Background()
	s := e.create(shSpec(`trap "" HUP; echo ready; read x`))
	e.waitSession(s.ID, 3*time.Second, "ready", func(*api.TerminalSession) bool {
		snap, err := e.c.Snapshot(ctx, s.ID, 10)
		return err == nil && strings.Contains(snap.Text, "ready")
	})
	for _, sig := range []string{"HUP", "TERM"} {
		res, err := e.c.Delete(ctx, s.ID, ptyclient.DeleteSpec{Signal: sig})
		if err != nil || !res.Changed {
			t.Fatalf("%s delivery = %+v, %v", sig, res, err)
		}
	}
	done := e.waitExit(s.ID, time.Second)
	if done.ExitCode == nil || *done.ExitCode != 128+15 {
		t.Fatalf("TERM did not terminate immediately: %+v", done.ExitCode)
	}
}
