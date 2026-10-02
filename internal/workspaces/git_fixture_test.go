package workspaces

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// Every repository, hook, worktree and remote belongs to this test. Ignore
// machine Git configuration and prohibit all transports except local files.
func gitFixture(t *testing.T, committed bool) (*Service, string) {
	t.Helper()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("LC_ALL", "C")
	root := cleanReal(t.TempDir())
	t.Setenv("RELAY_HOME", filepath.Join(root, "relay-home"))
	gitOK(t, root, "init", "--quiet", "--initial-branch=main", "--template=")
	gitOK(t, root, "config", "user.name", "Fixture Author")
	gitOK(t, root, "config", "user.email", "fixture@example.invalid")
	gitOK(t, root, "config", "commit.gpgsign", "false")
	if committed {
		writeFixture(t, root, "tracked.txt", "base\n")
		gitOK(t, root, "add", "--", "tracked.txt")
		gitOK(t, root, "commit", "--quiet", "-m", "fixture baseline")
	}
	cfg := config.Defaults()
	cfg.Files.Root = root
	cfg.Agents.WorkspaceRoots = []string{root}
	s, err := New(&core.Deps{Cfg: cfg, Paths: config.Paths{Home: root}})
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func gitCall(t *testing.T, root string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--literal-pathspecs", "-C", root}, args...)...)
	cmd.WaitDelay = time.Second
	return cmd.CombinedOutput()
}

func gitOK(t *testing.T, root string, args ...string) string {
	t.Helper()
	b, err := gitCall(t, root, args...)
	if err != nil {
		t.Fatalf("git %q: %v: %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func writeFixture(t *testing.T, root, path, content string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func wantContent(t *testing.T, root, path, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil || string(b) != want {
		t.Fatalf("%q content = %q, %v; want %q", path, b, err, want)
	}
}

func wantMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%q still exists or failed to stat: %v", path, err)
	}
}

func wantHTTPError(t *testing.T, err error, code int, message string) {
	t.Helper()
	var he *httpx.Err
	if !errors.As(err, &he) || he.Status != code || !strings.Contains(he.Message, message) {
		t.Fatalf("error = %v; want HTTP %d containing %q", err, code, message)
	}
}

func callHandler(t *testing.T, fn http.HandlerFunc, method, target string, body any, code int) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, target, bytes.NewReader(b))
	w := httptest.NewRecorder()
	fn(w, r)
	if w.Code != code {
		t.Fatalf("%s %s = %d: %s; want %d", method, target, w.Code, w.Body, code)
	}
	if code >= 400 {
		var e api.ErrorBody
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error.Code == "" || e.Error.Message == "" {
			t.Fatalf("missing structured failure: %s, %v", w.Body, err)
		}
	}
	return w
}

func statusFixture(t *testing.T, s *Service, root string) *api.GitStatus {
	t.Helper()
	st, err := s.status(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func fileStatus(t *testing.T, st *api.GitStatus, path string) api.GitFile {
	t.Helper()
	for _, f := range st.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("missing %q in status: %+v", path, st.Files)
	return api.GitFile{}
}
