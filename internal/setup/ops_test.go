package setup

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLogsCommand(t *testing.T) {
	tests := []struct {
		goos, service string
		n             int
		follow        bool
		want          string
		err           bool
	}{
		{"linux", "all", 200, false, "journalctl --user -n 200 -o short-iso -u relay-ptyd.service -u relay.service --no-pager", false},
		{"linux", "serve", 50, true, "journalctl --user -n 50 -o short-iso -u relay.service -f", false},
		{"linux", "ptyd", 10, false, "journalctl --user -n 10 -o short-iso -u relay-ptyd.service --no-pager", false},
		{"darwin", "all", 20, true, "tail -n 20 -F /L/relay-ptyd.log /L/relay.log", false},
		{"darwin", "serve", 20, false, "tail -n 20 /L/relay.log", false},
		{"linux", "caddy", 20, false, "", true},
		{"linux", "all", 0, false, "", true},
	}
	for _, tt := range tests {
		got, err := LogsCommand(tt.goos, "/L", tt.service, tt.n, tt.follow)
		if (err != nil) != tt.err {
			t.Errorf("%+v: err %v", tt, err)
			continue
		}
		if !tt.err && strings.Join(got, " ") != tt.want {
			t.Errorf("got  %q\nwant %q", strings.Join(got, " "), tt.want)
		}
	}
}

func TestPurgeTargets(t *testing.T) {
	p := tempPaths(t)
	got, err := PurgeTargets(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join([]string{p.ConfigDir, p.DataDir, p.CacheDir, p.RuntimeDir}, ",") {
		t.Errorf("targets %v", got)
	}

	// RELAY_HOME: one root; a config file elsewhere is added explicitly.
	root := filepath.Join(p.Home, ".relay-x")
	q := p
	q.ConfigFile = filepath.Join(p.Home, "elsewhere", "relay.toml")
	got, err = PurgeTargets(q, root)
	if err != nil || len(got) != 2 || got[0] != root || got[1] != q.ConfigFile {
		t.Errorf("relay home: %v %v", got, err)
	}

	// Safety net: never the home directory itself, / or outside home.
	for name, bad := range map[string]string{"home": p.Home, "root": "/", "outside": "/etc/relay"} {
		if _, err := PurgeTargets(p, bad); err == nil {
			t.Errorf("%s: %q accepted", name, bad)
		}
	}
	r := p
	r.DataDir = "/var/lib/relay"
	if _, err := PurgeTargets(r, ""); err == nil {
		t.Error("data dir outside home accepted")
	}
}
