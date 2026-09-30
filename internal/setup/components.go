package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aduthekaddu/relay/internal/toolbox"
)

// Component is an optional group of toolbox recipes offered by setup.
type Component struct {
	ID    string
	Label string
	Hint  string
	// Recipes are installed in order (dependencies are added by the
	// catalog). For "agents" the wizard lets the user pick which ones.
	Recipes []string
}

// Components lists the optional installs in wizard order.
var Components = []Component{
	{ID: "desktop", Label: "Remote desktop", Hint: "TigerVNC + Openbox for the browser desktop (sudo)", Recipes: []string{"desktop"}},
	{ID: "code", Label: "Browser IDE", Hint: "code-server, opened inside Relay", Recipes: []string{"code-server"}},
	{ID: "agents", Label: "Coding agents", Hint: "Claude Code, Codex and friends", Recipes: []string{"claude-code", "codex"}},
	{ID: "tools", Label: "CLI essentials", Hint: "ripgrep, fd, fzf, jq, gh, tmux", Recipes: []string{"ripgrep", "fd", "fzf", "jq", "gh", "tmux"}},
}

// ComponentIDs returns the valid --components values.
func ComponentIDs() []string {
	ids := make([]string, len(Components))
	for i, c := range Components {
		ids[i] = c.ID
	}
	return ids
}

// ParseComponents validates a comma-separated --components value.
// "none" and "" select nothing; "all" selects everything.
func ParseComponents(s string) ([]string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "", "none":
		return nil, nil
	case "all":
		return ComponentIDs(), nil
	}
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !slices.Contains(ComponentIDs(), f) {
			return nil, fmt.Errorf("unknown component %q (use %s, all or none)", f, strings.Join(ComponentIDs(), ","))
		}
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out, nil
}

// componentRecipes resolves chosen components (plus extra recipe ids
// such as individually picked agents) into catalog recipes, dependencies
// first, dropping recipes that do not support goos.
func componentRecipes(cat *toolbox.Catalog, goos string, ids []string, agents []string) ([]*toolbox.Recipe, []string, error) {
	var want []string
	for _, c := range Components {
		if !slices.Contains(ids, c.ID) {
			continue
		}
		if c.ID == "agents" && agents != nil {
			want = append(want, agents...)
			continue
		}
		want = append(want, c.Recipes...)
	}
	rs, err := cat.WithNeeds(want)
	if err != nil {
		return nil, nil, err
	}
	var out []*toolbox.Recipe
	var skipped []string
	for _, r := range rs {
		if r.Supports(goos) {
			out = append(out, r)
		} else {
			skipped = append(skipped, r.Name)
		}
	}
	return out, skipped, nil
}

// InstallResult is the outcome of one recipe run by setup.
type InstallResult struct {
	Recipe  *toolbox.Recipe
	Skipped bool // already installed
	Err     error
}

// Installer runs toolbox recipes in the user's terminal.
type Installer struct {
	Sys     System
	Catalog *toolbox.Catalog
	Prober  *toolbox.Prober
	TempDir string   // where scripts are written (0700 dir)
	Env     []string // extra env, e.g. RELAY_DRY_RUN=1 in tests
}

// Run installs each recipe that is not yet present, visibly.
func (in Installer) Run(ctx context.Context, rs []*toolbox.Recipe, progress func(r *toolbox.Recipe, i, n int)) []InstallResult {
	out := make([]InstallResult, 0, len(rs))
	for i, r := range rs {
		if in.Prober != nil && in.Prober.Probe(ctx, r).Installed {
			out = append(out, InstallResult{Recipe: r, Skipped: true})
			continue
		}
		if progress != nil {
			progress(r, i+1, len(rs))
		}
		out = append(out, InstallResult{Recipe: r, Err: in.runOne(ctx, r)})
		if ctx.Err() != nil {
			break
		}
	}
	return out
}

func (in Installer) runOne(ctx context.Context, r *toolbox.Recipe) error {
	dir, err := os.MkdirTemp(in.TempDir, "relay-setup-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	script := filepath.Join(dir, r.ID+".sh")
	if err := os.WriteFile(script, []byte(in.Catalog.Script(r)), 0o700); err != nil {
		return fmt.Errorf("write recipe: %w", err)
	}
	env := append([]string{"RELAY_TOOLBOX=1"}, in.Env...)
	if err := in.Sys.Interactive(ctx, env, "bash", script); err != nil {
		return fmt.Errorf("%s: %w", r.Name, err)
	}
	return nil
}
