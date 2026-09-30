package toolbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseRecipe(t *testing.T) {
	good := `#!/usr/bin/env bash
# id: demo
# name: Demo
# category: cli
# description: A demo tool.
# homepage: https://example.com
# check: demo | demo2, helper
# version: {bin} --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 1 MB
# tags: a, b
# needs: node
echo body
# not: header
`
	tests := []struct {
		name    string
		src     string
		wantErr string
		check   func(t *testing.T, r *Recipe)
	}{
		{name: "full header", src: good, check: func(t *testing.T, r *Recipe) {
			if r.ID != "demo" || r.Name != "Demo" || r.Category != "cli" || r.Size != "1 MB" {
				t.Fatalf("fields: %+v", r)
			}
			if len(r.Check) != 2 || strings.Join(r.Check[0], ",") != "demo,demo2" || r.Check[1][0] != "helper" {
				t.Fatalf("check: %q", r.Check)
			}
			if strings.Join(r.Version, " ") != "{bin} --version" {
				t.Fatalf("version: %q", r.Version)
			}
			if !r.NeedsSudo("linux") || r.NeedsSudo("darwin") {
				t.Fatalf("sudo: %q", r.SudoOn)
			}
			if !r.Supports("darwin") || r.Supports("windows") {
				t.Fatalf("platforms: %q", r.Platforms)
			}
			if strings.Join(r.Tags, "|") != "a|b" || r.Needs[0] != "node" {
				t.Fatalf("tags/needs: %q %q", r.Tags, r.Needs)
			}
		}},
		{name: "sudo true", src: strings.Replace(good, "requires-sudo: linux", "requires-sudo: true", 1), check: func(t *testing.T, r *Recipe) {
			if !r.NeedsSudo("linux") || !r.NeedsSudo("darwin") {
				t.Fatal("want sudo everywhere")
			}
		}},
		{name: "sudo false", src: strings.Replace(good, "requires-sudo: linux", "requires-sudo: false", 1), check: func(t *testing.T, r *Recipe) {
			if r.NeedsSudo("linux") {
				t.Fatal("want no sudo")
			}
		}},
		{name: "missing id", src: strings.Replace(good, "# id: demo\n", "", 1), wantErr: "invalid or missing id"},
		{name: "bad id", src: strings.Replace(good, "id: demo", "id: ../x", 1), wantErr: "invalid or missing id"},
		{name: "bad category", src: strings.Replace(good, "category: cli", "category: games", 1), wantErr: "invalid category"},
		{name: "unknown header", src: strings.Replace(good, "# size:", "# colour: red\n# size:", 1), wantErr: "unknown header"},
		{name: "duplicate header", src: strings.Replace(good, "# size: 1 MB", "# size: 1 MB\n# size: 2 MB", 1), wantErr: "duplicate header"},
		{name: "missing check", src: strings.Replace(good, "# check: demo | demo2, helper\n", "", 1), wantErr: "missing check"},
		{name: "bad platform", src: strings.Replace(good, "platforms: linux, darwin", "platforms: linux, beos", 1), wantErr: "unknown platform"},
		{name: "header ends at code", src: strings.Replace(good, "# name: Demo\n", "", 1) + "", wantErr: "missing name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ParseRecipe("demo.sh", tt.src)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, r)
		})
	}
}

func TestLoadCatalogValidation(t *testing.T) {
	rec := func(id string, extra string) string {
		return "#!/usr/bin/env bash\n# id: " + id + "\n# name: " + id + "\n# category: cli\n# description: d\n# check: " + id + "\n# platforms: linux\n" + extra + "echo\n"
	}
	tests := []struct {
		name    string
		files   fstest.MapFS
		wantErr string
	}{
		{"ok", fstest.MapFS{"r/_lib.sh": {Data: []byte("x")}, "r/a.sh": {Data: []byte(rec("a", ""))}}, ""},
		{"missing lib", fstest.MapFS{"r/a.sh": {Data: []byte(rec("a", ""))}}, "missing _lib.sh"},
		{"id mismatch", fstest.MapFS{"r/_lib.sh": {Data: []byte("x")}, "r/b.sh": {Data: []byte(rec("a", ""))}}, "does not match"},
		{"unknown need", fstest.MapFS{"r/_lib.sh": {Data: []byte("x")}, "r/a.sh": {Data: []byte(rec("a", "# needs: zz\n"))}}, "needs unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadCatalog(tt.files, "r")
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultCatalog(t *testing.T) {
	c, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"agents":   {"claude-code", "codex", "gemini-cli", "opencode", "kiro-cli", "cursor-agent", "amp", "copilot-cli", "aider", "qwen-code", "crush"},
		"runtimes": {"node", "python", "go", "rust", "bun"},
		"browsers": {"chrome"},
		"desktop":  {"desktop"},
		"creative": {"blender", "ffmpeg", "imagemagick"},
		"cli":      {"ripgrep", "fd", "fzf", "jq", "gh", "lazygit", "btop", "neovim", "tmux", "just", "zoxide", "bat", "eza"},
		"editors":  {"code-server"},
	}
	for cat, ids := range want {
		for _, id := range ids {
			r := c.Get(id)
			if r == nil {
				t.Errorf("missing recipe %s", id)
				continue
			}
			if r.Category != cat {
				t.Errorf("%s: category %s, want %s", id, r.Category, cat)
			}
			if r.Homepage == "" || !strings.HasPrefix(r.Homepage, "https://") {
				t.Errorf("%s: homepage %q", id, r.Homepage)
			}
			if r.Size == "" {
				t.Errorf("%s: missing size", id)
			}
		}
	}
	// Sorted by category order.
	last := -1
	for _, r := range c.All() {
		i := indexOf(Categories, r.Category)
		if i < last {
			t.Fatalf("catalog not sorted by category at %s", r.ID)
		}
		last = i
	}
	deps, err := c.WithNeeds([]string{"codex", "node", "gemini-cli"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range deps {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, ","); got != "node,codex,gemini-cli" {
		t.Fatalf("WithNeeds = %s", got)
	}
	if _, err := c.WithNeeds([]string{"nope"}); err == nil {
		t.Fatal("want error for unknown id")
	}
}

// Side effects in recipes must go through the helpers so that dry-run
// mode (and the terminal output) shows every command.
func TestRecipesUseHelpers(t *testing.T) {
	c, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	pkgArg := regexp.MustCompile(`^\s*[a-z]+=""\s*\\?$`)
	banned := regexp.MustCompile(`(?m)^[^#]*\b(curl|wget|sudo|apt-get|dnf|pacman|brew install)\b`)
	for _, r := range c.All() {
		body := r.Script
		// Strip quoted strings (messages may mention commands).
		body = regexp.MustCompile(`"[^"]*"|'[^']*'`).ReplaceAllString(body, `""`)
		for _, ln := range strings.Split(body, "\n") {
			if pkgArg.MatchString(ln) { // continuation line of a pkg call
				continue
			}
			if m := banned.FindString(ln); m != "" && !strings.Contains(ln, "pkg ") && !strings.Contains(ln, "as_root") && !strings.Contains(ln, "[ ") {
				t.Errorf("%s: direct side effect outside helpers: %q", r.ID, strings.TrimSpace(ln))
			}
		}
	}
}

// Every recipe runs to completion in dry-run mode on each supported
// platform and prints at least one command. Nothing is installed and no
// network is used.
func TestRecipesDryRun(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	c, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	type platform struct{ os, arch, pm string }
	platforms := []platform{{"linux", "amd64", "apt"}, {"linux", "arm64", "apt"}, {"linux", "amd64", "dnf"}, {"darwin", "arm64", "brew"}}
	home := t.TempDir()
	tmp := t.TempDir()
	for _, r := range c.All() {
		for _, p := range platforms {
			if !r.Supports(p.os) {
				continue
			}
			t.Run(r.ID+"/"+p.os+"-"+p.arch+"-"+p.pm, func(t *testing.T) {
				cmd := exec.Command("bash", "-c", c.Script(r))
				cmd.Env = []string{
					"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + tmp, "NO_COLOR=1",
					"RELAY_DRY_RUN=1", "RELAY_OS=" + p.os, "RELAY_ARCH=" + p.arch, "RELAY_PM=" + p.pm,
					"RELAY_BIN_DIR=" + filepath.Join(home, "bin"), "RELAY_TOOLS_DIR=" + filepath.Join(home, "tools"),
				}
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("dry run failed: %v\n%s", err, out)
				}
				if !strings.Contains(string(out), "+ ") {
					t.Fatalf("dry run printed no commands:\n%s", out)
				}
			})
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "bin")); len(entries) > 0 {
		t.Fatalf("dry run created files in bin dir: %v", entries)
	}
}
