package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aduthekaddu/relay/internal/config"
)

// fakeSys records commands and answers them from a table keyed by the
// full command line.
type fakeSys struct {
	mu       sync.Mutex
	goos     string
	uid      int
	user     string
	bins     map[string]string // name → path; missing = not found
	out      map[string]string // "cmd arg…" → output
	fail     map[string]bool   // "cmd arg…" → exit non-zero
	calls    []string
	interact []string
}

func newFakeSys(goos string) *fakeSys {
	return &fakeSys{goos: goos, uid: 1000, user: "tester", bins: map[string]string{}, out: map[string]string{}, fail: map[string]bool{}}
}

func (f *fakeSys) GOOS() string { return f.goos }

func (f *fakeSys) LookPath(name string) (string, error) {
	if p, ok := f.bins[name]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func (f *fakeSys) Output(_ context.Context, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, line)
	if f.fail[line] {
		return f.out[line], errors.New("exit status 1")
	}
	if o, ok := f.out[line]; ok {
		return o, nil
	}
	return "", nil
}

func (f *fakeSys) Interactive(_ context.Context, _ []string, name string, args ...string) error {
	line := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interact = append(f.interact, line)
	if f.fail[line] {
		return errors.New("exit status 1")
	}
	return nil
}

func (f *fakeSys) Getuid() int      { return f.uid }
func (f *fakeSys) Username() string { return f.user }

func (f *fakeSys) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range append(append([]string(nil), f.calls...), f.interact...) {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// tempPaths lays out a RELAY_HOME-style tree in a temp dir.
func tempPaths(t *testing.T) config.Paths {
	t.Helper()
	root := t.TempDir()
	p := config.Paths{
		Home:       root,
		ConfigDir:  filepath.Join(root, "config"),
		DataDir:    filepath.Join(root, "data"),
		RuntimeDir: filepath.Join(root, "run"),
		CacheDir:   filepath.Join(root, "cache"),
	}
	p.ConfigFile = filepath.Join(p.ConfigDir, "relay.toml")
	p.DB = filepath.Join(p.DataDir, "relay.db")
	p.PtydSocket = filepath.Join(p.RuntimeDir, "ptyd.sock")
	p.CtlSocket = filepath.Join(p.RuntimeDir, "relay.sock")
	p.Uploads = filepath.Join(p.DataDir, "uploads")
	p.Recordings = filepath.Join(p.DataDir, "recordings")
	return p
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}
