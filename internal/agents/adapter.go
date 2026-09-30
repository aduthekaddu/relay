package agents

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Adapter describes one coding-agent CLI: identity, how to find and run it,
// and (optionally) how to read its on-disk history and install hooks.
//
// Every adapter lives in its own file (adapter_<id>.go) and is returned by
// builtinAdapters. Command builders receive the resolved binary path and
// must return argv slices: user text (prompts, models, ids) is always a
// single argv element, never interpolated into a shell string.
type Adapter struct {
	ID          string
	Name        string
	Vendor      string
	Color       string   // brand accent, hex
	Binaries    []string // executable names, in preference order
	ExtraDirs   []string // extra install dirs relative to $HOME (beyond the common ones)
	InstallHint string   // toolbox recipe id
	// Caps holds the static capabilities; Installed/Hooks/Sessions are
	// filled at runtime.
	Caps api.AgentCapabilities

	// Interactive returns argv for an interactive session; prompt and
	// model may be empty. Required.
	Interactive func(bin, prompt, model string) []string
	// PresetID, when set, returns extra arguments that make a new
	// interactive session use a pre-chosen native id (claude --session-id),
	// so the terminal is paired with its transcript without guessing.
	PresetID func() (args []string, nativeID string)
	// Resume returns argv to resume a native session (nil: unsupported).
	Resume func(bin, nativeID string) []string
	// Fork returns argv to fork a native session into a new one (nil: unsupported).
	Fork func(bin, nativeID string) []string
	// Headless returns argv for a one-shot run printing the answer on
	// stdout (nil: unsupported).
	Headless func(bin, prompt, model string) []string

	// History reads transcripts from disk (nil: none).
	History historyReader
	// Hooks installs attention hooks into the agent's config (nil: none).
	Hooks hookInstaller
	// ParseHook turns a hook invocation into an attention/done event.
	ParseHook func(event string, payload []byte) hookEvent
}

// builtinAdapters returns a fresh set of every supported adapter.
func builtinAdapters() []*Adapter {
	return []*Adapter{
		newClaude(), newCodex(), newGemini(), newOpenCode(), newKiro(),
		newCursor(), newGrok(), newPi(), newHermes(), newAmp(),
		newCopilot(), newAider(), newQwen(), newCrush(),
	}
}

// commonBinDirs are searched (relative to $HOME, then absolute) after
// $PATH, because relay often runs under systemd with a minimal PATH.
var commonBinDirs = []string{
	".local/bin", "bin", ".npm-global/bin", ".bun/bin", ".cargo/bin", "go/bin",
	".volta/bin", ".deno/bin", ".yarn/bin", ".local/share/pnpm",
	"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin",
}

// detection is the cached result of locating an adapter's binary.
type detection struct {
	Binary  string
	Version string
	At      time.Time
}

// detector finds binaries and caches `--version` output.
type detector struct {
	home     string
	lookPath func(string) (string, error)
	run      func(ctx context.Context, bin string, args ...string) ([]byte, error)
	ttl      time.Duration

	mu    sync.Mutex
	cache map[string]detection // adapter id -> detection
	vers  map[string]string    // "path|mtime" -> version
}

func newDetector(home string) *detector {
	return &detector{
		home:     home,
		lookPath: exec.LookPath,
		run: func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Stdin = nil
			cmd.Env = append(os.Environ(), "NO_COLOR=1", "CI=1", "TERM=dumb")
			cmd.WaitDelay = 500 * time.Millisecond
			return cmd.CombinedOutput()
		},
		ttl:   10 * time.Minute,
		cache: map[string]detection{},
		vers:  map[string]string{},
	}
}

// find returns the absolute path of the first executable found for a.
func (d *detector) find(a *Adapter) string {
	for _, name := range a.Binaries {
		if p, err := d.lookPath(name); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
	}
	dirs := append(append([]string{}, a.ExtraDirs...), commonBinDirs...)
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(d.home, dir)
		}
		for _, name := range a.Binaries {
			p := filepath.Join(dir, name)
			if isExecutable(p) {
				return p
			}
		}
	}
	return ""
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// detect returns the cached detection for a, refreshing it when stale.
func (d *detector) detect(ctx context.Context, a *Adapter) detection {
	d.mu.Lock()
	c, ok := d.cache[a.ID]
	d.mu.Unlock()
	if ok && time.Since(c.At) < d.ttl {
		return c
	}
	c = detection{Binary: d.find(a), At: time.Now()}
	if c.Binary != "" {
		c.Version = d.version(ctx, c.Binary)
	}
	d.mu.Lock()
	d.cache[a.ID] = c
	d.mu.Unlock()
	return c
}

// cached returns the last detection without running anything.
func (d *detector) cached(id string) (detection, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.cache[id]
	return c, ok
}

var versionRe = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?(?:[-+.][0-9A-Za-z]+)*`)

// version runs `bin --version` (2 s timeout) and extracts a version token.
// Results are cached per binary path and modification time.
func (d *detector) version(ctx context.Context, bin string) string {
	key := bin
	if st, err := os.Stat(bin); err == nil {
		key = bin + "|" + st.ModTime().UTC().Format(time.RFC3339Nano)
	}
	d.mu.Lock()
	v, ok := d.vers[key]
	d.mu.Unlock()
	if ok {
		return v
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := d.run(ctx, bin, "--version")
	if err != nil && len(out) == 0 {
		return ""
	}
	v = parseVersion(string(out))
	d.mu.Lock()
	d.vers[key] = v
	d.mu.Unlock()
	return v
}

// parseVersion extracts the first version-looking token from CLI output.
func parseVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if m := versionRe.FindString(line); m != "" {
			return m
		}
	}
	return ""
}

// errUnknownAgent is returned for adapter ids that do not exist or are disabled.
var errUnknownAgent = errors.New("unknown agent")

// withPrompt appends prompt as a single argv element when non-empty. A
// prompt that starts with "-" gets a leading space so the agent CLI can
// never parse it as a flag (argument injection such as
// "--dangerously-skip-permissions").
func withPrompt(argv []string, prompt string) []string {
	if prompt != "" {
		argv = append(argv, safeArg(prompt))
	}
	return argv
}

// safeArg neutralises a leading dash in free text.
func safeArg(s string) string {
	if strings.HasPrefix(s, "-") {
		return " " + s
	}
	return s
}

var (
	modelRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@\[\]=,+ -]{0,127}$`)
	nativeIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,199}$`)
)

// validModel reports whether a model name is safe to pass as an argument.
func validModel(m string) bool { return m == "" || modelRe.MatchString(m) }

// validNativeID reports whether a native session id is safe to pass as an argument.
func validNativeID(id string) bool { return nativeIDRe.MatchString(id) }

// withFlag appends flag and value when value is non-empty.
func withFlag(argv []string, flag, value string) []string {
	if value != "" {
		argv = append(argv, flag, value)
	}
	return argv
}

// newUUID returns a random RFC 4122 version 4 UUID.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
