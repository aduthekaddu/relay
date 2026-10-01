package apps

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/previews/revproxy"
)

func ownedIdleProc(t *testing.T) *proc {
	t.Helper()
	p := newProc(procSpec{Name: "settings-fixture", Argv: []string{"/bin/sleep", "60"}, StopGrace: 100 * time.Millisecond}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := p.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return p
}

func TestSettingsCodeIdleUsesSharedSnapshot(t *testing.T) {
	d := testDeps(t, nil)
	d.InitSettings()
	s := &Service{d: d, lookPath: exec.LookPath, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	code := s.codeApp()
	code.proc = ownedIdleProc(t)
	s.apps = map[string]*webApp{"code": code}
	s.order = []string{"code"}
	s.desk = &desktop{d: d, unavailable: "fixture desktop unavailable"}
	now := time.Now().Add(time.Hour)
	set := func(minutes int) {
		t.Helper()
		if err := d.Settings.Update(func(c *config.Config) error {
			c.Code.IdleStop.Duration = time.Duration(minutes) * time.Minute
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	set(0)
	s.checkIdle(context.Background(), now)
	if st, _, _ := code.proc.Status(); st != stateRunning {
		t.Fatal("zero idle stopped code")
	}
	set(1)
	code.act = new(revproxy.Activity)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	proxy := revproxy.New(revproxy.Target{Host: "localhost", Dial: func(context.Context) (net.Conn, error) {
		close(started)
		<-release
		return nil, errors.New("synthetic upstream")
	}}, revproxy.Options{Activity: code.act})
	defer proxy.Close()
	go func() {
		defer close(done)
		proxy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://fixture.invalid", nil))
	}()
	<-started
	s.checkIdle(context.Background(), now)
	if st, _, _ := code.proc.Status(); st != stateRunning {
		t.Fatal("active Code stopped")
	}
	once.Do(func() { close(release) })
	<-done
	s.checkIdle(context.Background(), now.Add(time.Hour))
	if st, _, _ := code.proc.Status(); st != stateStopped {
		t.Fatal("new idle limit not adopted")
	}
	if d.Cfg.Code.IdleStop.Duration != 2*time.Hour {
		t.Fatal("startup config mutated")
	}
}

func TestSettingsDesktopIdlePreservesViewersAndUnsupportedState(t *testing.T) {
	d := testDeps(t, nil)
	d.InitSettings()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	k := &desktop{d: d, log: log, xvnc: ownedIdleProc(t), session: ownedIdleProc(t), scanner: &runningScanner{procDir: t.TempDir()}, lastUse: time.Now()}
	t.Cleanup(func() { k.Stop(context.Background()) })
	set := func(minutes int) {
		t.Helper()
		if err := d.Settings.Update(func(c *config.Config) error {
			c.Desktop.IdleStop.Duration = time.Duration(minutes) * time.Minute
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Add(time.Hour)
	set(0)
	k.checkIdle(context.Background(), now)
	if st, _ := k.status(); st != stateRunning {
		t.Fatal("zero idle stopped desktop")
	}
	set(1)
	k.viewers = 1
	k.checkIdle(context.Background(), now)
	if st, _ := k.status(); st != stateRunning {
		t.Fatal("active viewer stopped desktop")
	}
	k.viewers = 0
	k.checkIdle(context.Background(), now)
	if st, _ := k.status(); st != stateStopped {
		t.Fatal("desktop retained old idle limit")
	}
	disabled, err := newDesktop(testDeps(t, func(c *config.Config) { c.Desktop.Enabled = false }), log, fakeLookPath())
	if err != nil {
		t.Fatal(err)
	}
	disabled.checkIdle(context.Background(), now)
	if got := disabled.State(); got.State != "unavailable" {
		t.Fatal("idle update invented availability")
	}
}
