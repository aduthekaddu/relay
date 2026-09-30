package files

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

const (
	indexMaxEntries = 50_000
	indexBudget     = 20 * time.Second
	homeIndexDepth  = 3
	wsIndexDepth    = 6
)

// nameIndex is a small in-memory list of file and folder names under the
// home folder and workspace roots, rebuilt in the background, so the
// command center can match file names in microseconds.
type nameIndex struct {
	mu      sync.RWMutex
	entries []indexEntry
	builtAt time.Time
}

type indexEntry struct {
	lower string // lowercased base name
	path  string
	dir   bool
}

type indexRoot struct {
	dir    string
	depth  int
	hidden bool // descend into dot-folders
}

func newNameIndex() *nameIndex { return &nameIndex{} }

// indexRoots lists the folders the command-center index covers.
func (s *Service) indexRoots(ctx context.Context) []indexRoot {
	roots := []indexRoot{{dir: s.res.Root(), depth: homeIndexDepth}}
	if s.res.Inside(s.d.Paths.Home) && s.d.Paths.Home != s.res.Root() {
		roots = append(roots, indexRoot{dir: s.d.Paths.Home, depth: homeIndexDepth})
	}
	add := func(p string) {
		if real, err := filepath.EvalSymlinks(p); err == nil && s.res.Inside(real) {
			roots = append(roots, indexRoot{dir: real, depth: wsIndexDepth})
		}
	}
	for _, r := range s.d.Cfg.Agents.WorkspaceRoots {
		add(r)
	}
	if s.d.Workspaces != nil {
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ws, err := s.d.Workspaces.List(lctx)
		cancel()
		if err == nil {
			for _, w := range ws {
				add(w.Path)
			}
		}
	}
	return roots
}

// rebuild walks roots (bounded by entries and time) and swaps the index.
func (ix *nameIndex) rebuild(ctx context.Context, roots []indexRoot) {
	ctx, cancel := context.WithTimeout(ctx, indexBudget)
	defer cancel()
	// visited records the remaining depth budget a folder was walked with,
	// so a workspace inside home is re-walked deeper than the home pass.
	visited := map[string]int{}
	out := make([]indexEntry, 0, 4096)
	type item struct {
		dir  string
		left int // levels still allowed below dir
	}
	for _, r := range roots {
		queue := []item{{r.dir, r.depth}}
		for len(queue) > 0 && len(out) < indexMaxEntries && ctx.Err() == nil {
			it := queue[0]
			queue = queue[1:]
			if prev, ok := visited[it.dir]; ok && prev >= it.left {
				continue
			}
			visited[it.dir] = it.left
			des, err := os.ReadDir(it.dir)
			if err != nil {
				continue
			}
			for _, de := range des {
				name := de.Name()
				if strings.HasPrefix(name, ".") && !r.hidden {
					continue
				}
				full := filepath.Join(it.dir, name)
				isDir := de.IsDir()
				out = append(out, indexEntry{lower: strings.ToLower(name), path: full, dir: isDir})
				if isDir && it.left > 1 && !skipDirs[name] && !systemDirs[full] {
					queue = append(queue, item{full, it.left - 1})
				}
				if len(out) >= indexMaxEntries {
					break
				}
			}
		}
	}
	// Dedupe entries reached through overlapping roots.
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	uniq := out[:0]
	for i, e := range out {
		if i == 0 || e.path != out[i-1].path {
			uniq = append(uniq, e)
		}
	}
	ix.mu.Lock()
	ix.entries = uniq
	ix.builtAt = time.Now()
	ix.mu.Unlock()
}

type scored struct {
	e     indexEntry
	score int
}

// search returns the best matches for q.
func (ix *nameIndex) search(q string, limit int) []scored {
	q = strings.ToLower(strings.TrimSpace(q))
	if len(q) < 2 || limit <= 0 {
		return nil
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var best []scored
	for _, e := range ix.entries {
		sc := fuzzyScore(e.lower, q)
		if sc == 0 {
			continue
		}
		if e.dir {
			sc += 2 // folders are more often what people jump to
		}
		// Shallower paths win ties.
		sc -= strings.Count(e.path, "/") / 3
		best = append(best, scored{e, sc})
	}
	sort.SliceStable(best, func(i, j int) bool {
		if best[i].score != best[j].score {
			return best[i].score > best[j].score
		}
		return len(best[i].e.path) < len(best[j].e.path)
	})
	if len(best) > limit {
		best = best[:limit]
	}
	return best
}

// SearchProvider returns the command-center provider for file names.
func (s *Service) SearchProvider() *FileSearchProvider { return &FileSearchProvider{s: s} }

// FileSearchProvider implements core.SearchProvider (scope "files").
type FileSearchProvider struct{ s *Service }

// Scope implements core.SearchProvider.
func (p *FileSearchProvider) Scope() string { return "files" }

// Search implements core.SearchProvider.
func (p *FileSearchProvider) Search(ctx context.Context, query string, limit int) []api.SearchResult {
	if limit <= 0 {
		limit = 8
	}
	hits := p.s.index.search(query, limit)
	out := make([]api.SearchResult, 0, len(hits))
	home := p.s.d.Paths.Home
	for _, h := range hits {
		if ctx.Err() != nil {
			break
		}
		kind, icon := "file", "file"
		if h.e.dir {
			kind, icon = "dir", "folder"
		} else if m := mimeByName(filepath.Base(h.e.path)); m != "" {
			icon = m
		}
		out = append(out, api.SearchResult{
			Scope:    "files",
			ID:       h.e.path,
			Title:    filepath.Base(h.e.path),
			Subtitle: tildePath(filepath.Dir(h.e.path), home),
			Icon:     icon,
			Link:     "/files?path=" + url.QueryEscape(h.e.path),
			Score:    float64(h.score) / 100,
			Meta:     map[string]string{"path": h.e.path, "type": kind},
		})
	}
	return out
}

func tildePath(p, home string) string {
	if home != "" && within(home, p) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
