package agents

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Indexer tuning. The machine may hold gigabytes of transcripts, so the
// first pass reads append-only files in bounded chunks and sleeps between
// them; later passes only touch files whose size, mtime or version moved.
const (
	indexChunk     = 8 << 20
	rescanEvery    = 20 * time.Second
	initialPause   = 25 * time.Millisecond // between chunks/sources on the first pass
	steadyPause    = 2 * time.Millisecond
	maxSourceBytes = 2 << 30 // sources larger than this are skipped
)

// indexer drives history readers into the index.
type indexer struct {
	s       *Service
	mu      sync.Mutex // one pass at a time
	running atomic.Bool
	first   atomic.Bool // first pass completed
	kick    chan bool   // true = full reindex
}

func newIndexer(s *Service) *indexer {
	return &indexer{s: s, kick: make(chan bool, 1)}
}

// trigger asks the loop for a pass (full drops and rebuilds the index).
// It never blocks; a pending request absorbs later ones.
func (x *indexer) trigger(full bool) bool {
	select {
	case x.kick <- full:
		return true
	default:
		return false
	}
}

// loop runs passes until ctx is done.
func (x *indexer) loop(ctx context.Context) error {
	// Let the server come up before the (possibly long) first pass.
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(2 * time.Second):
	}
	t := time.NewTicker(rescanEvery)
	defer t.Stop()
	full := false
	for {
		x.pass(ctx, full)
		full = false
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case full = <-x.kick:
		}
	}
}

// pass indexes every enabled adapter once.
func (x *indexer) pass(ctx context.Context, full bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.running.Store(true)
	defer x.running.Store(false)
	initial := !x.first.Load()
	for _, a := range x.s.adapters {
		if a.History == nil || ctx.Err() != nil {
			continue
		}
		if full {
			if err := x.s.ix.resetAgent(ctx, a.ID); err != nil {
				x.s.log().Warn("agents: reset index", "agent", a.ID, "err", err)
				continue
			}
		}
		n, changed, err := x.indexAgent(ctx, a, initial || full)
		if err != nil && !errors.Is(err, context.Canceled) {
			x.s.log().Warn("agents: index", "agent", a.ID, "err", err)
		}
		if changed > 0 || initial || full {
			x.s.publish(api.EvAgentsIndexed, map[string]any{"agent": a.ID, "sessions": n})
		}
	}
	if ctx.Err() == nil {
		x.first.Store(true)
	}
	x.s.onIndexed()
}

// indexAgent syncs one adapter's sources. It returns the number of
// visible sessions and how many sources changed.
func (x *indexer) indexAgent(ctx context.Context, a *Adapter, bulk bool) (int, int, error) {
	e := x.s.env()
	srcs, err := a.History.sources(ctx, e)
	if err != nil && len(srcs) == 0 {
		return 0, 0, err
	}
	known, kerr := x.s.ix.sources(ctx, a.ID)
	if kerr != nil {
		return 0, 0, kerr
	}
	pause := steadyPause
	if bulk {
		pause = initialPause
	}
	seen := make(map[string]bool, len(srcs))
	changed := 0
	for _, src := range srcs {
		seen[src.Key] = true
		k, ok := known[src.Key]
		if ok && !k.changed(src) {
			continue
		}
		if src.Size > maxSourceBytes {
			continue
		}
		changed++
		if err := x.indexSource(ctx, a, e, src, k, ok, !bulk, pause); err != nil {
			if ctx.Err() != nil {
				return 0, changed, ctx.Err()
			}
			x.s.log().Debug("agents: source skipped", "agent", a.ID, "err", err)
		}
		if !sleepCtx(ctx, pause) {
			return 0, changed, ctx.Err()
		}
	}
	// A reader error means the listing may be partial: keep what we know.
	if err == nil {
		for key, k := range known {
			if !seen[key] {
				if ferr := x.s.ix.forget(ctx, a.ID, key, k); ferr == nil {
					changed++
				}
			}
		}
	}
	var n int
	_ = x.s.ix.db.QueryRowContext(ctx, `SELECT count(*) FROM agent_sessions WHERE agent=? AND hidden=0`, a.ID).Scan(&n)
	return n, changed, err
}

// indexSource parses src (incrementally when it only grew) and stores the
// result chunk by chunk.
func (x *indexer) indexSource(ctx context.Context, a *Adapter, e *env, src source, k knownSource, known, announce bool, pause time.Duration) error {
	offset := int64(0)
	incremental := false
	if known && src.Append && src.Size >= k.Offset && k.Offset > 0 && src.Size >= k.Size {
		offset, incremental = k.Offset, true
	}
	ce := *e
	if src.Append {
		ce.chunk = indexChunk
	}
	for {
		p, err := a.History.parse(ctx, &ce, src, offset)
		if err != nil && p == nil {
			return err
		}
		if !src.Append {
			p.Consumed = 0
		}
		res, aerr := x.s.ix.apply(ctx, a.ID, src, k, p, incremental)
		if aerr != nil {
			return aerr
		}
		if announce {
			x.s.announce(ctx, res.Session)
		}
		k.Session = res.Session
		if err != nil || !src.Append || p.Consumed <= offset || p.Consumed >= src.Size {
			return err
		}
		offset, incremental = p.Consumed, true
		if !sleepCtx(ctx, pause) {
			return ctx.Err()
		}
	}
}

// sleepCtx sleeps d unless ctx ends first (false).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
