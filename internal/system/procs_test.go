package system

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/httpx"
)

func TestParseProcPidStat(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    procStat
		wantErr bool
	}{
		{
			name: "parens in comm",
			in:   "4002 (my (weird) proc) R 4001 4001 4001 0 -1 0 0 0 0 0 300 100 0 0 20 0 4 0 6000 2000000 1024 0 0\n",
			want: procStat{pid: 4002, comm: "my (weird) proc", state: "R", ppid: 4001, ticks: 400, threads: 4, start: 6000, rss: 1024},
		},
		{name: "short", in: "1 (init) S 0 1", wantErr: true},
		{name: "garbage", in: "not a stat line", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProcPidStat([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestCmdlineAndPasswd(t *testing.T) {
	if got := cmdline([]byte("a\x00b c\x00\x00"), 100); got != "a b c" {
		t.Errorf("cmdline = %q", got)
	}
	if got := cmdline([]byte("abcdef"), 3); got != "abc…" {
		t.Errorf("truncated = %q", got)
	}
	users := parsePasswd([]byte("# comment\nroot:x:0:0::/:/bin/sh\nalice:x:1000:1000::/h:/bin/sh\nshadow:x:1000:1::/:/x\nbad\n"))
	if !reflect.DeepEqual(users, map[int]string{0: "root", 1000: "alice"}) {
		t.Errorf("passwd = %v", users)
	}
}

func fixtureLister(f fixture) *ProcLister {
	return &ProcLister{Proc: f.proc, Etc: f.etc, uid: os.Getuid(), self: 999999, parent: 999998, pageSize: 4096}
}

func TestProcListFixture(t *testing.T) {
	f := newFixture(t)
	l := fixtureLister(f)
	ctx := context.Background()
	// Prime, advance CPU ticks of 4002 by 50 over a known interval.
	l.remember(l.readAll(), time.Now().Add(-time.Second))
	writeProc(t, f.proc, 4002, "4002 (my (weird) proc) R 4001 4001 4001 0 -1 0 0 0 0 0 330 120 0 0 20 0 4 0 6000 2000000 1024 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n", "python3\x00-m\x00http.server\x00")

	ps := l.List(ctx, ListOptions{Sessions: map[int]string{4001: "t_abc"}})
	if len(ps) != 2 {
		t.Fatalf("got %d processes", len(ps))
	}
	top := ps[0]
	if top.PID != 4002 || top.Name != "my (weird) proc" || top.Cmd != "python3 -m http.server" || top.Threads != 4 {
		t.Fatalf("top = %+v", top)
	}
	if top.CPU < 40 || top.CPU > 55 { // 50 ticks over ~1 s = 50 %
		t.Errorf("cpu = %v", top.CPU)
	}
	if top.RSS != 1024*4096 || top.User != "dev" || top.Terminal != "t_abc" || top.Protected {
		t.Errorf("fields = %+v", top)
	}
	wantStart := time.Unix(1700000000, 0).Add(60 * time.Second).UTC()
	if !top.StartedAt.Equal(wantStart) {
		t.Errorf("start = %v want %v", top.StartedAt, wantStart)
	}
	if ps[1].PID != 4001 || ps[1].Terminal != "t_abc" {
		t.Errorf("session root = %+v", ps[1])
	}

	if got := l.List(ctx, ListOptions{Query: "http.server"}); len(got) != 1 || got[0].PID != 4002 {
		t.Errorf("query = %+v", got)
	}
	if got := l.List(ctx, ListOptions{Sort: "pid", Limit: 1}); len(got) != 1 || got[0].PID != 4001 {
		t.Errorf("limit/sort = %+v", got)
	}
}

func TestTerminalOf(t *testing.T) {
	ppid := map[int]int{1: 0, 10: 1, 11: 10, 12: 11, 13: 12, 20: 1, 21: 20, 30: 30}
	got := terminalOf(ppid, map[int]string{11: "a", 20: "b", 99: "gone"})
	want := map[int]string{11: "a", 12: "a", 13: "a", 20: "b", 21: "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := terminalOf(ppid, nil); len(got) != 0 {
		t.Fatalf("no sessions: %v", got)
	}
}

func TestParseSignal(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"TERM", "TERM", true}, {"sigkill", "KILL", true}, {"", "TERM", true}, {"9", "KILL", true},
		{"HUP", "HUP", true}, {"SEGV", "", false}, {"0", "", false}, {"; rm -rf", "", false},
	}
	for _, tt := range tests {
		_, name, err := parseSignal(tt.in)
		if (err == nil) != tt.ok || name != tt.want {
			t.Errorf("parseSignal(%q) = %q, %v", tt.in, name, err)
		}
	}
}

func statusOf(err error) int {
	var he *httpx.Err
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

func TestSignalProtection(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("needs /proc")
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Skip("cannot start sleep:", err)
	}
	pid := child.Process.Pid
	done := make(chan struct{})
	go func() { _ = child.Wait(); close(done) }()
	t.Cleanup(func() { _ = child.Process.Kill(); <-done })

	l := NewProcLister()
	tests := []struct {
		name   string
		pid    int
		status int
	}{
		{"init", 1, 403},
		{"kthreadd", 2, 403},
		{"self", os.Getpid(), 403},
		{"parent", os.Getppid(), 403},
		{"missing", 1 << 30, 404},
		{"invalid", 0, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := l.Signal(tt.pid, "TERM"); statusOf(err) != tt.status {
				t.Fatalf("Signal(%d) = %v, want status %d", tt.pid, err, tt.status)
			}
		})
	}

	// Another user's process is protected (simulated by changing our uid).
	other := NewProcLister()
	other.uid = os.Getuid() + 1
	if _, _, err := other.Signal(pid, "TERM"); statusOf(err) != 403 {
		t.Fatalf("foreign uid: %v", err)
	}
	// A bad signal name is rejected before anything is sent.
	if _, _, err := l.Signal(pid, "SEGV"); statusOf(err) != 400 {
		t.Fatalf("bad signal: %v", err)
	}
	// Our own child can be signalled.
	p, name, err := l.Signal(pid, "TERM")
	if err != nil || name != "TERM" || p.Name != "sleep" {
		t.Fatalf("Signal(child) = %+v %q %v", p, name, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit after SIGTERM")
	}
}

func TestProtectedRelayBinary(t *testing.T) {
	l := &ProcLister{uid: 1000, self: 50, parent: 49, exe: "/opt/relay/relay"}
	tests := []struct {
		name string
		rp   rawProc
		want bool
	}{
		{"ordinary", rawProc{st: procStat{pid: 100, ppid: 60}, uid: 1000, exe: "/usr/bin/vim"}, false},
		{"relay ptyd", rawProc{st: procStat{pid: 101, ppid: 1}, uid: 1000, exe: "/opt/relay/relay"}, true},
		{"kernel thread", rawProc{st: procStat{pid: 300, ppid: 2}, uid: 0}, true},
		{"other user", rawProc{st: procStat{pid: 400, ppid: 1}, uid: 0}, true},
		{"parent", rawProc{st: procStat{pid: 49, ppid: 1}, uid: 1000}, true},
	}
	for _, tt := range tests {
		if got := l.protected(tt.rp); got != tt.want {
			t.Errorf("%s: protected = %v", tt.name, got)
		}
	}
}
