package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const (
	jobKeep    = 10 * time.Minute // finished jobs stay listed this long
	maxJobs    = 4                // concurrent copy jobs
	scanBudget = 10 * time.Second // time spent counting before copying
)

type jobState struct {
	mu     sync.Mutex
	job    api.FileJob
	cancel context.CancelFunc
}

func (j *jobState) snapshot() api.FileJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.job
	out.From = append([]string(nil), j.job.From...)
	out.Result = append([]string(nil), j.job.Result...)
	return out
}

// jobManager runs background copy jobs and publishes EvFilesJob.
type jobManager struct {
	bus  *events.Bus
	mu   sync.Mutex
	jobs map[string]*jobState
	sem  chan struct{}
	wg   sync.WaitGroup
}

func newJobManager(bus *events.Bus) *jobManager {
	return &jobManager{bus: bus, jobs: map[string]*jobState{}, sem: make(chan struct{}, maxJobs)}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (m *jobManager) publish(j *jobState) {
	if m.bus != nil {
		m.bus.Publish(api.EvFilesJob, j.snapshot())
	}
}

// startCopy launches a copy job under parent and returns its first state.
func (m *jobManager) startCopy(parent context.Context, pairs []opPair, to string) api.FileJob {
	ctx, cancel := context.WithCancel(parent)
	j := &jobState{cancel: cancel, job: api.FileJob{
		ID: newID(), Op: "copy", State: "running", To: to, StartedAt: time.Now().UTC(),
	}}
	for _, p := range pairs {
		j.job.From = append(j.job.From, p.src)
	}
	m.mu.Lock()
	m.gcLocked()
	m.jobs[j.job.ID] = j
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.runCopy(ctx, j, pairs)
	}()
	return j.snapshot()
}

func (m *jobManager) runCopy(ctx context.Context, j *jobState, pairs []opPair) {
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		m.finish(j, ctx.Err())
		return
	}
	// Count first so progress has a denominator (bounded in time).
	scanCtx, stop := context.WithTimeout(ctx, scanBudget)
	var files, size int64
	for _, p := range pairs {
		countTree(scanCtx, p.src, &files, &size)
	}
	stop()
	j.mu.Lock()
	j.job.TotalFiles, j.job.TotalBytes = files, size
	j.mu.Unlock()
	m.publish(j)

	c := &copier{ctx: ctx}
	c.progress = func(c *copier) {
		j.mu.Lock()
		j.job.Files, j.job.Bytes, j.job.Skipped, j.job.Current = c.files, c.bytes, c.skipped, c.current
		j.mu.Unlock()
		m.publish(j)
	}
	var err error
	for _, p := range pairs {
		dst := p.dst
		if _, statErr := os.Lstat(dst); statErr == nil { // appeared since planning
			dst = freeName(dst)
		}
		if err = c.copyTree(p.src, dst); err != nil {
			_ = os.RemoveAll(dst)
			break
		}
		j.mu.Lock()
		j.job.Result = append(j.job.Result, dst)
		j.mu.Unlock()
	}
	j.mu.Lock()
	j.job.Files, j.job.Bytes, j.job.Skipped, j.job.Current = c.files, c.bytes, c.skipped, ""
	j.mu.Unlock()
	m.finish(j, err)
}

func (m *jobManager) finish(j *jobState, err error) {
	j.mu.Lock()
	j.job.EndedAt = time.Now().UTC()
	switch {
	case err == nil:
		j.job.State = "done"
	case errors.Is(err, context.Canceled):
		j.job.State = "canceled"
	default:
		j.job.State = "failed"
		j.job.Error = friendlyFSError(err)
	}
	j.mu.Unlock()
	m.publish(j)
}

func friendlyFSError(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrExist):
		return "destination already exists"
	case errors.Is(err, fs.ErrNotExist):
		return "a file disappeared during the copy"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Op + " " + filepath.Base(pe.Path) + ": " + pe.Err.Error()
	}
	return "copy failed"
}

// countTree adds the regular files and bytes under p (no symlink follow).
func countTree(ctx context.Context, p string, files, size *int64) {
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				*files++
				*size += fi.Size()
			}
		} else if d.Type()&fs.ModeSymlink != 0 {
			*files++
		}
		return nil
	})
}

func (m *jobManager) gcLocked() {
	for id, j := range m.jobs {
		s := j.snapshot()
		if s.State != "running" && time.Since(s.EndedAt) > jobKeep {
			delete(m.jobs, id)
		}
	}
}

func (m *jobManager) list() []api.FileJob {
	m.mu.Lock()
	m.gcLocked()
	out := make([]api.FileJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j.snapshot())
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, k int) bool { return out[i].StartedAt.After(out[k].StartedAt) })
	return out
}

func (m *jobManager) cancelJob(id string) bool {
	m.mu.Lock()
	j := m.jobs[id]
	m.mu.Unlock()
	if j == nil {
		return false
	}
	j.cancel()
	return true
}

// wait blocks until running jobs exit or timeout passes.
func (m *jobManager) wait(timeout time.Duration) {
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (s *Service) handleJobs(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, s.jobs.list())
}

func (s *Service) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if !s.jobs.cancelJob(r.PathValue("id")) {
		httpx.Fail(w, httpx.NotFound("no such job"))
		return
	}
	httpx.NoContent(w)
}
