package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Process states (match api.App.State).
const (
	stateStopped  = "stopped"
	stateStarting = "starting"
	stateRunning  = "running"
	stateError    = "error"
)

// procSpec describes a supervised child process.
type procSpec struct {
	Name string
	// ResolveCommand rechecks a managed feature selection for each launch.
	ResolveCommand func() (argv, env []string, err error)
	Argv           []string
	Env            []string // full environment; nil inherits Relay's
	Dir            string
	// Ready returns nil once the child accepts connections.
	Ready        func(ctx context.Context) error
	ReadyTimeout time.Duration
	// StopGrace is how long SIGTERM gets before SIGKILL.
	StopGrace time.Duration
	// BeforeStart runs before each launch (e.g. remove a stale socket).
	BeforeStart func() error
}

// proc supervises one child: started on demand in its own process group,
// stopped with SIGTERM → SIGKILL on the whole group, never restarted
// automatically (the next request starts it again).
type proc struct {
	spec     procSpec
	onChange func()

	mu      sync.Mutex
	state   string
	since   time.Time
	lastErr string
	cmd     *exec.Cmd
	exited  chan struct{} // closed when the current child exits
	ready   chan struct{} // closed when the current start attempt resolves
	stopReq bool
	out     *tailBuffer
}

func newProc(spec procSpec, onChange func()) *proc {
	if spec.ReadyTimeout == 0 {
		spec.ReadyTimeout = 60 * time.Second
	}
	if spec.StopGrace == 0 {
		spec.StopGrace = 5 * time.Second
	}
	return &proc{spec: spec, onChange: onChange, state: stateStopped}
}

// Status returns state, since and the last error message.
func (p *proc) Status() (string, time.Time, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state, p.since, p.lastErr
}

// PID returns the child's pid (0 when not running).
func (p *proc) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *proc) setState(s, errMsg string) {
	p.state, p.since, p.lastErr = s, time.Now().UTC(), errMsg
}

func (p *proc) changed() {
	if p.onChange != nil {
		p.onChange()
	}
}

// StartAsync launches the child if it is not running and returns
// immediately. The returned channel closes when the attempt resolves
// (running or failed).
func (p *proc) StartAsync() <-chan struct{} {
	p.mu.Lock()
	if p.state == stateRunning || p.state == stateStarting {
		ch := p.ready
		p.mu.Unlock()
		return ch
	}
	p.ready = make(chan struct{})
	ready := p.ready
	p.setState(stateStarting, "")
	p.stopReq = false
	p.mu.Unlock()
	p.changed()
	go p.run(ready)
	return ready
}

// Start launches the child and waits until it is ready or fails.
func (p *proc) Start(ctx context.Context) error {
	ready := p.StartAsync()
	select {
	case <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	st, _, msg := p.Status()
	if st != stateRunning {
		if msg == "" {
			msg = "failed to start"
		}
		return errors.New(msg)
	}
	return nil
}

func (p *proc) run(ready chan struct{}) {
	resolve := sync.OnceFunc(func() { close(ready) })
	defer resolve()
	argv, env := p.spec.Argv, p.spec.Env
	if p.spec.ResolveCommand != nil {
		var err error
		argv, env, err = p.spec.ResolveCommand()
		if err != nil {
			p.fail(err.Error())
			return
		}
	}
	if p.spec.BeforeStart != nil {
		if err := p.spec.BeforeStart(); err != nil {
			p.fail(fmt.Sprintf("prepare %s: %v", p.spec.Name, err))
			return
		}
	}
	if len(argv) == 0 {
		p.fail("no command configured")
		return
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = p.spec.Dir
	cmd.SysProcAttr = childAttr()
	out := newTailBuffer(8 << 10)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 2 * time.Second // don't hang on grandchildren holding the pipes
	if devnull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devnull
		defer devnull.Close()
	}
	if err := cmd.Start(); err != nil {
		p.fail(fmt.Sprintf("start %s: %v", p.spec.Name, err))
		return
	}
	exited := make(chan struct{})
	p.mu.Lock()
	p.cmd, p.exited, p.out = cmd, exited, out
	stopNow := p.stopReq
	p.mu.Unlock()
	go p.wait(cmd, exited, out)
	if stopNow {
		p.Stop(context.Background())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.spec.ReadyTimeout)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if p.spec.Ready == nil || p.spec.Ready(ctx) == nil {
			p.mu.Lock()
			if p.cmd == cmd && p.state == stateStarting {
				p.setState(stateRunning, "")
				p.mu.Unlock()
				resolve()
				p.changed()
				return
			}
			p.mu.Unlock()
			return
		}
		select {
		case <-exited:
			return // wait() recorded the failure
		case <-ctx.Done():
			msg := fmt.Sprintf("%s did not become ready within %s", p.spec.Name, p.spec.ReadyTimeout)
			if t := out.Tail(); t != "" {
				msg += ": " + t
			}
			p.killGroup(cmd, syscall.SIGKILL)
			<-exited
			p.mu.Lock()
			p.setState(stateError, msg)
			p.mu.Unlock()
			p.changed()
			return
		case <-tick.C:
		}
	}
}

func (p *proc) fail(msg string) {
	p.mu.Lock()
	p.setState(stateError, msg)
	p.mu.Unlock()
	p.changed()
}

func (p *proc) wait(cmd *exec.Cmd, exited chan struct{}, out *tailBuffer) {
	err := cmd.Wait()
	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
		switch {
		case p.stopReq:
			p.setState(stateStopped, "")
		case p.state == stateStarting || err != nil:
			msg := fmt.Sprintf("%s exited", p.spec.Name)
			if err != nil {
				msg += ": " + err.Error()
			}
			if t := out.Tail(); t != "" {
				msg += " — " + t
			}
			p.setState(stateError, msg)
		default:
			p.setState(stateStopped, "")
		}
	}
	p.mu.Unlock()
	// Reap the rest of the group (helpers the child left behind).
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	close(exited)
	p.changed()
}

// Stop terminates the child's process group: SIGTERM, then SIGKILL after
// the grace period (or when ctx is done).
func (p *proc) Stop(ctx context.Context) error {
	p.mu.Lock()
	p.stopReq = true
	cmd, exited := p.cmd, p.exited
	if cmd == nil {
		if p.state != stateStarting {
			p.setState(stateStopped, "")
		}
		p.mu.Unlock()
		p.changed()
		return nil
	}
	p.mu.Unlock()
	p.killGroup(cmd, syscall.SIGTERM)
	t := time.NewTimer(p.spec.StopGrace)
	defer t.Stop()
	select {
	case <-exited:
		return nil
	case <-t.C:
	case <-ctx.Done():
	}
	p.killGroup(cmd, syscall.SIGKILL)
	select {
	case <-exited:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("%s did not exit", p.spec.Name)
	}
}

func (p *proc) killGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	// Negative pid: the whole process group (Setpgid made pgid = pid).
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}

// tailBuffer keeps the last n bytes written (child output for errors).
type tailBuffer struct {
	mu  sync.Mutex
	n   int
	buf []byte
}

func newTailBuffer(n int) *tailBuffer { return &tailBuffer{n: n} }

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.n {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.n:]...)
	}
	return len(b), nil
}

// Tail returns the last non-empty output line (≤ 300 bytes).
func (t *tailBuffer) Tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 300 {
				l = l[:300]
			}
			return l
		}
	}
	return ""
}
