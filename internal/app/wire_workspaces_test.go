package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/ptyd"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

// Exercise wiring, authentication, handlers and visible Git tasks through an
// actual owned ptyd. Both remotes are local disposable repositories. No TCP
// listener, real account configuration or external Git transport is used.
func TestWorkspaceAPI(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELAY_HOME", filepath.Join(base, "state"))
	t.Setenv("RELAY_CONFIG", "")
	t.Setenv("RELAY_NO_PTYD", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("LC_ALL", "C")
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(base, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitBin, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if _, err := exec.LookPath("gh"); err == nil {
		t.Fatal("fixture unexpectedly has gh")
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, gitBin, append([]string{"--literal-pathspecs", "-C", dir}, args...)...)
		cmd.WaitDelay = time.Second
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %q: %v: %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	write := func(dir, path, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	content := func(dir, path, want string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || string(b) != want {
			t.Fatalf("%s=%q,%v; want %q", path, b, err, want)
		}
	}
	repo := filepath.Join(base, "repo with space")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git(repo, "init", "-q", "--initial-branch=main", "--template=")
	git(repo, "config", "user.name", "Fixture Author")
	git(repo, "config", "user.email", "fixture@example.invalid")
	git(repo, "config", "commit.gpgsign", "false")
	write(repo, "tracked", "baseline\n")
	git(repo, "add", "-A")
	git(repo, "commit", "-qm", "baseline")
	remote := filepath.Join(base, "remote.git")
	if err := os.Mkdir(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	git(remote, "init", "-q", "--bare", "--initial-branch=main", "--template=")
	git(repo, "remote", "add", "origin", remote)
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	paths.Home = base
	// Keep the owner socket short even when the test runner supplies a long TMPDIR.
	paths.PtydSocket = filepath.Join(base, "p.sock")
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Files.Root = repo
	cfg.Agents.WorkspaceRoots = []string{repo}
	cfg.Server.Listen = "127.0.0.1:47728"
	cfg.Server.TLS = "off"
	cfg.Terminal.Shell = "/bin/sh"
	cfg.Terminal.DefaultCwd = repo
	cfg.Terminal.Record = "off"
	cfg.Terminal.ImportTmux = false
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &core.Deps{Cfg: cfg, Paths: paths, Store: st, Bus: events.New(), Log: log, Pty: ptyclient.New(paths.PtydSocket), Search: core.NewSearchRegistry()}
	dm, err := ptyd.New(ptyd.Options{Cfg: cfg, Paths: paths, Log: log, LoginEnv: new(bool)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dm.Run(ctx) }()
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if terms, err := d.Pty.List(cleanup); err == nil {
			for _, term := range terms {
				if term.Activity != api.ActivityExited {
					_ = d.Pty.Kill(cleanup, term.ID, "KILL")
				}
			}
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Error("owned ptyd did not stop")
		}
	})
	waitREL023(t, 5*time.Second, func() bool { return d.Pty.Health(ctx) == nil })
	a := &App{D: d}
	a.Router = server.NewRouter(rel023MetricsAuth{}, a.Origins)
	if err := wireWorkspaces(ctx, a); err != nil {
		t.Fatal(err)
	}
	if d.Workspaces == nil || d.Workspaces.RootOf(repo) != repo {
		t.Fatal("workspace dependency not wired")
	}
	request := func(method, target string, body any, authorized bool, code int, out any) *httptest.ResponseRecorder {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, cfg.Origin()+target, bytes.NewReader(b))
		if authorized {
			r.Header.Set("Authorization", "Bearer test-token")
		}
		w := httptest.NewRecorder()
		a.Router.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("%s %s=%d: %s; want %d", method, target, w.Code, w.Body, code)
		}
		if out != nil {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
		if code >= 400 && code != 404 {
			var e api.ErrorBody
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error.Message == "" {
				t.Fatalf("failure hidden: %s", w.Body)
			}
		}
		return w
	}
	apiRoot := "/api/v1/workspaces"
	write(repo, "tracked", "working\n")
	for _, route := range []string{"stage", "unstage", "discard", "commit", "push", "pull", "worktrees"} {
		request("POST", apiRoot+"/git/"+route, api.GitActionRequest{Path: repo, Files: []string{"tracked"}, Message: "unauthorized", Branch: "unauthorized"}, false, 401, nil)
	}
	content(repo, "tracked", "working\n")
	if git(repo, "diff", "--cached", "--name-only") != "" {
		t.Fatal("unauthorized request staged work")
	}
	var status api.GitStatus
	request("POST", apiRoot+"/git/stage", api.GitActionRequest{Path: repo, Files: []string{"tracked"}}, true, 200, &status)
	if len(status.Files) != 1 || !status.Files[0].Staged || git(repo, "show", ":tracked") != "working" {
		t.Fatal("stage response disagrees with index")
	}
	request("POST", apiRoot+"/git/unstage", api.GitActionRequest{Path: repo}, true, 200, &status)
	content(repo, "tracked", "working\n")
	if git(repo, "diff", "--cached", "--name-only") != "" {
		t.Fatal("unstage failed")
	}
	request("POST", apiRoot+"/git/discard", api.GitActionRequest{Path: repo, Files: []string{"tracked"}}, true, 200, &status)
	content(repo, "tracked", "baseline\n")
	write(repo, "tracked", "committed\n")
	request("POST", apiRoot+"/git/stage", api.GitActionRequest{Path: repo}, true, 200, &status)
	request("POST", apiRoot+"/git/commit", api.GitActionRequest{Path: repo, Message: "API commit"}, true, 200, &status)
	if status.Last == nil || status.Last.Hash != git(repo, "rev-parse", "HEAD") || git(repo, "show", "HEAD:tracked") != "committed" {
		t.Fatal("commit response disagrees with HEAD")
	}
	var ws api.Workspace
	request("POST", apiRoot+"/pin", api.PinWorkspaceRequest{Path: repo, Pinned: true}, true, 200, &ws)
	var pins int
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM workspace_pins WHERE pinned=1").Scan(&pins); err != nil || pins != 1 {
		t.Fatalf("pin storage: %d,%v", pins, err)
	}
	request("POST", apiRoot+"/pin", api.PinWorkspaceRequest{Path: repo, Pinned: false}, true, 200, &ws)
	var list []api.Workspace
	request("GET", apiRoot, nil, true, 200, &list)
	if len(list) != 1 || list[0].Pinned || list[0].Git == nil {
		t.Fatalf("discovery without gh: %+v", list)
	}
	var diff api.GitDiff
	write(repo, "new\nfile", "new\n")
	request("GET", apiRoot+"/git/diff?path="+url.QueryEscape(repo)+"&file="+url.QueryEscape("new\nfile"), nil, true, 200, &diff)
	if !strings.Contains(diff.Diff, "+new") {
		t.Fatal("untracked handler diff missing")
	}
	var commits []api.Commit
	request("GET", apiRoot+"/git/log?path="+url.QueryEscape(repo)+"&limit=1", nil, true, 200, &commits)
	if len(commits) != 1 || commits[0].Subject != "API commit" {
		t.Fatalf("log response: %+v", commits)
	}
	var wt api.Worktree
	request("POST", apiRoot+"/git/worktrees", api.GitActionRequest{Path: repo, Branch: "api-worktree"}, true, 200, &wt)
	if d.Workspaces.RootOf(wt.Path) != wt.Path {
		t.Fatal("worktree launch dependency lost")
	}
	live, err := d.Pty.Create(ctx, ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Command: []string{"/bin/sleep", "30"}, Cwd: wt.Path}, Workspace: wt.Path})
	if err != nil {
		t.Fatal(err)
	}
	request("DELETE", apiRoot+"/git/worktrees?path="+url.QueryEscape(wt.Path), nil, true, 409, nil)
	content(wt.Path, "tracked", "committed\n")
	if err := d.Pty.Kill(ctx, live.ID, "KILL"); err != nil {
		t.Fatal(err)
	}
	waitREL023(t, 5*time.Second, func() bool {
		term, err := d.Pty.Get(ctx, live.ID)
		return err == nil && term.Activity == api.ActivityExited
	})
	request("DELETE", apiRoot+"/git/worktrees?path="+url.QueryEscape(wt.Path), nil, true, 204, nil)
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("removed worktree still exists: %v", err)
	}
	// PR delivery belongs to REL-066. The existing backend has no PR route,
	// and absence of gh must not cause a success claim or a shell fallback.
	request("POST", apiRoot+"/git/pr", api.GitActionRequest{Path: repo}, true, 404, nil)
	task := func(verb string, wantCode int, output string) *api.TerminalSession {
		t.Helper()
		var response api.GitTaskResponse
		request("POST", apiRoot+"/git/"+verb, api.GitActionRequest{Path: repo}, true, 202, &response)
		if response.Terminal == nil || response.Terminal.Kind != api.KindTask || response.Terminal.Cwd != repo {
			t.Fatalf("task response: %+v", response)
		}
		var term *api.TerminalSession
		waitREL023(t, 10*time.Second, func() bool {
			var err error
			term, err = d.Pty.Get(ctx, response.Terminal.ID)
			return err == nil && term.ExitCode != nil
		})
		if *term.ExitCode != wantCode {
			t.Fatalf("git %s exit=%d, want %d; preview=%q", verb, *term.ExitCode, wantCode, term.Preview)
		}
		snapshot, err := d.Pty.Snapshot(ctx, term.ID, 100)
		if err != nil || output != "" && !strings.Contains(strings.Join(strings.Fields(snapshot.Text), " "), output) {
			t.Fatalf("visible git %s output=%+v,%v; want %q", verb, snapshot, err, output)
		}
		t.Logf("local git %s task exited %d; expected output present=%v", verb, *term.ExitCode, output == "" || strings.Contains(strings.Join(strings.Fields(snapshot.Text), " "), output))
		return term
	}
	task("push", 0, "")
	if git(remote, "rev-parse", "refs/heads/main") != git(repo, "rev-parse", "HEAD") || git(repo, "rev-parse", "--abbrev-ref", "@{upstream}") != "origin/main" {
		t.Fatal("initial push did not update local remote or upstream")
	}
	peer := filepath.Join(base, "peer")
	git(base, "clone", "-q", "--", remote, peer)
	git(peer, "config", "user.name", "Peer Fixture")
	git(peer, "config", "user.email", "peer@example.invalid")
	git(peer, "config", "commit.gpgsign", "false")
	write(peer, "tracked", "peer update\n")
	git(peer, "commit", "-qam", "peer update")
	git(peer, "push", "-q")
	task("pull", 0, "")
	content(repo, "tracked", "peer update\n")
	if git(repo, "rev-parse", "HEAD") != git(remote, "rev-parse", "main") {
		t.Fatal("pull did not fast forward")
	}
	write(repo, "tracked", "dirty local work\n")
	write(peer, "tracked", "second peer\n")
	git(peer, "commit", "-qam", "second peer")
	git(peer, "push", "-q")
	head := git(repo, "rev-parse", "HEAD")
	task("pull", 1, "would be overwritten")
	content(repo, "tracked", "dirty local work\n")
	if git(repo, "rev-parse", "HEAD") != head {
		t.Fatal("failed pull moved HEAD")
	}
	git(repo, "add", "--", "tracked")
	git(repo, "commit", "-qm", "local divergent work")
	head = git(repo, "rev-parse", "HEAD")
	task("pull", 128, "Not possible to fast-forward")
	content(repo, "tracked", "dirty local work\n")
	if git(repo, "rev-parse", "HEAD") != head {
		t.Fatal("ff-only failure moved HEAD")
	}
	remoteHead := git(remote, "rev-parse", "main")
	task("push", 1, "rejected")
	if git(remote, "rev-parse", "main") != remoteHead || git(repo, "rev-parse", "HEAD") != head {
		t.Fatal("rejected push changed either HEAD")
	}
	git(repo, "remote", "set-url", "origin", filepath.Join(base, "missing.git"))
	task("push", 128, "does not appear to be a git repository")
	content(repo, "tracked", "dirty local work\n")
}
