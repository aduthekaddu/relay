package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/setup"
)

type updateSystem struct {
	setup.OSSystem
	goos      string
	calls     []string
	fail      string
	available bool
}

// GOOS returns the operating system selected by the test.
func (s *updateSystem) GOOS() string { return s.goos }

// Getuid returns the synthetic user ID used for launchd domains.
func (s *updateSystem) Getuid() int { return 501 }

// LookPath simulates command discovery using the configured manager availability.
func (s *updateSystem) LookPath(name string) (string, error) {
	if !s.available {
		return "", errors.New("manager unavailable")
	}
	return "/usr/bin/" + name, nil
}

// Output records a service-manager command and fails when it matches the
// command selected by the test.
func (s *updateSystem) Output(_ context.Context, name string, args ...string) (string, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	s.calls = append(s.calls, call)
	if call == s.fail {
		return "", errors.New("synthetic restart failure")
	}
	return "", nil
}

// The update and explicit downgrade paths share restartAfterUpdate. Neither
// may restart the daemon unless --all was explicitly selected.
func TestUpdateRestartOwnership(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		for _, tc := range []struct {
			name                                               string
			all, missing, unavailable, restartFail, healthFail bool
			tag                                                string
		}{
			{name: "serve update", tag: "v2.0.0"},
			{name: "explicit rollback", tag: "v1.0.0"},
			{name: "failed restart", restartFail: true, tag: "v2.0.0"},
			{name: "failed health", healthFail: true, tag: "v2.0.0"},
			{name: "missing serve", missing: true, tag: "v2.0.0"},
			{name: "unavailable manager", unavailable: true, tag: "v2.0.0"},
			{name: "explicit all", all: true, tag: "v2.0.0"},
		} {
			t.Run(goos+"/"+tc.name, func(t *testing.T) {
				root, err := os.MkdirTemp("", "r26u-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(root) })
				t.Setenv("RELAY_HOME", root)
				t.Setenv("RELAY_CONFIG", "")
				t.Setenv("XDG_CONFIG_HOME", root)
				paths, err := config.ResolvePaths()
				if err != nil {
					t.Fatal(err)
				}
				if err = paths.Ensure(); err != nil {
					t.Fatal(err)
				}
				sys := &updateSystem{goos: goos, available: !tc.unavailable}
				dir := filepath.Join(root, "units")
				if !tc.missing {
					if _, err = setup.WriteUnits(goos, dir, setup.UnitData{Binary: "/opt/relay", LogDir: root}); err != nil {
						t.Fatal(err)
					}
				}
				serve, daemon := "systemctl --user restart relay.service", "systemctl --user restart relay-ptyd.service"
				if goos == "darwin" {
					serve, daemon = "launchctl kickstart -k gui/501/dev.relay.serve", "launchctl kickstart -k gui/501/dev.relay.ptyd"
				}
				if tc.restartFail {
					sys.fail = serve
				}
				if !tc.healthFail {
					listener, err := net.Listen("unix", paths.CtlSocket)
					if err != nil {
						t.Fatal(err)
					}
					server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"ok":true,"version":"`+tc.tag+`"}`) })}
					go func() { _ = server.Serve(listener) }()
					t.Cleanup(func() { server.Close() })
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if tc.healthFail {
					cancel()
				}
				ui, done := setup.NewUI(false, io.Discard)
				defer done()
				err = restartManagedUpdate(ctx, ui, setup.Manager{Sys: sys, UnitsDir: dir}, paths, tc.tag, tc.all)
				if (err != nil) != (tc.restartFail || tc.healthFail) {
					t.Fatalf("restart: %v", err)
				}
				var restarts []string
				for _, call := range sys.calls {
					if strings.Contains(call, " restart ") || strings.Contains(call, "kickstart") {
						restarts = append(restarts, call)
					}
				}
				var want []string
				if !tc.missing && !tc.unavailable {
					if tc.all {
						want = append(want, daemon)
					}
					want = append(want, serve)
				}
				if !reflect.DeepEqual(restarts, want) {
					t.Fatalf("restarts %v, want %v", restarts, want)
				}
			})
		}
	}
}
