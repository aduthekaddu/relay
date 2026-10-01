package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestRuntimeOwnsSnapshotsAndRollsBack(t *testing.T) {
	c := Defaults()
	c.Apps = []AppConfig{{Command: []string{"synthetic"}, Env: map[string]string{"KEY": "value"}}}
	c.Desktop.Apps = []DesktopAppConfig{{Command: []string{"synthetic"}}}
	r := NewRuntime(c, t.TempDir())
	original := r.Snapshot()
	c.Apps[0].Env["KEY"] = "changed"
	copy := r.Snapshot()
	copy.Apps[0].Command[0] = "changed"
	copy.Apps[0].Env["KEY"] = "changed"
	copy.Desktop.Apps[0].Command[0] = "changed"
	copy.Agents.Disabled = append(copy.Agents.Disabled, "changed")
	if !reflect.DeepEqual(original, r.Snapshot()) {
		t.Fatal("snapshot storage was shared")
	}
	err := r.Update(func(next *Config) error { next.Usage.ClaudeQuota = true; return errors.New("save failed") })
	if err == nil || !reflect.DeepEqual(original, r.Snapshot()) {
		t.Fatal("failed update published")
	}
}

func TestRuntimeSerializesPartialUpdates(t *testing.T) {
	r := NewRuntime(Defaults(), t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Update(func(c *Config) error { c.Terminal.RecordDays++; return nil }); err != nil {
				t.Error(err)
			}
			r.Snapshot()
		}()
	}
	wg.Wait()
	if got := r.Snapshot().Terminal.RecordDays; got != 94 {
		t.Fatalf("lost update: %d", got)
	}
}

func TestSaveUsesPrivateUniqueTempAndLeavesFailuresClean(t *testing.T) {
	dir := t.TempDir()
	p := Paths{ConfigFile: filepath.Join(dir, "relay.toml")}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, p.ConfigFile+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, Defaults()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "preserve" {
		t.Fatal("followed shared temporary filename")
	}
	st, err := os.Stat(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	for _, mode := range []string{"ancestor file", "target directory"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "relay.toml")
			if mode == "ancestor file" {
				if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(target, "child")
			} else {
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := Save(Paths{ConfigFile: target}, Defaults()); err == nil {
				t.Fatal("expected failure")
			}
			temps, err := filepath.Glob(filepath.Join(root, ".relay-settings-*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(temps) != 0 {
				t.Fatal("leaked temporary config")
			}
		})
	}
}

func TestLoadEnvironmentAndHomeExpansion(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Home: dir, ConfigFile: filepath.Join(dir, "relay.toml")}
	c := Defaults()
	c.Server.PublicURL = "https://saved.example.test"
	c.Terminal.DefaultCwd = "~/work"
	c.Agents.WorkspaceRoots = []string{"~/work"}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELAY_PUBLIC_URL", "https://effective.example.test")
	t.Setenv("RELAY_LISTEN", "127.0.0.1:47795")
	t.Setenv("RELAY_TLS", "OFF")
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin() != "https://effective.example.test" || got.Server.Listen != "127.0.0.1:47795" || got.Server.TLS != "off" || got.Terminal.DefaultCwd != filepath.Join(dir, "work") {
		t.Fatalf("load: %+v", got)
	}
	raw, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || reflect.DeepEqual(got, c) {
		t.Fatal("expected normalized environment view")
	}
}
