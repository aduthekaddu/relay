package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/secret"
)

// Run starts one run of schedule id now (whether or not it is enabled)
// and returns the run record. If the previous run is still going, a
// "skipped" run is recorded instead. The run continues in the background
// after Run returns.
func (s *Service) Run(ctx context.Context, id string) (*api.ScheduleRun, error) {
	sc, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	runID, err := secret.Token("sr_", 8)
	if err != nil {
		return nil, err
	}
	run := api.ScheduleRun{ID: runID, ScheduleID: sc.ID, StartedAt: s.now().UTC(), Status: "running"}

	s.mu.Lock()
	if prev, busy := s.running[sc.ID]; busy {
		s.mu.Unlock()
		run.Status, run.FinishedAt = "skipped", run.StartedAt
		run.Output = "skipped: the previous run (" + prev + ") is still in progress"
		if err := s.saveRun(ctx, run); err != nil {
			return nil, err
		}
		s.publish(run)
		return &run, nil
	}
	s.running[sc.ID] = runID
	s.mu.Unlock()

	term, interactive, err := s.launch(ctx, sc)
	if err != nil {
		s.release(sc.ID)
		code := -1
		run.Status, run.FinishedAt, run.ExitCode = "failed", s.now().UTC(), &code
		run.Output = "could not start: " + err.Error()
		if serr := s.saveRun(ctx, run); serr != nil {
			return nil, serr
		}
		s.publish(run)
		s.notifyResult(ctx, sc, run)
		return &run, nil
	}
	run.TerminalID = term.ID
	if interactive {
		// Interactive agent sessions are for the user to pick up; the run
		// is complete once the session is up.
		s.release(sc.ID)
		run.Status, run.FinishedAt = "ok", s.now().UTC()
		run.Output = "interactive session started"
		if err := s.saveRun(ctx, run); err != nil {
			return nil, err
		}
		s.publish(run)
		s.notifyResult(ctx, sc, run)
		return &run, nil
	}
	if err := s.saveRun(ctx, run); err != nil {
		s.release(sc.ID)
		return nil, err
	}
	s.publish(run)
	s.watch(*sc, run)
	return &run, nil
}

func (s *Service) release(scheduleID string) {
	s.mu.Lock()
	delete(s.running, scheduleID)
	s.mu.Unlock()
}

// launch creates the terminal for sc. interactive reports an agent
// session the user is meant to pick up (not awaited).
func (s *Service) launch(ctx context.Context, sc *api.Schedule) (term *api.TerminalSession, interactive bool, err error) {
	if s.pty == nil {
		return nil, false, errors.New("the session daemon is not available")
	}
	if sc.Agent == "" {
		term, err = s.create(ctx, sc, sc.Command, api.KindTask, nil)
		return term, false, err
	}
	if s.d.Agents == nil {
		return nil, false, errors.New("agent support is not available")
	}
	if sc.Mode == "interactive" {
		argv, env, err := s.d.Agents.Command(ctx, sc.Agent, sc.Prompt, "")
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", sc.Agent, err)
		}
		term, err = s.create(ctx, sc, argv, api.KindAgent, env)
		return term, true, err
	}
	argv, err := s.d.Agents.HeadlessCommand(ctx, sc.Agent, sc.Prompt, "")
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", sc.Agent, err)
	}
	term, err = s.create(ctx, sc, argv, api.KindTask, nil)
	return term, false, err
}

func (s *Service) create(ctx context.Context, sc *api.Schedule, argv []string, kind api.TerminalKind, env map[string]string) (*api.TerminalSession, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	record := kind == api.KindAgent
	term, err := s.pty.Create(ctx, ptyclient.CreateSpec{
		CreateTerminalRequest: api.CreateTerminalRequest{
			Name:    "⏱ " + sc.Name,
			Command: argv,
			Cwd:     sc.Cwd,
			Kind:    kind,
			Agent:   sc.Agent,
			Record:  &record,
			Meta:    map[string]string{"schedule": sc.ID},
		},
		ExtraEnv: env,
	})
	if err != nil {
		return nil, fmt.Errorf("create terminal: %w", err)
	}
	return term, nil
}

// watch waits (in the background) for the run's terminal to exit.
func (s *Service) watch(sc api.Schedule, run api.ScheduleRun) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.release(sc.ID)
		s.await(s.baseCtx, sc, run)
	}()
}

// await polls the terminal until it exits or the run exceeds maxRuntime,
// then records the result.
func (s *Service) await(ctx context.Context, sc api.Schedule, run api.ScheduleRun) {
	deadline := run.StartedAt.Add(s.maxRuntime)
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	misses := 0
	for {
		term, err := s.pty.Get(ctx, run.TerminalID)
		switch {
		case ctx.Err() != nil:
			return // server shutting down: resumeRunning picks it up next start
		case errors.Is(err, ptyclient.ErrNotFound):
			s.finish(ctx, sc, run, nil, "failed", "the task terminal disappeared")
			return
		case err != nil:
			misses++
			if misses > 30 {
				s.finish(ctx, sc, run, nil, "failed", "lost contact with the session daemon: "+err.Error())
				return
			}
		case term.ExitCode != nil || term.Activity == api.ActivityExited:
			code := -1
			if term.ExitCode != nil {
				code = *term.ExitCode
			}
			status := "ok"
			if code != 0 {
				status = "failed"
			}
			s.finish(ctx, sc, run, &code, status, "")
			return
		default:
			misses = 0
			if !s.now().Before(deadline) {
				_ = s.pty.Kill(ctx, run.TerminalID, "TERM")
				s.finish(ctx, sc, run, nil, "failed", fmt.Sprintf("timed out after %s; the task was stopped", s.maxRuntime))
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// finish captures the output tail, stores the result, publishes it and
// notifies.
func (s *Service) finish(ctx context.Context, sc api.Schedule, run api.ScheduleRun, code *int, status, note string) {
	run.Status, run.ExitCode, run.FinishedAt = status, code, s.now().UTC()
	if snap, err := s.pty.Snapshot(ctx, run.TerminalID, outputLines); err == nil && snap != nil {
		run.Output = tail(snap.Text, maxOutput)
	}
	if note != "" {
		if run.Output != "" {
			run.Output += "\n"
		}
		run.Output += "[relay] " + note
	}
	if err := s.saveRun(context.WithoutCancel(ctx), run); err != nil {
		s.warn("schedule: save run", err)
	}
	s.publish(run)
	s.notifyResult(context.WithoutCancel(ctx), &sc, run)
}

func (s *Service) publish(run api.ScheduleRun) {
	if s.d.Bus != nil {
		s.d.Bus.Publish(api.EvScheduleRun, run)
	}
}

func (s *Service) notifyResult(ctx context.Context, sc *api.Schedule, run api.ScheduleRun) {
	if !sc.Notify || s.d.Notifier == nil || run.Status == "skipped" {
		return
	}
	req := api.NotifyRequest{Kind: "schedule", Agent: sc.Agent, Body: lastLines(run.Output, 3)}
	switch run.Status {
	case "ok":
		req.Title, req.Severity = sc.Name+" finished", "success"
	default:
		req.Title, req.Severity = sc.Name+" failed", "danger"
		if run.ExitCode != nil && *run.ExitCode > 0 {
			req.Title += fmt.Sprintf(" (exit %d)", *run.ExitCode)
		}
	}
	if run.TerminalID != "" {
		req.SessionID = run.TerminalID
	}
	if _, err := s.d.Notifier.Notify(ctx, req); err != nil {
		s.warn("schedule: notify", err)
	}
}

// resumeRunning re-attaches watchers to runs a previous server process
// left in "running" (their terminals live on in ptyd).
func (s *Service) resumeRunning(ctx context.Context) {
	rows, err := s.d.Store.DB.QueryContext(ctx, `SELECT `+runCols+` FROM schedule_runs WHERE status='running'`)
	if err != nil {
		s.warn("schedule: resume", err)
		return
	}
	var runs []api.ScheduleRun
	for rows.Next() {
		if r, err := scanRun(rows); err == nil {
			runs = append(runs, r)
		}
	}
	rows.Close()
	for _, r := range runs {
		sc, err := s.Get(ctx, r.ScheduleID)
		if err != nil {
			continue
		}
		if s.pty == nil || r.TerminalID == "" {
			s.finish(ctx, *sc, r, nil, "failed", "interrupted by a server restart")
			continue
		}
		s.mu.Lock()
		if _, busy := s.running[sc.ID]; busy {
			s.mu.Unlock()
			continue
		}
		s.running[sc.ID] = r.ID
		s.mu.Unlock()
		s.watch(*sc, r)
	}
}

// tail keeps the last n bytes of s, cut at a line boundary when possible.
func tail(s string, n int) string {
	s = strings.TrimRight(s, "\n ")
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return strings.ToValidUTF8(s, "")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n "), "\n")
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			out = append([]string{l}, out...)
		}
	}
	return strings.Join(out, "\n")
}
