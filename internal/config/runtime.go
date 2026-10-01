package config

import (
	"maps"
	"slices"
	"sync"
)

// Runtime owns immutable consumer snapshots. Startup Cfg remains unchanged.
// Update serializes read/merge/save/publication, including partial PATCHes.
type Runtime struct {
	mu  sync.RWMutex
	cfg *Config
}

func NewRuntime(c *Config, home string) *Runtime {
	if c == nil {
		c = Defaults()
	}
	next := Clone(c)
	next.normalize(home)
	// Pin authorization roots to their real targets for this snapshot.
	policy := WorkspacePolicy(next, home)
	next.Agents.WorkspaceRoots = policy.Roots
	fileRoot := next.Files.Root
	if fileRoot == "" {
		fileRoot = home
	}
	if real := realDir(fileRoot); real != "" {
		next.Files.Root = real
	}
	return &Runtime{cfg: next}
}

func (r *Runtime) Snapshot() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Clone(r.cfg)
}

// Read holds the settings transaction lock while reading saved state.
func (r *Runtime) Read(read func(*Config) error) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return read(Clone(r.cfg))
}

// Update publishes only after change succeeds. Neither callback nor callers
// receive storage owned by the Runtime.
func (r *Runtime) Update(change func(*Config) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := Clone(r.cfg)
	if err := change(next); err != nil {
		return err
	}
	r.cfg = Clone(next)
	return nil
}

// Clone copies every reference-bearing configuration field.
func Clone(c *Config) *Config {
	if c == nil {
		return Defaults()
	}
	out := *c
	out.Server.TrustedProxies = slices.Clone(c.Server.TrustedProxies)
	out.Agents.WorkspaceRoots = slices.Clone(c.Agents.WorkspaceRoots)
	out.Agents.Disabled = slices.Clone(c.Agents.Disabled)
	out.Previews.Ignore = slices.Clone(c.Previews.Ignore)
	out.Desktop.Apps = slices.Clone(c.Desktop.Apps)
	for i := range out.Desktop.Apps {
		out.Desktop.Apps[i].Command = slices.Clone(c.Desktop.Apps[i].Command)
	}
	out.Apps = slices.Clone(c.Apps)
	for i := range out.Apps {
		out.Apps[i].Command = slices.Clone(c.Apps[i].Command)
		out.Apps[i].Env = maps.Clone(c.Apps[i].Env)
	}
	return &out
}

// Normalized expands saved paths without applying process environment values.
func Normalized(c *Config, home string) *Config {
	out := Clone(c)
	out.normalize(home)
	return out
}
