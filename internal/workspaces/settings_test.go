package workspaces

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/store"
)

func workspaceFixture(t *testing.T) (*Service, *core.Deps, string, string, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	old := filepath.Join(base, "old")
	next := filepath.Join(base, "next")
	for _, p := range []string{home, filepath.Join(old, "repo", ".git"), filepath.Join(next, "repo", ".git")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.Files.Root = home
	cfg.Agents.WorkspaceRoots = []string{old}
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := &core.Deps{Cfg: cfg, Paths: config.Paths{Home: home}, Store: st}
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	return s, d, home, old, next
}

func TestSettingsRootsUpdateDiscoveryAuthorizationAndPins(t *testing.T) {
	s, d, home, old, next := workspaceFixture(t)
	ctx := context.Background()
	oldRepo := filepath.Join(old, "repo")
	newRepo := filepath.Join(next, "repo")
	if !slices.Contains(s.discoverRepos(ctx), oldRepo) {
		t.Fatal("old root not discovered")
	}
	if _, err := s.resolveDir(oldRepo); err != nil {
		t.Fatal(err)
	}
	if s.RootOf(oldRepo) != oldRepo {
		t.Fatal("root cache not primed")
	}
	if _, err := s.pin(ctx, oldRepo, true); err != nil {
		t.Fatal(err)
	}
	if err := d.Settings.Update(func(c *config.Config) error { c.Agents.WorkspaceRoots = []string{next}; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := s.discoverRepos(ctx); !slices.Equal(got, []string{newRepo}) {
		t.Fatalf("stale discovery: %v", got)
	}
	if _, err := s.resolveDir(oldRepo); err == nil {
		t.Fatal("removed root still authorized")
	}
	if s.RootOf(oldRepo) != "" {
		t.Fatal("removed root in root cache")
	}
	if got := s.Paths(ctx); slices.Contains(got, oldRepo) || !slices.Contains(got, newRepo) {
		t.Fatalf("stale pin/list: %v", got)
	}
	if _, err := s.resolveDir(home); err != nil {
		t.Fatal("independent files.root grant lost")
	}
	var count int
	d.Store.DB.QueryRow("SELECT COUNT(*) FROM workspace_pins").Scan(&count)
	if count != 1 {
		t.Fatal("root change deleted persistent pin")
	}
}

func TestSettingsWorkspaceSymlinkAndRepositoryBoundaries(t *testing.T) {
	s, d, home, old, next := workspaceFixture(t)
	alias := filepath.Join(home, "escape")
	if err := os.Symlink(next, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveDir(alias); err == nil {
		t.Fatal("symlink escape authorized")
	}
	sibling := old + "-sibling"
	os.MkdirAll(sibling, 0o700)
	if _, err := s.resolveDir(sibling); err == nil {
		t.Fatal("prefix sibling authorized")
	}
	// Pin a configured root's target once. Retargeting its alias cannot expand access.
	rootAlias := filepath.Join(filepath.Dir(home), "root-link")
	os.Symlink(old, rootAlias)
	cfg := config.Clone(d.Cfg)
	cfg.Agents.WorkspaceRoots = []string{rootAlias}
	d.Settings = config.NewRuntime(cfg, home)
	os.Remove(rootAlias)
	os.Symlink(next, rootAlias)
	if _, err := s.resolveDir(filepath.Join(rootAlias, "repo")); err == nil {
		t.Fatal("retargeted root expanded access")
	}
	if _, err := s.resolveDir(filepath.Join(old, "repo")); err != nil {
		t.Fatal("pinned real root lost")
	}
	// An allowed folder below .git does not grant access to the ancestor repo.
	nested := filepath.Join(next, "repo", "allowed")
	os.MkdirAll(nested, 0o700)
	d.Settings = config.NewRuntime(&config.Config{Files: config.FilesConfig{Root: nested}}, home)
	if _, err := s.repoRoot(nested); err == nil {
		t.Fatal("ancestor repository escaped boundary")
	}
	for _, c := range []struct {
		path, root string
		want       bool
	}{{"/a", "/a", true}, {"/ab", "/a", false}, {"/b", "/", true}} {
		if got := within(c.path, c.root); got != c.want {
			t.Errorf("within(%q,%q)=%v", c.path, c.root, got)
		}
	}
}

func TestSettingsWorkspaceConcurrentSnapshots(t *testing.T) {
	s, d, _, old, next := workspaceFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 80; j++ {
				s.Paths(context.Background())
				s.RootOf(filepath.Join(old, "repo"))
				s.resolveDir(filepath.Join(next, "repo"))
			}
		}()
	}
	for i := 0; i < 80; i++ {
		root := old
		if i%2 == 0 {
			root = next
		}
		if err := d.Settings.Update(func(c *config.Config) error { c.Agents.WorkspaceRoots = []string{root}; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}

func TestSettingsReplacedCanonicalRootDoesNotExpandGrant(t *testing.T) {
	s, _, _, old, next := workspaceFixture(t)
	ctx := context.Background()
	if len(s.discoverRepos(ctx)) != 1 {
		t.Fatal("fixture discovery")
	}
	if err := os.Rename(old, old+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(next, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveDir(filepath.Join(old, "repo")); err == nil {
		t.Fatal("replaced root expanded authorization")
	}
	if got := s.discoverRepos(ctx); len(got) != 0 {
		t.Fatalf("replaced root still discovered: %v", got)
	}
	if got := s.RootOf(filepath.Join(old, "repo")); got != "" {
		t.Fatalf("replaced root cache authorized %s", got)
	}
}

func TestSettingsPinnedAliasesUseCanonicalWorkspace(t *testing.T) {
	s, d, home, old, next := workspaceFixture(t)
	ctx := context.Background()
	repo := filepath.Join(old, "repo")
	latest := time.UnixMilli(5000)
	aliases := []string{filepath.Join(home, "alias-a"), filepath.Join(home, "alias-b")}
	for i, alias := range aliases {
		if err := os.Symlink(repo, alias); err != nil {
			t.Fatal(err)
		}
		// Legacy/external pin rows may retain symlink spellings. The current pin API
		// writes canonical paths, but existing persistent rows must be reconciled.
		if _, err := d.Store.DB.ExecContext(ctx,
			"INSERT INTO workspace_pins(path,pinned,at) VALUES(?,1,?)", alias, 4000+i*1000); err != nil {
			t.Fatal(err)
		}
	}
	workspaces := s.collect(ctx)
	for _, workspace := range workspaces {
		t.Logf("collected canonical workspace: %+v", *workspace)
	}
	if len(workspaces) != 1 || workspaces[0].Path != repo || !workspaces[0].Pinned || !workspaces[0].LastUsedAt.Equal(latest) {
		t.Fatalf("canonical pin and discovery must merge: %+v", workspaces)
	}
	if err := d.Settings.Update(func(c *config.Config) error {
		c.Agents.WorkspaceRoots = []string{next}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	workspaces = s.collect(ctx)
	if len(workspaces) != 1 || workspaces[0].Path != filepath.Join(next, "repo") || workspaces[0].Pinned {
		t.Fatalf("removed-root aliases retained authorization: %+v", workspaces)
	}
	var count int
	if err := d.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_pins").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("persistent legacy pins changed: %d", count)
	}
}
