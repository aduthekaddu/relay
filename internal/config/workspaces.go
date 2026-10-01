package config

import (
	"os"
	"path/filepath"
	"strings"
)

// WorkspacePaths is a single discovery and authorization policy. Files.Root
// remains an independent grant even when a workspace root is removed.
type WorkspacePaths struct {
	Roots   []string
	Allowed []string
}

func WorkspacePolicy(c *Config, home string) WorkspacePaths {
	return workspacePolicy(c, home, false)
}

// PinnedWorkspacePolicy uses the real targets saved in a runtime snapshot.
// A directory replaced by a symlink cannot turn an existing grant into a
// grant for a different target. Explicit PATCH or restart pins new targets.
func PinnedWorkspacePolicy(c *Config, home string) WorkspacePaths {
	return workspacePolicy(c, home, true)
}

func workspacePolicy(c *Config, home string, pinned bool) WorkspacePaths {
	resolve := func(path string) string {
		path = expand(path, home)
		real := realDir(path)
		if pinned && real != filepath.Clean(path) {
			return ""
		}
		return real
	}
	p := WorkspacePaths{Roots: []string{}, Allowed: []string{}}
	fileRoot := c.Files.Root
	if fileRoot == "" {
		fileRoot = home
	}
	seen := map[string]bool{}
	if real := resolve(fileRoot); real != "" {
		p.Allowed = append(p.Allowed, real)
		seen[real] = true
	}
	roots := map[string]bool{}
	for _, root := range c.Agents.WorkspaceRoots {
		if real := resolve(root); real != "" {
			if !roots[real] {
				p.Roots = append(p.Roots, real)
				roots[real] = true
			}
			if !seen[real] {
				p.Allowed = append(p.Allowed, real)
				seen[real] = true
			}
		}
	}
	return p
}

func realDir(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return ""
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return ""
	}
	return real
}

// Resolve canonicalizes a reader path and applies this snapshot's grant.
func (p WorkspacePaths) Resolve(path string) string {
	real := realDir(path)
	if real == "" {
		return ""
	}
	for _, root := range p.Allowed {
		if real == root || root == string(filepath.Separator) || strings.HasPrefix(real, root+string(filepath.Separator)) {
			return real
		}
	}
	return ""
}

func (p WorkspacePaths) Contains(path string) bool { return p.Resolve(path) != "" }
