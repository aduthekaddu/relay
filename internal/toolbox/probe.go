package toolbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SearchPath returns the directories searched for tools: $PATH plus the
// usual per-user install locations. Services started by systemd get a
// minimal PATH, so without these a freshly installed agent CLI would look
// missing.
func SearchPath(home string) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || !filepath.IsAbs(d) || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		add(d)
	}
	for _, d := range []string{
		".local/bin", ".local/share/relay/tools/node/bin", ".cargo/bin", "go/bin",
		".bun/bin", ".opencode/bin", ".npm-global/bin", ".deno/bin", ".claude/local",
	} {
		add(filepath.Join(home, d))
	}
	for _, d := range []string{
		"/usr/local/bin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/sbin", "/sbin",
		"/snap/bin", "/opt/homebrew/bin", "/usr/local/go/bin",
	} {
		add(d)
	}
	return dirs
}

// Finder resolves executables against a fixed search path.
type Finder struct {
	Dirs []string
}

// Find returns the absolute path of name. Names containing a slash are
// checked as-is.
func (f Finder) Find(name string) (string, bool) {
	if strings.Contains(name, "/") {
		if isExecutable(name) {
			return name, true
		}
		return "", false
	}
	for _, d := range f.Dirs {
		p := filepath.Join(d, name)
		if isExecutable(p) {
			return p, true
		}
	}
	return "", false
}

// Env returns a PATH=… entry for subprocesses that matches Dirs.
func (f Finder) Env() string { return "PATH=" + strings.Join(f.Dirs, string(os.PathListSeparator)) }

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// Resolve checks every requirement of r. It returns the resolved path of
// the first requirement's first present alternative, and whether all
// requirements are met.
func (f Finder) Resolve(r *Recipe) (first string, ok bool) {
	for i, alts := range r.Check {
		found := ""
		for _, a := range alts {
			if p, ok := f.Find(a); ok {
				found = p
				break
			}
		}
		if found == "" {
			return "", false
		}
		if i == 0 {
			first = found
		}
	}
	return first, true
}

// Runner runs a command and returns its combined output (capped).
type Runner func(ctx context.Context, env []string, argv []string) (string, error)

// ExecRunner runs argv in its own process group, killing the whole group
// on timeout, and returns at most 64 KiB of combined output.
func ExecRunner(ctx context.Context, env []string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin = nil
	var buf capBuffer
	buf.max = 64 << 10
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	setProcessGroup(cmd)
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return buf.String(), err
}

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid: signal the whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

type capBuffer struct {
	mu  sync.Mutex
	b   bytes.Buffer
	max int
}

func (c *capBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
		} else {
			c.b.Write(p)
		}
	}
	return len(p), nil
}

func (c *capBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

var versionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

// ParseVersion extracts the first dotted version number from output.
func ParseVersion(out string) string {
	return versionPattern.FindString(out)
}

// Prober checks whether recipes are installed and reads their versions.
type Prober struct {
	Finder      Finder
	Run         Runner
	Timeout     time.Duration // per version command
	Concurrency int
	Env         []string // extra environment for version commands
}

// Status is the probe result for one recipe.
type Status struct {
	Installed bool
	Version   string
	Path      string
}

// Probe checks one recipe.
func (p *Prober) Probe(ctx context.Context, r *Recipe) Status {
	path, ok := p.Finder.Resolve(r)
	if !ok {
		return Status{}
	}
	st := Status{Installed: true, Path: path}
	if len(r.Version) == 0 || p.Run == nil {
		return st
	}
	argv := make([]string, len(r.Version))
	for i, a := range r.Version {
		argv[i] = strings.ReplaceAll(a, "{bin}", path)
	}
	if !strings.Contains(argv[0], "/") {
		if abs, ok := p.Finder.Find(argv[0]); ok {
			argv[0] = abs
		}
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := append([]string{p.Finder.Env(), "NO_COLOR=1", "TERM=dumb", "CI=1"}, p.Env...)
	out, _ := p.Run(cctx, env, argv) // many tools exit non-zero on --version; parse anyway
	st.Version = ParseVersion(out)
	return st
}

// ProbeAll checks recipes concurrently (bounded) and returns results in
// the same order.
func (p *Prober) ProbeAll(ctx context.Context, rs []*Recipe) []Status {
	out := make([]Status, len(rs))
	n := p.Concurrency
	if n <= 0 {
		n = 4
	}
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for i, r := range rs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			out[i] = p.Probe(ctx, r)
		}()
	}
	wg.Wait()
	return out
}
