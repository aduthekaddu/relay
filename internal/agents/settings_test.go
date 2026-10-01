package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
)

func setQuota(t *testing.T, s *Service, on bool) {
	t.Helper()
	if err := s.d.Settings.Update(func(c *config.Config) error { c.Usage.ClaudeQuota = on; return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsQuotaOptOutHidesCacheAndInflight(t *testing.T) {
	s := newTestService(t).Service
	s.d.InitSettings()
	var calls atomic.Int32
	started := make(chan struct{})
	finish := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		<-finish
		w.Write([]byte(`{"five_hour":{"utilization":1}}`))
	}))
	defer server.Close()
	writeFile(t, filepath.Join(s.home, ".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"synthetic-token"}}`)
	s.quota.claudeURL, s.quota.client = server.URL, server.Client()
	setQuota(t, s, true)
	done := make(chan []api.Quota, 1)
	go func() { done <- s.quotas(context.Background()) }()
	<-started
	setQuota(t, s, false)
	close(finish)
	if got := <-done; len(got) != 0 {
		t.Fatal("opt-out returned in-flight result")
	}
	if got := s.quotas(context.Background()); len(got) != 0 || calls.Load() != 1 {
		t.Fatal("opt-out consulted cached quota")
	}
	setQuota(t, s, true)
	if got := s.quotas(context.Background()); len(got) != 1 || calls.Load() != 1 {
		t.Fatal("re-enabled cache policy changed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.quotas(context.Background())
			}
		}()
	}
	for i := 0; i < 100; i++ {
		setQuota(t, s, i%2 == 0)
	}
	wg.Wait()
}

type settingsWorkspaceSource struct{ paths []string }

func (w *settingsWorkspaceSource) List(context.Context) ([]api.Workspace, error) {
	out := []api.Workspace{}
	for _, p := range w.paths {
		out = append(out, api.Workspace{Path: p})
	}
	return out, nil
}
func (w *settingsWorkspaceSource) RootOf(string) string           { return "" }
func (w *settingsWorkspaceSource) Paths(context.Context) []string { return w.paths }

func TestSettingsRootsInvalidateAgentReaderCache(t *testing.T) {
	s := newTestService(t).Service
	base := t.TempDir()
	a := filepath.Join(base, "old")
	b := filepath.Join(base, "next")
	os.Mkdir(a, 0o700)
	os.Mkdir(b, 0o700)
	s.d.Cfg.Agents.WorkspaceRoots = []string{a}
	s.d.InitSettings()
	source := &settingsWorkspaceSource{paths: []string{a}}
	s.d.Workspaces = source
	if got := s.workspaceDirs(context.Background()); !slices.Equal(got, []string{a}) {
		t.Fatalf("first %v", got)
	}
	source.paths = []string{b}
	if err := s.d.Settings.Update(func(c *config.Config) error { c.Agents.WorkspaceRoots = []string{b}; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := s.workspaceDirs(context.Background()); !slices.Equal(got, []string{b}) {
		t.Fatalf("cached %v", got)
	}
	if err := s.d.Settings.Update(func(c *config.Config) error { c.Agents.WorkspaceRoots = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := s.workspaceDirs(context.Background()); len(got) != 0 {
		t.Fatalf("removed root read %v", got)
	}
}

func TestSettingsQuotaUnsupportedStates(t *testing.T) {
	s := newTestService(t).Service
	s.d.InitSettings()
	setQuota(t, s, true)
	if got := s.quotas(context.Background()); len(got) != 0 {
		t.Fatal("invented quota without credentials")
	}
	delete(s.byID, "claude")
	if got := s.quotas(context.Background()); len(got) != 0 {
		t.Fatal("disabled adapter advertised quota")
	}
}

func TestSettingsAgentReaderCachePinsTargetsAndOwnsResults(t *testing.T) {
	s := newTestService(t).Service
	base := t.TempDir()
	allowed, outside := filepath.Join(base, "allowed"), filepath.Join(base, "outside")
	for _, p := range []string{allowed, outside} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(allowed, alias); err != nil {
		t.Fatal(err)
	}
	s.d.Cfg.Agents.WorkspaceRoots = []string{allowed}
	s.d.InitSettings()
	s.d.Workspaces = &settingsWorkspaceSource{paths: []string{alias}}
	got := s.workspaceDirs(context.Background())
	if !slices.Equal(got, []string{allowed}) {
		t.Fatalf("not canonical: %v", got)
	}
	got[0] = outside
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	if got := s.workspaceDirs(context.Background()); !slices.Equal(got, []string{allowed}) {
		t.Fatalf("cached alias/caller changed grant: %v", got)
	}
	if err := os.Rename(allowed, allowed+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, allowed); err != nil {
		t.Fatal(err)
	}
	if got := s.workspaceDirs(context.Background()); len(got) != 0 {
		t.Fatalf("replaced target expanded grant: %v", got)
	}
}
