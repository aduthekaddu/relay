package files

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const (
	usageTTL         = 10 * time.Minute // completed results are reused this long
	usageMaxCache    = 32
	usageMaxDuration = 3 * time.Minute // a scan gives up (complete=false) after this
	usageMaxEntries  = 5_000_000
	usageMaxChildren = 300 // largest children reported; the rest are summed
	usageOtherKey    = "…"
	usageConcurrent  = 2
)

// usageScanner computes recursive folder sizes in the background.
type usageScanner struct {
	mu    sync.Mutex
	scans map[string]*usageScan
	sem   chan struct{}
}

type usageScan struct {
	mu      sync.Mutex
	res     api.DiskUsage
	running bool
	at      time.Time // finished at
}

func newUsageScanner() *usageScanner {
	return &usageScanner{scans: map[string]*usageScan{}, sem: make(chan struct{}, usageConcurrent)}
}

func (u *usageScan) snapshot() api.DiskUsage {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := u.res
	out.Children = make(map[string]int64, len(u.res.Children))
	for k, v := range u.res.Children {
		out.Children[k] = v
	}
	out.Pending = u.running
	return out
}

// get returns the current result for dir, starting a scan when there is
// none or it is stale. refresh forces a new scan.
func (u *usageScanner) get(ctx context.Context, dir string, refresh bool) api.DiskUsage {
	u.mu.Lock()
	sc := u.scans[dir]
	start := false
	if sc == nil {
		sc = &usageScan{res: api.DiskUsage{Path: dir, Children: map[string]int64{}}}
		u.scans[dir] = sc
		start = true
		u.evictLocked()
	} else {
		sc.mu.Lock()
		if !sc.running && (refresh || time.Since(sc.at) > usageTTL) {
			start = true
		}
		sc.mu.Unlock()
	}
	if start {
		sc.mu.Lock()
		sc.running = true
		sc.mu.Unlock()
	}
	u.mu.Unlock()
	if start {
		go u.run(ctx, dir, sc)
	}
	return sc.snapshot()
}

func (u *usageScanner) evictLocked() {
	for len(u.scans) > usageMaxCache {
		var oldest string
		var at time.Time
		for k, v := range u.scans {
			v.mu.Lock()
			running, t := v.running, v.at
			v.mu.Unlock()
			if running {
				continue
			}
			if oldest == "" || t.Before(at) {
				oldest, at = k, t
			}
		}
		if oldest == "" {
			return
		}
		delete(u.scans, oldest)
	}
}

func (u *usageScanner) run(ctx context.Context, dir string, sc *usageScan) {
	select {
	case u.sem <- struct{}{}:
		defer func() { <-u.sem }()
	case <-ctx.Done():
		sc.mu.Lock()
		sc.running = false
		sc.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(ctx, usageMaxDuration)
	defer cancel()
	w := &usageWalker{ctx: ctx, seen: map[[2]uint64]bool{}, sc: sc}
	res := api.DiskUsage{Path: dir, Children: map[string]int64{}}
	complete := w.scanTop(dir, &res)
	res.Complete = complete && ctx.Err() == nil
	res.Children = capChildren(res.Children)
	sc.mu.Lock()
	sc.res = res
	sc.running = false
	sc.at = time.Now()
	sc.mu.Unlock()
}

// capChildren keeps the largest usageMaxChildren entries and folds the
// rest into usageOtherKey.
func capChildren(m map[string]int64) map[string]int64 {
	if len(m) <= usageMaxChildren {
		return m
	}
	type kv struct {
		k string
		v int64
	}
	all := make([]kv, 0, len(m))
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	out := make(map[string]int64, usageMaxChildren+1)
	for i, e := range all {
		if i < usageMaxChildren {
			out[e.k] = e.v
		} else {
			out[usageOtherKey] += e.v
		}
	}
	return out
}

type usageWalker struct {
	ctx     context.Context
	dev     uint64
	seen    map[[2]uint64]bool // hard-linked files counted once
	entries int
	bytes   int64 // running total for progress reports
	sc      *usageScan
	res     *api.DiskUsage
	last    time.Time
}

// scanTop walks dir's children, recording each child's total. It returns
// false when the walk was cut short (limits, unreadable folders are fine).
func (w *usageWalker) scanTop(dir string, res *api.DiskUsage) bool {
	w.res = res
	fi, err := os.Lstat(dir)
	if err != nil {
		return false
	}
	if dev, _, _, ok := fileID(fi); ok {
		w.dev = dev
	}
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	res.Dirs = 1
	ok := true
	for _, name := range names {
		size, cont := w.walk(filepath.Join(dir, name))
		res.Children[name] += size
		res.Size += size
		w.publish()
		if !cont {
			ok = false
			break
		}
	}
	return ok
}

// walk returns the apparent size of p and whether scanning may continue.
func (w *usageWalker) walk(p string) (int64, bool) {
	if w.ctx.Err() != nil || w.entries >= usageMaxEntries {
		return 0, false
	}
	w.entries++
	fi, err := os.Lstat(p)
	if err != nil {
		return 0, true
	}
	mode := fi.Mode()
	if mode&os.ModeSymlink != 0 || isSpecial(mode) {
		return 0, true
	}
	dev, ino, nlink, ok := fileID(fi)
	if ok && dev != w.dev {
		return 0, true // another filesystem (mount point)
	}
	if !mode.IsDir() {
		if ok && nlink > 1 {
			k := [2]uint64{dev, ino}
			if w.seen[k] {
				return 0, true
			}
			w.seen[k] = true
		}
		w.res.Files++
		w.bytes += fi.Size()
		if w.entries%4096 == 0 {
			w.publish()
		}
		return fi.Size(), true
	}
	w.res.Dirs++
	f, err := os.Open(p)
	if err != nil {
		return 0, true
	}
	defer f.Close()
	var total int64
	for {
		names, err := f.Readdirnames(512)
		for _, n := range names {
			sz, cont := w.walk(filepath.Join(p, n))
			total += sz
			if !cont {
				return total, false
			}
		}
		if err != nil {
			break
		}
	}
	return total, true
}

// publish copies partial totals to the shared result a few times a second.
func (w *usageWalker) publish() {
	if time.Since(w.last) < 200*time.Millisecond {
		return
	}
	w.last = time.Now()
	w.sc.mu.Lock()
	w.sc.res.Size, w.sc.res.Files, w.sc.res.Dirs = w.bytes, w.res.Files, w.res.Dirs
	for k, v := range w.res.Children {
		w.sc.res.Children[k] = v
	}
	w.sc.mu.Unlock()
}

func (s *Service) handleUsage(w http.ResponseWriter, r *http.Request) {
	real, err := s.res.Resolve(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	st, err := os.Stat(real)
	if err != nil {
		fail(w, err)
		return
	}
	if !st.IsDir() {
		httpx.OK(w, api.DiskUsage{Path: real, Size: st.Size(), Files: 1, Children: map[string]int64{}, Complete: true})
		return
	}
	httpx.OK(w, s.usage.get(s.ctx, real, httpx.QueryBool(r, "refresh")))
}
