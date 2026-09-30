// Package schedule runs scheduled agent tasks and commands (the "night
// shift"): cron expressions with per-schedule timezones, persisted in
// SQLite, executed as task terminals under ptyd so their output can be
// watched live, with run history and notifications.
package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/secret"
)

// Defaults and limits.
const (
	DefaultMaxRuntime = 2 * time.Hour
	MaxSchedules      = 200
	runsKept          = 50
	outputLines       = 40
	maxOutput         = 16 << 10
	maxPrompt         = 32 << 10
	maxArgs           = 64
	maxArg            = 4 << 10
)

// Pty is the subset of ptyclient.Client the runner needs (fakeable).
type Pty interface {
	Create(ctx context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error)
	Get(ctx context.Context, id string) (*api.TerminalSession, error)
	Kill(ctx context.Context, id, signal string) error
	Snapshot(ctx context.Context, id string, lines int) (*api.TerminalSnapshot, error)
}

// Service manages schedules and runs them.
type Service struct {
	d          *core.Deps
	pty        Pty
	now        func() time.Time
	poll       time.Duration
	maxRuntime time.Duration

	baseCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	wake    chan struct{}

	mu      sync.Mutex
	running map[string]string // schedule id -> run id
}

// Option customises the service (tests).
type Option func(*Service)

// WithPty replaces the pty client.
func WithPty(p Pty) Option { return func(s *Service) { s.pty = p } }

// WithClock replaces the clock.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithPoll sets how often a running task is polled for exit.
func WithPoll(d time.Duration) Option { return func(s *Service) { s.poll = d } }

// WithMaxRuntime overrides the per-run limit.
func WithMaxRuntime(d time.Duration) Option { return func(s *Service) { s.maxRuntime = d } }

// New migrates the schedule tables.
func New(d *core.Deps, opts ...Option) (*Service, error) {
	s := &Service{
		d:          d,
		now:        time.Now,
		poll:       time.Second,
		maxRuntime: DefaultMaxRuntime,
		wake:       make(chan struct{}, 1),
		running:    map[string]string{},
	}
	if d.Pty != nil {
		s.pty = d.Pty
	}
	for _, o := range opts {
		o(s)
	}
	s.baseCtx, s.cancel = context.WithCancel(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Store.Migrate(ctx, "schedule", []string{
		`CREATE TABLE schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			cron TEXT NOT NULL,
			timezone TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL,
			agent TEXT NOT NULL DEFAULT '',
			prompt TEXT NOT NULL DEFAULT '',
			command TEXT NOT NULL DEFAULT '[]',
			mode TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			notify INTEGER NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE schedule_runs (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			exit_code INTEGER,
			terminal_id TEXT NOT NULL DEFAULT '',
			output TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX schedule_runs_by_schedule ON schedule_runs(schedule_id, started_at DESC)`,
	}); err != nil {
		return nil, fmt.Errorf("schedule: migrate: %w", err)
	}
	return s, nil
}

// Close stops run watchers (the task terminals themselves keep running in
// ptyd and are picked up again on the next start).
func (s *Service) Close() error {
	s.cancel()
	s.wg.Wait()
	return nil
}

// Start resumes watching runs left "running" by a previous server process
// and, when schedules.enabled, fires schedules until ctx is done.
func (s *Service) Start(ctx context.Context) error {
	s.resumeRunning(ctx)
	if s.d.Cfg != nil && !s.d.Cfg.Schedules.Enabled {
		<-ctx.Done()
		return nil
	}
	last := s.now()
	for {
		next, err := s.nextWake(ctx, last)
		if err != nil {
			s.warn("schedule: compute next run", err)
			next = s.now().Add(time.Minute)
		}
		wait := time.Until(next)
		if wait < 0 {
			wait = 0
		}
		if wait > time.Hour {
			wait = time.Hour // re-evaluate periodically (clock changes, DST)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-s.wake:
			timer.Stop()
			continue // schedules changed; recompute from the same `last`
		case <-timer.C:
		}
		now := s.now()
		s.fireDue(ctx, last, now)
		last = now
	}
}

// nextWake returns the earliest activation after `after` across enabled
// schedules (or an hour from now when there are none).
func (s *Service) nextWake(ctx context.Context, after time.Time) (time.Time, error) {
	list, err := s.list(ctx, true)
	if err != nil {
		return time.Time{}, err
	}
	next := s.now().Add(time.Hour)
	for _, sc := range list {
		sch, err := ParseCron(sc.Cron, sc.Timezone)
		if err != nil {
			continue
		}
		if t := sch.Next(after); !t.IsZero() && t.Before(next) {
			next = t
		}
	}
	return next, nil
}

// fireDue starts every enabled schedule with an activation in (last, now].
// Activations missed while the server was down are not replayed.
func (s *Service) fireDue(ctx context.Context, last, now time.Time) {
	list, err := s.list(ctx, true)
	if err != nil {
		s.warn("schedule: list", err)
		return
	}
	for _, sc := range list {
		sch, err := ParseCron(sc.Cron, sc.Timezone)
		if err != nil {
			continue
		}
		if t := sch.Next(last); !t.IsZero() && !t.After(now) {
			if _, err := s.Run(ctx, sc.ID); err != nil {
				s.warn("schedule: run "+sc.ID, err)
			}
		}
	}
}

func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) warn(msg string, err error) {
	if s.d.Log != nil {
		s.d.Log.Warn(msg, "err", err)
	}
}

// ---------------------------------------------------------------------------
// Persistence

const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(tsLayout)
}

func parseTS(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

const schedCols = `id,name,cron,timezone,cwd,agent,prompt,command,mode,enabled,notify,created_at`

func scanSchedule(sc interface{ Scan(...any) error }) (api.Schedule, error) {
	var x api.Schedule
	var cmd, created string
	var enabled, notify int
	if err := sc.Scan(&x.ID, &x.Name, &x.Cron, &x.Timezone, &x.Cwd, &x.Agent, &x.Prompt, &cmd, &x.Mode, &enabled, &notify, &created); err != nil {
		return x, err
	}
	_ = json.Unmarshal([]byte(cmd), &x.Command)
	x.Enabled, x.Notify = enabled != 0, notify != 0
	x.CreatedAt = parseTS(created)
	return x, nil
}

func (s *Service) list(ctx context.Context, enabledOnly bool) ([]api.Schedule, error) {
	q := `SELECT ` + schedCols + ` FROM schedules`
	if enabledOnly {
		q += ` WHERE enabled=1`
	}
	rows, err := s.d.Store.DB.QueryContext(ctx, q+` ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("schedule: list: %w", err)
	}
	defer rows.Close()
	out := []api.Schedule{}
	for rows.Next() {
		x, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// List returns every schedule with NextRun and LastRun filled in.
func (s *Service) List(ctx context.Context) ([]api.Schedule, error) {
	list, err := s.list(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range list {
		s.decorate(ctx, &list[i])
	}
	return list, nil
}

// Get returns one schedule.
func (s *Service) Get(ctx context.Context, id string) (*api.Schedule, error) {
	x, err := scanSchedule(s.d.Store.DB.QueryRowContext(ctx, `SELECT `+schedCols+` FROM schedules WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("schedule not found")
	}
	if err != nil {
		return nil, fmt.Errorf("schedule: get: %w", err)
	}
	s.decorate(ctx, &x)
	return &x, nil
}

func (s *Service) decorate(ctx context.Context, x *api.Schedule) {
	x.NextRun = time.Time{}
	if x.Enabled && (s.d.Cfg == nil || s.d.Cfg.Schedules.Enabled) {
		if sch, err := ParseCron(x.Cron, x.Timezone); err == nil {
			x.NextRun = sch.Next(s.now())
		}
	}
	if runs, err := s.Runs(ctx, x.ID, 1); err == nil && len(runs) > 0 {
		x.LastRun = &runs[0]
	}
}

// patch is Partial<Schedule> for create/update.
type patch struct {
	Name     *string   `json:"name"`
	Cron     *string   `json:"cron"`
	Timezone *string   `json:"timezone"`
	Cwd      *string   `json:"cwd"`
	Agent    *string   `json:"agent"`
	Prompt   *string   `json:"prompt"`
	Command  *[]string `json:"command"`
	Mode     *string   `json:"mode"`
	Enabled  *bool     `json:"enabled"`
	Notify   *bool     `json:"notify"`
	// Read-only fields clients may echo back.
	ID        *string          `json:"id"`
	NextRun   *string          `json:"nextRun"`
	LastRun   *json.RawMessage `json:"lastRun"`
	CreatedAt *string          `json:"createdAt"`
}

func (p patch) apply(x *api.Schedule, home string) error {
	if p.Name != nil {
		x.Name = strings.TrimSpace(*p.Name)
	}
	if p.Cron != nil {
		x.Cron = strings.Join(strings.Fields(*p.Cron), " ")
	}
	if p.Timezone != nil {
		x.Timezone = strings.TrimSpace(*p.Timezone)
	}
	if p.Cwd != nil {
		x.Cwd = strings.TrimSpace(*p.Cwd)
	}
	if p.Agent != nil {
		x.Agent = strings.TrimSpace(*p.Agent)
	}
	if p.Prompt != nil {
		x.Prompt = *p.Prompt
	}
	if p.Command != nil {
		x.Command = *p.Command
	}
	if p.Mode != nil {
		x.Mode = strings.ToLower(strings.TrimSpace(*p.Mode))
	}
	if p.Enabled != nil {
		x.Enabled = *p.Enabled
	}
	if p.Notify != nil {
		x.Notify = *p.Notify
	}
	return validate(x, home)
}

func bad(fieldName, msg string) *httpx.Err {
	return &httpx.Err{Status: 400, Code: "bad_request", Message: msg, Field: fieldName}
}

func validate(x *api.Schedule, home string) error {
	if x.Name == "" || utf8.RuneCountInString(x.Name) > 120 {
		return bad("name", "a name of up to 120 characters is required")
	}
	if _, err := ParseCron(x.Cron, x.Timezone); err != nil {
		if strings.Contains(err.Error(), "timezone") {
			return bad("timezone", err.Error())
		}
		return bad("cron", err.Error())
	}
	cwd, err := resolveCwd(x.Cwd, home)
	if err != nil {
		return err
	}
	x.Cwd = cwd
	if x.Mode == "" {
		x.Mode = "headless"
	}
	if x.Mode != "headless" && x.Mode != "interactive" {
		return bad("mode", "mode must be headless or interactive")
	}
	hasAgent, hasCmd := x.Agent != "", len(x.Command) > 0
	switch {
	case hasAgent && hasCmd:
		return bad("command", "choose an agent prompt or a command, not both")
	case !hasAgent && !hasCmd:
		return bad("agent", "an agent with a prompt, or a command, is required")
	case hasAgent:
		if strings.TrimSpace(x.Prompt) == "" {
			return bad("prompt", "a prompt is required for agent runs")
		}
		if len(x.Prompt) > maxPrompt {
			return bad("prompt", "prompts are limited to 32 KiB")
		}
		if len(x.Agent) > 32 {
			return bad("agent", "unknown agent")
		}
	default:
		if x.Mode == "interactive" {
			return bad("mode", "interactive mode needs an agent")
		}
		if len(x.Command) > maxArgs || strings.TrimSpace(x.Command[0]) == "" {
			return bad("command", "command must be a non-empty argv list of up to 64 items")
		}
		for _, a := range x.Command {
			if len(a) > maxArg || strings.ContainsRune(a, 0) {
				return bad("command", "command arguments are limited to 4 KiB and must not contain NUL")
			}
		}
		x.Prompt = ""
	}
	return nil
}

// resolveCwd expands "~", requires an absolute path to an existing dir.
func resolveCwd(cwd, home string) (string, error) {
	if cwd == "" || cwd == "~" {
		cwd = home
	} else if strings.HasPrefix(cwd, "~/") {
		cwd = filepath.Join(home, cwd[2:])
	}
	if !filepath.IsAbs(cwd) {
		return "", bad("cwd", "working directory must be an absolute path")
	}
	cwd = filepath.Clean(cwd)
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return "", bad("cwd", "working directory does not exist")
	}
	return cwd, nil
}

func (s *Service) home() string {
	if s.d.Paths.Home != "" {
		return s.d.Paths.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

// Create stores a new schedule. enabled and notify default to true.
func (s *Service) Create(ctx context.Context, p patch) (*api.Schedule, error) {
	x := api.Schedule{Enabled: true, Notify: true}
	if err := p.apply(&x, s.home()); err != nil {
		return nil, err
	}
	var n int
	if err := s.d.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM schedules`).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxSchedules {
		return nil, httpx.Conflict("too many schedules")
	}
	id, err := secret.Token("sc_", 8)
	if err != nil {
		return nil, err
	}
	x.ID, x.CreatedAt = id, s.now().UTC()
	cmd, _ := json.Marshal(nonNil(x.Command))
	if _, err := s.d.Store.DB.ExecContext(ctx, `INSERT INTO schedules(`+schedCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		x.ID, x.Name, x.Cron, x.Timezone, x.Cwd, x.Agent, x.Prompt, string(cmd), x.Mode, b2i(x.Enabled), b2i(x.Notify), ts(x.CreatedAt)); err != nil {
		return nil, fmt.Errorf("schedule: create: %w", err)
	}
	s.poke()
	return s.Get(ctx, id)
}

// Update applies a partial update.
func (s *Service) Update(ctx context.Context, id string, p patch) (*api.Schedule, error) {
	x, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// Switching between agent and command: clearing one side is implied.
	if p.Command != nil && len(*p.Command) > 0 && p.Agent == nil {
		x.Agent, x.Prompt = "", ""
	}
	if p.Agent != nil && *p.Agent != "" && p.Command == nil {
		x.Command = nil
	}
	if err := p.apply(x, s.home()); err != nil {
		return nil, err
	}
	cmd, _ := json.Marshal(nonNil(x.Command))
	if _, err := s.d.Store.DB.ExecContext(ctx, `UPDATE schedules SET name=?,cron=?,timezone=?,cwd=?,agent=?,prompt=?,command=?,mode=?,enabled=?,notify=? WHERE id=?`,
		x.Name, x.Cron, x.Timezone, x.Cwd, x.Agent, x.Prompt, string(cmd), x.Mode, b2i(x.Enabled), b2i(x.Notify), id); err != nil {
		return nil, fmt.Errorf("schedule: update: %w", err)
	}
	s.poke()
	return s.Get(ctx, id)
}

// Delete removes a schedule and its run history. A run in progress keeps
// going in its terminal.
func (s *Service) Delete(ctx context.Context, id string) error {
	res, err := s.d.Store.DB.ExecContext(ctx, `DELETE FROM schedules WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("schedule: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("schedule not found")
	}
	_, _ = s.d.Store.DB.ExecContext(ctx, `DELETE FROM schedule_runs WHERE schedule_id=?`, id)
	s.poke()
	return nil
}

func nonNil(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

const runCols = `id,schedule_id,started_at,finished_at,status,exit_code,terminal_id,output`

func scanRun(sc interface{ Scan(...any) error }) (api.ScheduleRun, error) {
	var r api.ScheduleRun
	var started, finished string
	var code sql.NullInt64
	if err := sc.Scan(&r.ID, &r.ScheduleID, &started, &finished, &r.Status, &code, &r.TerminalID, &r.Output); err != nil {
		return r, err
	}
	r.StartedAt, r.FinishedAt = parseTS(started), parseTS(finished)
	if code.Valid {
		c := int(code.Int64)
		r.ExitCode = &c
	}
	return r, nil
}

// Runs returns the newest runs of a schedule.
func (s *Service) Runs(ctx context.Context, id string, limit int) ([]api.ScheduleRun, error) {
	rows, err := s.d.Store.DB.QueryContext(ctx, `SELECT `+runCols+` FROM schedule_runs WHERE schedule_id=? ORDER BY started_at DESC, rowid DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("schedule: runs: %w", err)
	}
	defer rows.Close()
	out := []api.ScheduleRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) saveRun(ctx context.Context, r api.ScheduleRun) error {
	var code any
	if r.ExitCode != nil {
		code = *r.ExitCode
	}
	_, err := s.d.Store.DB.ExecContext(ctx, `INSERT INTO schedule_runs(`+runCols+`) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET finished_at=excluded.finished_at, status=excluded.status, exit_code=excluded.exit_code, terminal_id=excluded.terminal_id, output=excluded.output`,
		r.ID, r.ScheduleID, ts(r.StartedAt), ts(r.FinishedAt), r.Status, code, r.TerminalID, r.Output)
	if err != nil {
		return fmt.Errorf("schedule: save run: %w", err)
	}
	_, err = s.d.Store.DB.ExecContext(ctx, `DELETE FROM schedule_runs WHERE schedule_id=? AND id IN (SELECT id FROM schedule_runs WHERE schedule_id=? ORDER BY started_at DESC, rowid DESC LIMIT -1 OFFSET ?)`, r.ScheduleID, r.ScheduleID, runsKept)
	return err
}
