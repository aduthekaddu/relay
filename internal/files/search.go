package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const (
	searchMaxHits     = 500
	searchNameBudget  = 3 * time.Second
	searchTextBudget  = 10 * time.Second
	searchMaxQuery    = 200
	searchPreviewMax  = 300
	searchScanFileMax = 1 << 20 // fallback content search reads files up to this size
)

// skipDirs are never descended into by walkers (they are huge and rarely
// what anyone searches for).
var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "vendor": true, ".cache": true,
	"__pycache__": true, ".venv": true, ".tox": true, ".next": true, ".turbo": true,
	".npm": true, ".pnpm-store": true, ".cargo": true, ".rustup": true,
}

// systemDirs are virtual filesystems skipped when the root is "/".
var systemDirs = map[string]bool{"/proc": true, "/sys": true, "/dev": true, "/run": true}

// nameScore rates how well name matches q (lowercased). 0 = no match.
func nameScore(name, q string) int {
	n := strings.ToLower(name)
	switch {
	case n == q:
		return 100
	case strings.HasPrefix(n, q):
		return 80
	}
	if strings.ContainsAny(q, "*?[") {
		if ok, _ := path.Match(q, n); ok {
			return 70
		}
		return 0
	}
	if i := strings.Index(n, q); i >= 0 {
		s := 60 - i
		if s < 30 {
			s = 30
		}
		return s
	}
	return 0
}

// fuzzyScore allows subsequence matches ("flsys" → "file_system.go") at a
// low score, for the command center index only.
func fuzzyScore(name, q string) int {
	if s := nameScore(name, q); s > 0 {
		return s
	}
	if len(q) < 3 {
		return 0
	}
	n := strings.ToLower(name)
	j := 0
	for i := 0; i < len(n) && j < len(q); i++ {
		if n[i] == q[j] {
			j++
		}
	}
	if j == len(q) {
		return 10
	}
	return 0
}

// SearchNames walks dir breadth-first (shallow results first) and calls
// emit for each name match until limit, the deadline, or emit returns
// false.
func (s *Service) SearchNames(ctx context.Context, dir, query string, limit int, emit func(api.FileSearchHit) bool) {
	q := strings.ToLower(query)
	queue := []string{dir}
	hits := 0
	for len(queue) > 0 {
		if ctx.Err() != nil {
			return
		}
		d := queue[0]
		queue = queue[1:]
		des, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, de := range des {
			name := de.Name()
			full := filepath.Join(d, name)
			if sc := nameScore(name, q); sc > 0 {
				if !emit(api.FileSearchHit{Path: full, Score: sc}) {
					return
				}
				if hits++; hits >= limit {
					return
				}
			}
			if de.IsDir() && !skipDirs[name] && !systemDirs[full] {
				queue = append(queue, full)
			}
		}
	}
}

// rgMessage is the subset of `rg --json` output we read.
type rgMessage struct {
	Type string `json:"type"`
	Data struct {
		Path       rgText `json:"path"`
		Lines      rgText `json:"lines"`
		LineNumber int    `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
		} `json:"submatches"`
	} `json:"data"`
}

type rgText struct {
	Text string `json:"text"`
}

// clipPreview trims a matching line for display.
func clipPreview(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if len(line) > searchPreviewMax {
		line = line[:searchPreviewMax]
		for !utf8.ValidString(line) && len(line) > 0 {
			line = line[:len(line)-1]
		}
	}
	return line
}

// parseRgLine converts one rg --json line into a hit (ok=false to skip).
func parseRgLine(b []byte) (api.FileSearchHit, bool) {
	var m rgMessage
	if err := json.Unmarshal(b, &m); err != nil || m.Type != "match" || m.Data.Path.Text == "" {
		return api.FileSearchHit{}, false
	}
	h := api.FileSearchHit{Path: m.Data.Path.Text, Line: m.Data.LineNumber, Preview: clipPreview(m.Data.Lines.Text)}
	if len(m.Data.Submatches) > 0 {
		start := m.Data.Submatches[0].Start
		if start <= len(m.Data.Lines.Text) {
			h.Column = utf8.RuneCountInString(m.Data.Lines.Text[:start]) + 1
		}
	}
	return h, true
}

// SearchContent finds lines containing query under dir, with ripgrep when
// installed (argv only, fixed strings, smart case) or a Go fallback.
func (s *Service) SearchContent(ctx context.Context, dir, query string, limit int, emit func(api.FileSearchHit) bool) error {
	if s.rgPath == "" {
		s.searchContentFallback(ctx, dir, query, limit, emit)
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{"--json", "--fixed-strings", "--smart-case", "--max-count", "20",
		"--max-filesize", "5M", "--max-columns", "1000", "--max-columns-preview",
		"--glob", "!node_modules", "--glob", "!.git", "--", query, dir}
	cmd := exec.CommandContext(ctx, s.rgPath, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "RIPGREP_CONFIG_PATH="}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	hits := 0
	for sc.Scan() {
		h, ok := parseRgLine(sc.Bytes())
		if !ok || !s.res.Inside(filepath.Clean(h.Path)) {
			continue
		}
		if !emit(h) {
			break
		}
		if hits++; hits >= limit {
			break
		}
	}
	cancel()
	_, _ = io.Copy(io.Discard, out)
	_ = cmd.Wait() // exit 1 = no matches; killed on cancel
	return nil
}

func (s *Service) searchContentFallback(ctx context.Context, dir, query string, limit int, emit func(api.FileSearchHit) bool) {
	fold := strings.ToLower(query) == query // smart case
	needle := []byte(query)
	hits := 0
	queue := []string{dir}
	buf := make([]byte, searchScanFileMax)
	for len(queue) > 0 && ctx.Err() == nil {
		d := queue[0]
		queue = queue[1:]
		des, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, de := range des {
			full := filepath.Join(d, de.Name())
			if de.IsDir() {
				if !skipDirs[de.Name()] && !systemDirs[full] {
					queue = append(queue, full)
				}
				continue
			}
			if !de.Type().IsRegular() || ctx.Err() != nil {
				continue
			}
			n := readSmall(full, buf)
			if n <= 0 || bytes.IndexByte(buf[:min(n, 8192)], 0) >= 0 {
				continue
			}
			for i, line := range bytes.Split(buf[:n], []byte{'\n'}) {
				hay := line
				if fold {
					hay = bytes.ToLower(line)
				}
				col := bytes.Index(hay, needle)
				if col < 0 {
					continue
				}
				h := api.FileSearchHit{Path: full, Line: i + 1, Column: utf8.RuneCount(line[:col]) + 1, Preview: clipPreview(string(line))}
				if !emit(h) {
					return
				}
				if hits++; hits >= limit {
					return
				}
			}
		}
	}
}

// readSmall reads a whole regular file into buf when it fits.
func readSmall(p string, buf []byte) int {
	f, err := os.OpenFile(p, os.O_RDONLY|oNoFollow|oNonBlock, 0)
	if err != nil {
		return -1
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > int64(len(buf)) {
		return -1
	}
	n, err := io.ReadFull(f, buf[:st.Size()])
	if err != nil && n == 0 {
		return -1
	}
	return n
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if query == "" || len(query) > searchMaxQuery {
		httpx.Fail(w, &httpx.Err{Status: 400, Code: "bad_request", Message: "query must be 1–200 characters", Field: "q"})
		return
	}
	dir, err := s.res.Resolve(q.Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		httpx.Fail(w, httpx.BadRequest("search path is not a folder"))
		return
	}
	select {
	case s.searchSem <- struct{}{}:
		defer func() { <-s.searchSem }()
	default:
		httpx.Fail(w, &httpx.Err{Status: 429, Code: "rate_limited", Message: "another search is running", RetryIn: 1})
		return
	}
	limit := httpx.QueryInt(r, "limit", 200, 1, searchMaxHits)
	content := httpx.QueryBool(r, "content")
	budget := searchNameBudget
	if content {
		budget = searchTextBudget
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	lastFlush := time.Now()
	emit := func(h api.FileSearchHit) bool {
		if err := enc.Encode(h); err != nil {
			return false
		}
		if time.Since(lastFlush) > 100*time.Millisecond {
			_ = rc.Flush()
			lastFlush = time.Now()
		}
		return true
	}
	if content {
		if err := s.SearchContent(ctx, dir, query, limit, emit); err != nil {
			s.log.Warn("content search failed", "err", err)
		}
	} else {
		s.SearchNames(ctx, dir, query, limit, emit)
	}
	_ = rc.Flush()
}

func (s *Service) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req api.OpenRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		httpx.Fail(w, &httpx.Err{Status: 400, Code: "bad_request", Message: "path required", Field: "path"})
		return
	}
	real, err := s.res.Resolve(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	fi, err := os.Stat(real)
	if err != nil {
		fail(w, err)
		return
	}
	if isSpecial(fi.Mode()) {
		httpx.Fail(w, httpx.Forbidden("refusing to open a device, socket or pipe"))
		return
	}
	if req.Line < 0 || fi.IsDir() {
		req.Line = 0
	}
	ev := api.OpenRequest{Path: real, Line: req.Line}
	if s.d.Bus != nil {
		s.d.Bus.Publish(api.EvOpen, ev)
	}
	httpx.OK(w, ev)
}
