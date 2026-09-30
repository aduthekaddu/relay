package toolbox

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed recipes/*.sh
var recipeFS embed.FS

// Categories are the valid recipe categories, in display order.
var Categories = []string{"agents", "runtimes", "browsers", "desktop", "creative", "cli", "editors"}

// Recipe is one installable tool: metadata parsed from the header of
// recipes/<id>.sh plus the script body.
type Recipe struct {
	ID          string
	Name        string
	Category    string
	Description string
	Homepage    string
	// Check lists binaries that must all be present; each entry holds
	// alternatives (any one satisfies it). Parsed from
	// "check: a | b, c" → [[a b] [c]].
	Check [][]string
	// Version is the argv run to read the installed version. The
	// placeholder {bin} is replaced by the resolved first check binary.
	Version []string
	// SudoOn lists platforms where the install needs root.
	SudoOn    []string
	Platforms []string
	Size      string
	Tags      []string
	// Needs lists recipe ids that must be installed first.
	Needs []string
	// Script is the full recipe file.
	Script string
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ParseRecipe parses a recipe file. The header is the run of "# key: value"
// comment lines after the shebang; parsing stops at the first line that is
// not a comment.
func ParseRecipe(name, src string) (*Recipe, error) {
	r := &Recipe{Script: src}
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(src))
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first && strings.HasPrefix(line, "#!") {
			first = false
			continue
		}
		first = false
		if !strings.HasPrefix(line, "#") {
			break
		}
		key, val, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "#")), ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		if seen[key] {
			return nil, fmt.Errorf("%s: duplicate header %q", name, key)
		}
		seen[key] = true
		if err := r.set(key, val); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := r.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return r, nil
}

func (r *Recipe) set(key, val string) error {
	switch key {
	case "id":
		r.ID = val
	case "name":
		r.Name = val
	case "category":
		r.Category = val
	case "description":
		r.Description = val
	case "homepage":
		r.Homepage = val
	case "check":
		for _, group := range strings.Split(val, ",") {
			var alts []string
			for _, a := range strings.Split(group, "|") {
				if a = strings.TrimSpace(a); a != "" {
					alts = append(alts, a)
				}
			}
			if len(alts) > 0 {
				r.Check = append(r.Check, alts)
			}
		}
	case "version":
		r.Version = strings.Fields(val)
	case "requires-sudo":
		switch strings.ToLower(val) {
		case "true", "yes":
			r.SudoOn = []string{"linux", "darwin"}
		case "false", "no", "":
			r.SudoOn = nil
		default:
			r.SudoOn = splitList(val)
		}
	case "platforms":
		r.Platforms = splitList(val)
	case "size":
		r.Size = val
	case "tags":
		r.Tags = splitList(val)
	case "needs":
		r.Needs = splitList(val)
	default:
		return fmt.Errorf("unknown header %q", key)
	}
	return nil
}

func (r *Recipe) validate() error {
	switch {
	case !idPattern.MatchString(r.ID):
		return fmt.Errorf("invalid or missing id %q", r.ID)
	case r.Name == "":
		return fmt.Errorf("missing name")
	case !contains(Categories, r.Category):
		return fmt.Errorf("invalid category %q", r.Category)
	case r.Description == "":
		return fmt.Errorf("missing description")
	case len(r.Check) == 0:
		return fmt.Errorf("missing check")
	case len(r.Platforms) == 0:
		return fmt.Errorf("missing platforms")
	}
	for _, p := range append(append([]string{}, r.Platforms...), r.SudoOn...) {
		if p != "linux" && p != "darwin" {
			return fmt.Errorf("unknown platform %q", p)
		}
	}
	return nil
}

// Supports reports whether the recipe can run on goos.
func (r *Recipe) Supports(goos string) bool { return contains(r.Platforms, goos) }

// NeedsSudo reports whether installing on goos asks for root.
func (r *Recipe) NeedsSudo(goos string) bool { return contains(r.SudoOn, goos) }

// Catalog is the parsed, validated set of recipes.
type Catalog struct {
	recipes []*Recipe
	byID    map[string]*Recipe
	lib     string
}

var (
	defaultCatalog     *Catalog
	defaultCatalogErr  error
	defaultCatalogOnce sync.Once
)

// DefaultCatalog returns the embedded catalog (parsed once).
func DefaultCatalog() (*Catalog, error) {
	defaultCatalogOnce.Do(func() {
		defaultCatalog, defaultCatalogErr = LoadCatalog(recipeFS, "recipes")
	})
	return defaultCatalog, defaultCatalogErr
}

// LoadCatalog parses every <dir>/<id>.sh in fsys. <dir>/_lib.sh is the
// shared helper library prepended to scripts at install time.
func LoadCatalog(fsys fs.FS, dir string) (*Catalog, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read recipes: %w", err)
	}
	c := &Catalog{byID: map[string]*Recipe{}}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sh") {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if name == "_lib.sh" {
			c.lib = string(b)
			continue
		}
		if strings.HasPrefix(name, "_") {
			continue
		}
		r, err := ParseRecipe(name, string(b))
		if err != nil {
			return nil, err
		}
		if r.ID+".sh" != name {
			return nil, fmt.Errorf("%s: id %q does not match the file name", name, r.ID)
		}
		c.byID[r.ID] = r
		c.recipes = append(c.recipes, r)
	}
	if c.lib == "" {
		return nil, fmt.Errorf("recipes: missing _lib.sh")
	}
	for _, r := range c.recipes {
		for _, n := range r.Needs {
			if c.byID[n] == nil {
				return nil, fmt.Errorf("%s: needs unknown recipe %q", r.ID, n)
			}
		}
	}
	sort.SliceStable(c.recipes, func(i, j int) bool {
		ci, cj := indexOf(Categories, c.recipes[i].Category), indexOf(Categories, c.recipes[j].Category)
		if ci != cj {
			return ci < cj
		}
		return c.recipes[i].Name < c.recipes[j].Name
	})
	return c, nil
}

// All returns every recipe sorted by category then name.
func (c *Catalog) All() []*Recipe { return append([]*Recipe(nil), c.recipes...) }

// Get returns the recipe with id, or nil.
func (c *Catalog) Get(id string) *Recipe { return c.byID[id] }

// InCategory returns the recipes of one category.
func (c *Catalog) InCategory(cat string) []*Recipe {
	var out []*Recipe
	for _, r := range c.recipes {
		if r.Category == cat {
			out = append(out, r)
		}
	}
	return out
}

// Script returns the runnable script for r: the shared helper library,
// then the recipe body (its shebang and header stay as comments).
func (c *Catalog) Script(r *Recipe) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# Relay toolbox recipe: " + r.ID + "\n")
	b.WriteString(c.lib)
	b.WriteString("\n# ---- recipe ----\n")
	b.WriteString(r.Script)
	return b.String()
}

// WithNeeds expands ids with their dependencies, dependencies first,
// without duplicates. Unknown ids are an error.
func (c *Catalog) WithNeeds(ids []string) ([]*Recipe, error) {
	var out []*Recipe
	seen := map[string]bool{}
	var visit func(id string, depth int) error
	visit = func(id string, depth int) error {
		if seen[id] {
			return nil
		}
		r := c.byID[id]
		if r == nil {
			return fmt.Errorf("unknown tool %q", id)
		}
		if depth > 8 {
			return fmt.Errorf("dependency cycle at %q", id)
		}
		for _, n := range r.Needs {
			if err := visit(n, depth+1); err != nil {
				return err
			}
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, r)
		}
		return nil
	}
	for _, id := range ids {
		if err := visit(id, 0); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(list []string, s string) bool { return indexOf(list, s) >= 0 }

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
