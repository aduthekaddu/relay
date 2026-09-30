package system

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const (
	clkTck     = 100 // USER_HZ on every Linux architecture Relay supports
	cmdlineMax = 512
	procPrime  = 300 * time.Millisecond // CPU% window when no recent sample exists
	procFresh  = 5 * time.Second        // previous sample reused for deltas when younger
)

// ProcLister reads processes from /proc and computes CPU% between calls.
type ProcLister struct {
	Proc string
	Etc  string

	uid      int
	self     int
	parent   int
	exe      string
	pageSize uint64

	mu      sync.Mutex
	prev    map[int]procSample
	prevAt  time.Time
	users   map[int]string
	usersAt time.Time
}

type procSample struct {
	ticks, start uint64
}

// NewProcLister returns a lister for the current machine and user.
func NewProcLister() *ProcLister {
	l := &ProcLister{Proc: "/proc", Etc: "/etc", uid: os.Getuid(), self: os.Getpid(), parent: os.Getppid(), pageSize: uint64(os.Getpagesize())}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		l.exe = exe
	}
	return l
}

// rawProc is one process as read from /proc.
type rawProc struct {
	st  procStat
	uid int
	cmd string
	exe string
}

func (l *ProcLister) readProc(pid int) (rawProc, bool) {
	dir := filepath.Join(l.Proc, strconv.Itoa(pid))
	b, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return rawProc{}, false
	}
	st, err := parseProcPidStat(b)
	if err != nil {
		return rawProc{}, false
	}
	rp := rawProc{st: st, uid: -1}
	if fi, err := os.Stat(dir); err == nil {
		if s, ok := fi.Sys().(*syscall.Stat_t); ok {
			rp.uid = int(s.Uid)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		rp.cmd = cmdline(b, cmdlineMax)
	}
	if rp.uid == l.uid {
		if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
			rp.exe = strings.TrimSuffix(exe, " (deleted)")
		}
	}
	return rp, true
}

func (l *ProcLister) readAll() map[int]rawProc {
	des, err := os.ReadDir(l.Proc)
	if err != nil {
		return nil
	}
	out := make(map[int]rawProc, len(des))
	for _, de := range des {
		pid, err := strconv.Atoi(de.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if rp, ok := l.readProc(pid); ok {
			out[pid] = rp
		}
	}
	return out
}

// protected reports processes Relay refuses to signal.
func (l *ProcLister) protected(rp rawProc) bool {
	switch {
	case rp.st.pid <= 2, rp.st.ppid == 2: // init, kthreadd, kernel threads
		return true
	case rp.st.pid == l.self, rp.st.pid == l.parent:
		return true
	case rp.uid != l.uid:
		return true
	case l.exe != "" && rp.exe == l.exe: // relay serve / relay ptyd
		return true
	}
	return false
}

func (l *ProcLister) userName(uid int) string {
	if l.users == nil || time.Since(l.usersAt) > time.Minute {
		b, _ := os.ReadFile(filepath.Join(l.Etc, "passwd"))
		l.users = parsePasswd(b)
		l.usersAt = time.Now()
	}
	if n, ok := l.users[uid]; ok {
		return n
	}
	if uid < 0 {
		return "?"
	}
	return strconv.Itoa(uid)
}

// ListOptions filter and order a process list.
type ListOptions struct {
	Sort  string // cpu | mem | pid | name
	Limit int
	Query string
	// Sessions maps a terminal session's root pid → session id, used to
	// tag descendants with Terminal.
	Sessions map[int]string
}

// List returns processes with CPU% since the previous call (or over a
// short priming window).
func (l *ProcLister) List(ctx context.Context, o ListOptions) []api.Process {
	l.mu.Lock()
	stale := l.prev == nil || time.Since(l.prevAt) > procFresh
	l.mu.Unlock()
	if stale {
		l.remember(l.readAll(), time.Now())
		t := time.NewTimer(procPrime)
		select {
		case <-ctx.Done():
		case <-t.C:
		}
		t.Stop()
	}
	now := time.Now()
	procs := l.readAll()
	boot, memTotal := l.bootAndMem()

	l.mu.Lock()
	dt := now.Sub(l.prevAt).Seconds()
	prev := l.prev
	l.mu.Unlock()

	ppid := make(map[int]int, len(procs))
	for pid, rp := range procs {
		ppid[pid] = rp.st.ppid
	}
	term := terminalOf(ppid, o.Sessions)
	q := strings.ToLower(strings.TrimSpace(o.Query))
	out := make([]api.Process, 0, len(procs))
	l.mu.Lock()
	for pid, rp := range procs {
		p := api.Process{
			PID: pid, PPID: rp.st.ppid, Name: rp.st.comm, Cmd: rp.cmd,
			User: l.userName(rp.uid), State: rp.st.state, Threads: rp.st.threads,
			RSS: rp.st.rss * l.pageSize, Terminal: term[pid], Protected: l.protected(rp),
		}
		if p.Cmd == "" {
			p.Cmd = "[" + rp.st.comm + "]"
		}
		if boot > 0 {
			p.StartedAt = time.Unix(boot, 0).Add(time.Duration(rp.st.start) * time.Second / clkTck).UTC()
		}
		if memTotal > 0 {
			p.MemPct = round1(float64(p.RSS) / float64(memTotal) * 100)
		}
		if pr, ok := prev[pid]; ok && pr.start == rp.st.start && dt > 0 && rp.st.ticks >= pr.ticks {
			p.CPU = round1(float64(rp.st.ticks-pr.ticks) / clkTck / dt * 100)
		}
		if q == "" || matchProc(p, q) {
			out = append(out, p)
		}
	}
	l.mu.Unlock()
	l.remember(procs, now)
	sortProcs(out, o.Sort)
	if o.Limit > 0 && len(out) > o.Limit {
		out = out[:o.Limit]
	}
	return out
}

func (l *ProcLister) remember(procs map[int]rawProc, at time.Time) {
	m := make(map[int]procSample, len(procs))
	for pid, rp := range procs {
		m[pid] = procSample{ticks: rp.st.ticks, start: rp.st.start}
	}
	l.mu.Lock()
	l.prev, l.prevAt = m, at
	l.mu.Unlock()
}

func (l *ProcLister) bootAndMem() (int64, uint64) {
	b, _ := os.ReadFile(filepath.Join(l.Proc, "stat"))
	_, _, boot := parseProcStat(b)
	mb, _ := os.ReadFile(filepath.Join(l.Proc, "meminfo"))
	return boot, parseMeminfo(mb)["MemTotal"]
}

func matchProc(p api.Process, q string) bool {
	return strings.Contains(strings.ToLower(p.Name), q) ||
		strings.Contains(strings.ToLower(p.Cmd), q) ||
		strings.EqualFold(p.User, q) ||
		strconv.Itoa(p.PID) == q ||
		(p.Terminal != "" && strings.EqualFold(p.Terminal, q))
}

func sortProcs(ps []api.Process, key string) {
	sort.Slice(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		switch key {
		case "mem":
			if a.RSS != b.RSS {
				return a.RSS > b.RSS
			}
		case "pid":
			return a.PID < b.PID
		case "name":
			if !strings.EqualFold(a.Name, b.Name) {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
		default: // cpu
			if a.CPU != b.CPU {
				return a.CPU > b.CPU
			}
			if a.RSS != b.RSS {
				return a.RSS > b.RSS
			}
		}
		return a.PID < b.PID
	})
}

// terminalOf maps every pid that descends from (or is) a session root
// pid to that session id. Chains are memoised so the walk is O(n).
func terminalOf(ppid map[int]int, sessions map[int]string) map[int]string {
	out := make(map[int]string, len(sessions))
	if len(sessions) == 0 {
		return out
	}
	done := make(map[int]bool, len(ppid))
	for pid := range ppid {
		var chain []int
		id := ""
		for p, steps := pid, 0; p > 1 && steps < 4096; steps++ {
			if s, ok := sessions[p]; ok {
				id = s
				break
			}
			if done[p] {
				id = out[p]
				break
			}
			chain = append(chain, p)
			next, ok := ppid[p]
			if !ok || next == p {
				break
			}
			p = next
		}
		for _, c := range chain {
			done[c] = true
			if id != "" {
				out[c] = id
			}
		}
	}
	for pid, id := range sessions {
		if _, ok := ppid[pid]; ok {
			out[pid] = id
		}
	}
	return out
}

var signals = map[string]syscall.Signal{
	"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL, "INT": syscall.SIGINT, "HUP": syscall.SIGHUP,
	"STOP": syscall.SIGSTOP, "CONT": syscall.SIGCONT, "QUIT": syscall.SIGQUIT,
	"USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2,
}

// parseSignal accepts "TERM", "SIGTERM", "term" or a number from the table.
func parseSignal(s string) (syscall.Signal, string, error) {
	name := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "SIG")
	if name == "" {
		name = "TERM"
	}
	if n, err := strconv.Atoi(name); err == nil {
		for k, v := range signals {
			if int(v) == n {
				return v, k, nil
			}
		}
	}
	sig, ok := signals[name]
	if !ok {
		return 0, "", &httpx.Err{Status: 400, Code: "bad_request", Message: "unsupported signal", Field: "signal"}
	}
	return sig, name, nil
}

// Signal sends sig to pid unless the process is protected. It returns the
// process as it was before the signal (for the audit trail).
func (l *ProcLister) Signal(pid int, sigName string) (*api.Process, string, error) {
	sig, name, err := parseSignal(sigName)
	if err != nil {
		return nil, "", err
	}
	if pid <= 0 {
		return nil, "", httpx.BadRequest("invalid pid")
	}
	rp, ok := l.readProc(pid)
	if !ok {
		return nil, "", httpx.NotFound("no such process")
	}
	if l.protected(rp) {
		return nil, "", httpx.Forbidden("this process is protected")
	}
	p := &api.Process{PID: pid, PPID: rp.st.ppid, Name: rp.st.comm, Cmd: rp.cmd, User: l.userName(rp.uid)}
	if err := syscall.Kill(pid, sig); err != nil {
		switch {
		case errors.Is(err, syscall.ESRCH):
			return nil, "", httpx.NotFound("no such process")
		case errors.Is(err, syscall.EPERM):
			return nil, "", httpx.Forbidden("permission denied")
		}
		return nil, "", err
	}
	return p, name, nil
}

// psFallback lists processes with ps(1) on systems without /proc
// (macOS). CPU is ps's own estimate; start times are not reported.
func psFallback(ctx context.Context, self, parent, uid int, o ListOptions) []api.Process {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,uid=,user=,rss=,%cpu=,%mem=,stat=,comm=").Output()
	if err != nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(o.Query))
	var ps []api.Process
	ppid := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 9 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		pp, _ := strconv.Atoi(f[1])
		u, _ := strconv.Atoi(f[2])
		rss, _ := strconv.ParseUint(f[4], 10, 64)
		cpu, _ := strconv.ParseFloat(f[5], 64)
		mem, _ := strconv.ParseFloat(f[6], 64)
		cmd := strings.Join(f[8:], " ")
		p := api.Process{PID: pid, PPID: pp, User: f[3], RSS: rss * 1024, CPU: cpu, MemPct: mem,
			State: f[7], Name: filepath.Base(cmd), Cmd: cmd,
			Protected: pid <= 1 || pid == self || pid == parent || u != uid}
		ppid[pid] = pp
		if q == "" || matchProc(p, q) {
			ps = append(ps, p)
		}
	}
	term := terminalOf(ppid, o.Sessions)
	for i := range ps {
		ps[i].Terminal = term[ps[i].PID]
	}
	sortProcs(ps, o.Sort)
	if o.Limit > 0 && len(ps) > o.Limit {
		ps = ps[:o.Limit]
	}
	return ps
}
