// Package files is the file manager API: browse, preview, edit, upload
// targets, download (raw, zip), move/copy/rename, trash with restore,
// storage usage and search. Every client path is validated by a Resolver
// so nothing outside cfg.Files.Root is ever read or written.
//
// See docs/dev/FILES_SYSTEM.md for behaviour details.
package files

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// AuditTopic is the backend bus topic audit events are published on
// (payload api.AuditEntry) unless Service.Audit is set. It matches the
// intended core.BusAudit contract; see docs/dev/FILES_SYSTEM.md.
const AuditTopic = "audit"

// Service implements the files API.
type Service struct {
	d      *core.Deps
	log    *slog.Logger
	res    *Resolver
	trash  *Trash
	git    *gitCache
	thumbs *thumbnailer
	usage  *usageScanner
	jobs   *jobManager
	index  *nameIndex

	useTrash  bool
	rgPath    string
	searchSem chan struct{}

	ctx    context.Context // cancelled by Close; parent of background jobs
	cancel context.CancelFunc

	// Audit, when set, receives destructive actions instead of the default
	// publication on AuditTopic. Wiring may point it at core.BusAudit.
	Audit func(ctx context.Context, e api.AuditEntry)
}

// New constructs the service. It starts no goroutines.
func New(d *core.Deps) (*Service, error) {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	home := d.Paths.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = h
	}
	res, err := NewResolver(d.Cfg.Files.Root, home)
	if err != nil {
		return nil, err
	}
	cacheDir := d.Paths.CacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "relay-cache")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		d:         d,
		log:       log.With("feature", "files"),
		res:       res,
		trash:     NewTrash(TrashDir(home)),
		git:       newGitCache(),
		thumbs:    newThumbnailer(filepath.Join(cacheDir, "thumbs")),
		usage:     newUsageScanner(),
		index:     newNameIndex(),
		useTrash:  d.Cfg.Files.UseTrash,
		searchSem: make(chan struct{}, 2),
		ctx:       ctx,
		cancel:    cancel,
	}
	s.jobs = newJobManager(d.Bus)
	if p, err := exec.LookPath("rg"); err == nil {
		s.rgPath = p
	}
	return s, nil
}

// Root returns the resolved root folder.
func (s *Service) Root() string { return s.res.Root() }

// Routes registers every files endpoint.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/files/list", s.handleList)
	rt.Handle("GET /api/v1/files/stat", s.handleStat)
	rt.Handle("GET /api/v1/files/raw", s.handleRaw)
	rt.Handle("GET /api/v1/files/text", s.handleText)
	rt.Handle("PUT /api/v1/files/text", s.handleSaveText)
	rt.Handle("GET /api/v1/files/thumb", s.handleThumb)
	rt.Handle("POST /api/v1/files/mkdir", s.handleMkdir)
	rt.Handle("POST /api/v1/files/touch", s.handleTouch)
	rt.Handle("POST /api/v1/files/rename", s.handleRename)
	rt.Handle("POST /api/v1/files/move", s.handleMove)
	rt.Handle("POST /api/v1/files/copy", s.handleCopy)
	rt.Handle("POST /api/v1/files/delete", s.handleDelete)
	rt.Handle("GET /api/v1/files/trash", s.handleTrashList)
	rt.Handle("POST /api/v1/files/trash/restore", s.handleTrashRestore)
	rt.Handle("POST /api/v1/files/trash/empty", s.handleTrashEmpty)
	rt.Handle("GET /api/v1/files/zip", s.handleZip)
	rt.Handle("GET /api/v1/files/usage", s.handleUsage)
	rt.Handle("GET /api/v1/files/search", s.handleSearch)
	rt.Handle("GET /api/v1/files/jobs", s.handleJobs)
	rt.Handle("DELETE /api/v1/files/jobs/{id}", s.handleCancelJob)
	rt.Handle("POST /api/v1/open", s.handleOpen)
}

// Start runs background maintenance (search index refresh, thumbnail
// cache pruning) until ctx is done.
func (s *Service) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { // tie service-lifetime work to both contexts
		select {
		case <-ctx.Done():
		case <-s.ctx.Done():
		}
		cancel()
	}()
	// Let the server come up before walking disks.
	if !sleepCtx(ctx, 5*time.Second) {
		return ctx.Err()
	}
	s.thumbs.prune(30 * 24 * time.Hour)
	indexTick := time.NewTicker(5 * time.Minute)
	defer indexTick.Stop()
	pruneTick := time.NewTicker(24 * time.Hour)
	defer pruneTick.Stop()
	s.index.rebuild(ctx, s.indexRoots(ctx))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-indexTick.C:
			s.index.rebuild(ctx, s.indexRoots(ctx))
		case <-pruneTick.C:
			s.thumbs.prune(30 * 24 * time.Hour)
		}
	}
}

// Close cancels running jobs and scans.
func (s *Service) Close() error {
	s.cancel()
	s.jobs.wait(5 * time.Second)
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// audit records a destructive or security-relevant action.
func (s *Service) audit(r *http.Request, event, detail string) {
	e := api.AuditEntry{At: time.Now().UTC(), Event: event, Detail: detail}
	if p := server.PrincipalFrom(r.Context()); p != nil {
		e.Actor = p.User
		if p.Method != "cookie" {
			e.Device = p.Method
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		e.IP = host
	}
	if s.Audit != nil {
		s.Audit(r.Context(), e)
		return
	}
	if s.d.Bus != nil {
		s.d.Bus.Publish(AuditTopic, e)
	}
}

// fail writes err, mapping raw filesystem errors to API errors.
func fail(w http.ResponseWriter, err error) { httpx.Fail(w, mapFSError(err)) }
