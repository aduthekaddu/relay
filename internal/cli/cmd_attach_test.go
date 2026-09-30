package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestDetacher(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
		detach bool
	}{
		{"plain", []string{"ls\r"}, "ls\r", false},
		{"detach", []string{"ab\x1cd"}, "ab", true},
		{"detach split", []string{"ab\x1c", "d"}, "ab", true},
		{"capital D", []string{"\x1cD"}, "", true},
		{"literal prefix", []string{"\x1c\x1c"}, "\x1c", false},
		{"other key", []string{"\x1cx"}, "\x1cx", false},
		{"other key split", []string{"\x1c", "x", "y"}, "\x1cxy", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d detacher
			var out []byte
			detached := false
			for _, c := range tc.chunks {
				b, det := d.feed([]byte(c))
				out = append(out, b...)
				if det {
					detached = true
					break
				}
			}
			if string(out) != tc.want || detached != tc.detach {
				t.Fatalf("got %q detach=%v, want %q detach=%v", out, detached, tc.want, tc.detach)
			}
		})
	}
}

func TestPickSession(t *testing.T) {
	list := []api.TerminalSession{
		{ID: "t_aaaaaaaaaa", Name: "api", Activity: api.ActivityIdle},
		{ID: "t_aaaabbbbbb", Name: "api", Activity: api.ActivityExited},
		{ID: "t_cccccccccc", Name: "web", Activity: api.ActivityWorking},
		{ID: "t_dddddddddd", Name: "web", Activity: api.ActivityIdle},
		{ID: "t_eeeeeeeeee", Name: "old", Activity: api.ActivityExited},
	}
	cases := []struct {
		ref, want, err string
	}{
		{"t_cccccccccc", "t_cccccccccc", ""},
		{"api", "t_aaaaaaaaaa", ""}, // live beats exited
		{"old", "t_eeeeeeeeee", ""}, // single exited match
		{"web", "", "several"},
		{"t_cccc", "t_cccccccccc", ""},
		{"t_aaaa", "t_aaaaaaaaaa", ""}, // prefix: only one live
		{"t_", "", "no session"},       // prefix too short
		{"missing", "", "no session"},
	}
	for _, tc := range cases {
		got, err := pickSession(list, tc.ref)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("%s: err %v, want %q", tc.ref, err, tc.err)
			}
			continue
		}
		if err != nil || got.ID != tc.want {
			t.Fatalf("%s: got %v %v, want %s", tc.ref, got, err, tc.want)
		}
	}
}

func TestPrintSessions(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	code := 2
	list := []api.TerminalSession{
		{ID: "t_aaaaaaaaaa", Name: "build\x1b[31m", Cwd: "/home/u/code/app", Clients: 2, Activity: api.ActivityWorking, CreatedAt: now.Add(-90 * time.Second)},
		{ID: "t_bbbbbbbbbb", Name: "claude", Cwd: "/srv", CurrentCwd: "/home/u", Activity: api.ActivityIdle, Attention: &api.Attention{Reason: "osc"}, CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "t_cccccccccc", Name: "done", Cwd: "/tmp", Activity: api.ActivityExited, ExitCode: &code, CreatedAt: now.Add(-72 * time.Hour)},
	}
	var buf bytes.Buffer
	printSessions(&buf, list, "/home/u", now)
	out := buf.String()
	for _, want := range []string{"ID", "NAME", "STATUS", "CWD", "CLIENTS", "AGE", "~/code/app", "working", "1m", "waiting", "3h", "exited(2)", "3d"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Fatal("control characters leaked into the table")
	}
	buf.Reset()
	printSessions(&buf, nil, "", now)
	if !strings.Contains(buf.String(), "No sessions") {
		t.Fatal(buf.String())
	}
}
