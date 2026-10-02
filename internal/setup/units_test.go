package setup

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUnitFilesLinux(t *testing.T) {
	d := UnitData{Binary: "/home/ann/My Apps/relay", Env: []string{"RELAY_HOME=/home/ann/.relay", "PATH=/usr/bin:/bin", "WEIRD=50%$off"}}
	files, names, err := UnitFiles("linux", d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "relay-ptyd.service,relay.service" {
		t.Fatalf("names %v", names)
	}
	ptyd, serve := files["relay-ptyd.service"], files["relay.service"]
	for name, want := range map[string]string{
		"ptyd exec":   `ExecStart="/home/ann/My Apps/relay" ptyd`,
		"ptyd kill":   "KillMode=process",
		"env":         "Environment=RELAY_HOME=/home/ann/.relay",
		"escaped env": "Environment=WEIRD=50%%$$off",
	} {
		if !strings.Contains(ptyd, want) {
			t.Errorf("%s: missing %q in\n%s", name, want, ptyd)
		}
	}
	for _, want := range []string{`ExecStart="/home/ann/My Apps/relay" serve`, "After=relay-ptyd.service", "WantedBy=default.target", UnitMarker} {
		if !strings.Contains(serve, want) {
			t.Errorf("serve: missing %q", want)
		}
	}
	if strings.Contains(serve, "NoNewPrivileges=true") {
		t.Error("NoNewPrivileges would drop the bind capability")
	}
}

func TestUnitFilesDarwinIsValidXML(t *testing.T) {
	d := UnitData{Binary: "/Users/ann/bin/relay", Env: []string{"PATH=/usr/bin", "A=<b>&c"}, LogDir: "/Users/ann/Library/Logs/relay"}
	files, names, err := UnitFiles("darwin", d)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		dec := xml.NewDecoder(strings.NewReader(files[n]))
		dec.Strict = true
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s: invalid XML: %v", n, err)
			}
		}
		if !strings.Contains(files[n], "&lt;b&gt;&amp;c") {
			t.Errorf("%s: env not escaped", n)
		}
		if !strings.Contains(files[n], UnitMarker) {
			t.Errorf("%s: no marker", n)
		}
	}
}

func TestUnitFilesRejectBadInput(t *testing.T) {
	for name, d := range map[string]UnitData{
		"relative binary": {Binary: "relay"},
		"newline binary":  {Binary: "/bin/relay\nExecStartPre=/bin/evil"},
		"newline env":     {Binary: "/bin/relay", Env: []string{"A=1\nExecStartPre=/bin/evil"}},
		"env without =":   {Binary: "/bin/relay", Env: []string{"JUSTKEY"}},
	} {
		if _, _, err := UnitFiles("linux", d); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, _, err := UnitFiles("plan9", UnitData{Binary: "/bin/relay"}); err == nil {
		t.Error("unknown OS accepted")
	}
}

func TestUnitEnv(t *testing.T) {
	env := map[string]string{"RELAY_HOME": "/h/.relay", "PATH": "/a:relative:/b:/a:", "XDG_DATA_HOME": ""}
	got := UnitEnv(func(k string) string { return env[k] })
	want := []string{"RELAY_HOME=/h/.relay", "PATH=/a:/b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDefaultUnitsDir(t *testing.T) {
	none := func(string) string { return "" }
	if got := DefaultUnitsDir("linux", "/home/a", none); got != "/home/a/.config/systemd/user" {
		t.Error(got)
	}
	if got := DefaultUnitsDir("linux", "/home/a", func(k string) string { return map[string]string{"XDG_CONFIG_HOME": "/x"}[k] }); got != "/x/systemd/user" {
		t.Error(got)
	}
	if got := DefaultUnitsDir("darwin", "/Users/a", none); got != "/Users/a/Library/LaunchAgents" {
		t.Error(got)
	}
}

func TestManagerLinux(t *testing.T) {
	dir := t.TempDir()
	sys := newFakeSys("linux")
	sys.bins["systemctl"] = "/usr/bin/systemctl"
	sys.out["systemctl --user is-active relay.service"] = "active"
	sys.out["systemctl --user is-enabled relay.service"] = "enabled"
	m := Manager{Sys: sys, UnitsDir: dir}
	ctx := context.Background()
	if m.State(ctx, SvcServe) != "not installed" {
		t.Fatal("state before install")
	}
	if _, err := WriteUnits("linux", dir, UnitData{Binary: "/bin/relay"}); err != nil {
		t.Fatal(err)
	}
	if !m.Available(ctx) || !m.Installed(SvcServe) || !m.Installed(SvcPtyd) {
		t.Fatal("available/installed")
	}
	if err := m.EnableNow(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"systemctl --user daemon-reload", "systemctl --user enable --now relay-ptyd.service relay.service", "systemctl --user restart relay.service"} {
		if !sys.called(c) {
			t.Errorf("missing %q in %v", c, sys.calls)
		}
	}
	if sys.called("systemctl --user restart relay-ptyd") {
		t.Error("EnableNow must never restart ptyd")
	}
	if m.State(ctx, SvcServe) != "active" || !m.Enabled(ctx, SvcServe) {
		t.Error("state/enabled")
	}

	// A foreign unit with the same name is left alone.
	foreign := filepath.Join(dir, "relay-ptyd.service")
	if err := os.WriteFile(foreign, []byte("[Service]\nExecStart=/opt/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := m.Remove(ctx)
	if err == nil || !strings.Contains(err.Error(), "not written by relay setup") {
		t.Errorf("want foreign-unit error, got %v", err)
	}
	if len(removed) != 1 || filepath.Base(removed[0]) != "relay.service" {
		t.Errorf("removed %v", removed)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign unit deleted")
	}
	if !sys.called("systemctl --user disable --now relay.service") {
		t.Error("serve not disabled")
	}
}

func TestManagerDarwin(t *testing.T) {
	dir := t.TempDir()
	sys := newFakeSys("darwin")
	sys.uid = 501
	sys.bins["launchctl"] = "/bin/launchctl"
	sys.fail["launchctl print gui/501/dev.relay.ptyd"] = true
	if _, err := WriteUnits("darwin", dir, UnitData{Binary: "/bin/relay", LogDir: "/tmp/l"}); err != nil {
		t.Fatal(err)
	}
	m := Manager{Sys: sys, UnitsDir: dir}
	ctx := context.Background()
	if err := m.EnableNow(ctx); err != nil {
		t.Fatal(err)
	}
	if !sys.called("launchctl bootstrap gui/501 " + filepath.Join(dir, "dev.relay.ptyd.plist")) {
		t.Errorf("calls %v", sys.calls)
	}
	sys.out["launchctl print gui/501/dev.relay.serve"] = "dev.relay.serve = {\n\tstate = running\n}"
	if st := m.State(ctx, SvcServe); st != "active" {
		t.Errorf("state %q", st)
	}
	removed, err := m.Remove(ctx)
	if err != nil || len(removed) != 2 {
		t.Errorf("remove: %v %v", removed, err)
	}
}

func TestLinger(t *testing.T) {
	sys := newFakeSys("linux")
	sys.out["loginctl show-user tester --property=Linger"] = "Linger=yes"
	if on, err := Linger(context.Background(), sys); err != nil || !on {
		t.Fatalf("%v %v", on, err)
	}
	sys.out["loginctl show-user tester --property=Linger"] = "Linger=no"
	if on, _ := Linger(context.Background(), sys); on {
		t.Fatal("linger off")
	}
}

func TestManagedServeOwnershipEnvironment(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		for _, override := range []string{"", "0", "1"} {
			t.Run(goos+"/override="+override, func(t *testing.T) {
				d := UnitData{Binary: "/opt/relay", Env: []string{"PATH=/usr/bin", "RELAY_NO_PTYD=" + override}, LogDir: "/tmp/relay-logs"}
				before := append([]string(nil), d.Env...)
				files, _, err := UnitFiles(goos, d)
				if err != nil {
					t.Fatal(err)
				}
				serve, daemon := "relay.service", "relay-ptyd.service"
				want := "Environment=RELAY_NO_PTYD=1"
				if goos == "darwin" {
					serve, daemon = "dev.relay.serve.plist", "dev.relay.ptyd.plist"
					want = "<key>RELAY_NO_PTYD</key>\n    <string>1</string>"
				}
				if strings.Count(files[serve], "RELAY_NO_PTYD") != 1 || !strings.Contains(files[serve], want) {
					t.Fatalf("serve must contain exactly one forced ownership setting: %s", files[serve])
				}
				if strings.Contains(files[daemon], "RELAY_NO_PTYD") {
					t.Fatal("daemon inherited serve ownership setting")
				}
				if !reflect.DeepEqual(before, d.Env) {
					t.Fatal("render mutated caller environment")
				}
			})
		}
	}
}

func TestManagerDarwinPreservesLoadedDaemon(t *testing.T) {
	for _, failServe := range []bool{false, true} {
		t.Run(map[bool]string{false: "reload serve", true: "failed serve reload"}[failServe], func(t *testing.T) {
			dir := t.TempDir()
			sys := newFakeSys("darwin")
			sys.uid = 501
			serveBootstrap := "launchctl bootstrap gui/501 " + filepath.Join(dir, "dev.relay.serve.plist")
			sys.fail[serveBootstrap] = failServe
			m := Manager{Sys: sys, UnitsDir: dir}
			err := m.EnableNow(context.Background())
			if (err != nil) != failServe {
				t.Fatalf("EnableNow: %v", err)
			}
			want := []string{"launchctl print gui/501/dev.relay.ptyd", "launchctl kickstart gui/501/dev.relay.ptyd", "launchctl bootout gui/501/dev.relay.serve", serveBootstrap}
			if !reflect.DeepEqual(sys.calls, want) {
				t.Fatalf("calls %v, want %v", sys.calls, want)
			}
		})
	}
}

func TestManagerDarwinMissingDaemonFailureStopsStartup(t *testing.T) {
	dir := t.TempDir()
	sys := newFakeSys("darwin")
	sys.uid = 501
	sys.fail["launchctl print gui/501/dev.relay.ptyd"] = true
	bootstrap := "launchctl bootstrap gui/501 " + filepath.Join(dir, "dev.relay.ptyd.plist")
	sys.fail[bootstrap] = true
	err := (Manager{Sys: sys, UnitsDir: dir}).EnableNow(context.Background())
	if err == nil {
		t.Fatal("missing daemon startup accepted")
	}
	for _, call := range sys.calls {
		if strings.Contains(call, "dev.relay.serve") || strings.Contains(call, "bootout") {
			t.Fatalf("unexpected call after daemon failure: %s", call)
		}
	}
}

func TestOwnershipOverrideStillRejectsInvalidEnvironment(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		_, _, err := UnitFiles(goos, UnitData{Binary: "/opt/relay", Env: []string{"RELAY_NO_PTYD=0\nExecStart=/bin/false"}})
		if err == nil || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s invalid input: %v", goos, err)
		}
	}
}

func TestManagerDarwinLoadedDaemonStartFailureStopsStartup(t *testing.T) {
	sys := newFakeSys("darwin")
	sys.uid = 501
	sys.fail["launchctl kickstart gui/501/dev.relay.ptyd"] = true
	err := (Manager{Sys: sys, UnitsDir: t.TempDir()}).EnableNow(context.Background())
	if err == nil {
		t.Fatal("loaded daemon start failure accepted")
	}
	for _, call := range sys.calls {
		if strings.Contains(call, "dev.relay.serve") || strings.Contains(call, "bootout") || strings.Contains(call, "-k") {
			t.Fatalf("unexpected stop/reload after daemon failure: %s", call)
		}
	}
}
