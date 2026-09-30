package files

import (
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const (
	listDefaultLimit = 500
	listMaxLimit     = 5000
	childCountCap    = 1000 // children counts stop at this many entries
	childCountDirs   = 500  // at most this many dirs per page get a count
)

// MimeDirectory marks folders (and symlinks to folders) in FileEntry.Mime.
const MimeDirectory = "inode/directory"

// extraTypes covers common developer files that mime.TypeByExtension does
// not know or maps poorly on minimal systems.
var extraTypes = map[string]string{
	".md": "text/markdown", ".markdown": "text/markdown", ".txt": "text/plain",
	".go": "text/x-go", ".rs": "text/x-rust", ".py": "text/x-python", ".rb": "text/x-ruby",
	".ts": "text/typescript", ".tsx": "text/typescript", ".jsx": "text/javascript",
	".js": "text/javascript", ".mjs": "text/javascript", ".cjs": "text/javascript",
	".json": "application/json", ".jsonl": "application/x-ndjson", ".ndjson": "application/x-ndjson",
	".yaml": "application/yaml", ".yml": "application/yaml", ".toml": "application/toml",
	".sh": "text/x-shellscript", ".bash": "text/x-shellscript", ".zsh": "text/x-shellscript",
	".c": "text/x-c", ".h": "text/x-c", ".cpp": "text/x-c++", ".hpp": "text/x-c++", ".cc": "text/x-c++",
	".java": "text/x-java", ".kt": "text/x-kotlin", ".swift": "text/x-swift", ".lua": "text/x-lua",
	".css": "text/css", ".scss": "text/x-scss", ".html": "text/html", ".htm": "text/html",
	".svg": "image/svg+xml", ".xml": "application/xml", ".csv": "text/csv", ".tsv": "text/tab-separated-values",
	".sql": "application/sql", ".log": "text/plain", ".ini": "text/plain", ".conf": "text/plain",
	".env": "text/plain", ".lock": "text/plain", ".diff": "text/x-diff", ".patch": "text/x-diff",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".heic": "image/heic", ".pdf": "application/pdf", ".mp4": "video/mp4", ".webm": "video/webm",
	".mov": "video/quicktime", ".mkv": "video/x-matroska", ".mp3": "audio/mpeg", ".wav": "audio/wav",
	".ogg": "audio/ogg", ".flac": "audio/flac", ".m4a": "audio/mp4", ".opus": "audio/ogg",
	".zip": "application/zip", ".gz": "application/gzip", ".tgz": "application/gzip",
	".tar": "application/x-tar", ".xz": "application/x-xz", ".zst": "application/zstd",
	".wasm": "application/wasm", ".cast": "application/x-asciicast", ".blend": "application/x-blender",
}

// wellKnownText are extension-less file names that are text.
var wellKnownText = map[string]bool{
	"Makefile": true, "Dockerfile": true, "LICENSE": true, "README": true, "Caddyfile": true,
	"Procfile": true, "Gemfile": true, "Justfile": true, "Vagrantfile": true, ".gitignore": true,
	".gitattributes": true, ".editorconfig": true, ".bashrc": true, ".zshrc": true, ".profile": true,
}

// mimeByName guesses a media type from the file name only (no I/O).
func mimeByName(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if t, ok := extraTypes[ext]; ok {
		return t
	}
	if ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			if i := strings.IndexByte(t, ';'); i > 0 {
				t = t[:i]
			}
			return t
		}
	}
	if wellKnownText[name] {
		return "text/plain"
	}
	return ""
}

func fileType(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "dir"
	case m&fs.ModeSymlink != 0:
		return "symlink"
	case m.IsRegular():
		return "file"
	}
	return "other"
}

// entryFor builds a FileEntry from Lstat info. For symlinks it records the
// link target and, when the target is inside the root, its size/type hint.
func (s *Service) entryFor(full string, fi os.FileInfo) api.FileEntry {
	name := fi.Name()
	if full == s.res.Root() || full == "/" {
		name = filepath.Base(full)
	}
	e := api.FileEntry{
		Name:    name,
		Path:    full,
		Type:    fileType(fi.Mode()),
		Size:    fi.Size(),
		ModTime: fi.ModTime().UTC(),
		Mode:    fi.Mode().String(),
		Hidden:  strings.HasPrefix(name, "."),
	}
	switch e.Type {
	case "dir":
		e.Size = 0
		e.Mime = MimeDirectory
	case "file":
		e.Mime = mimeByName(name)
	case "symlink":
		if t, err := os.Readlink(full); err == nil {
			e.Target = t
		}
		if real, err := filepath.EvalSymlinks(full); err == nil && s.res.Inside(real) {
			if st, err := os.Stat(real); err == nil {
				if st.IsDir() {
					e.Mime = MimeDirectory
					e.Size = 0
				} else {
					e.Mime = mimeByName(filepath.Base(real))
					e.Size = st.Size()
				}
			}
		}
	}
	return e
}

// countChildren returns the number of entries in dir, capped.
func countChildren(dir string) int {
	f, err := os.Open(dir)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	for n < childCountCap {
		names, err := f.Readdirnames(256)
		n += len(names)
		if err != nil {
			break
		}
	}
	if n > childCountCap {
		n = childCountCap
	}
	return n
}

type listOptions struct {
	hidden bool
	sort   string // name | size | mtime | type
	desc   bool
	offset int
	limit  int
}

// List returns one page of a directory listing.
func (s *Service) List(dir string, o listOptions) (*api.DirListing, error) {
	real, err := s.res.Resolve(dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(real)
	if err != nil {
		return nil, mapFSError(err)
	}
	if !st.IsDir() {
		return nil, httpx.BadRequest("not a folder")
	}
	des, err := os.ReadDir(real)
	if err != nil && len(des) == 0 {
		return nil, mapFSError(err)
	}
	out := &api.DirListing{Path: real, Offset: o.offset, Entries: []api.FileEntry{}}
	if real != s.res.Root() {
		out.Parent = filepath.Dir(real)
	}
	entries := make([]api.FileEntry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if !o.hidden && strings.HasPrefix(name, ".") {
			out.Hidden++
			continue
		}
		fi, err := de.Info() // Lstat
		if err != nil {
			continue // vanished between ReadDir and Lstat
		}
		entries = append(entries, s.entryFor(filepath.Join(real, name), fi))
	}
	sortEntries(entries, o.sort, o.desc)
	out.Total = len(entries)
	if o.offset > len(entries) {
		o.offset = len(entries)
	}
	end := o.offset + o.limit
	if end > len(entries) {
		end = len(entries)
	}
	page := entries[o.offset:end]
	dirs := 0
	for i := range page {
		if page[i].Type == "dir" && dirs < childCountDirs {
			page[i].Children = countChildren(page[i].Path)
			dirs++
		}
	}
	if root, status := s.git.statusFor(s.ctx, real); root != "" {
		out.GitRoot = root
		applyGit(page, root, status)
	}
	out.Entries = page
	return out, nil
}

// sortEntries orders folders first, then by key; names compare
// case-insensitively with a natural number order ("file2" < "file10").
func sortEntries(es []api.FileEntry, key string, desc bool) {
	isDir := func(e api.FileEntry) bool { return e.Mime == MimeDirectory }
	less := func(a, b api.FileEntry) bool {
		switch key {
		case "size":
			if a.Size != b.Size {
				return a.Size < b.Size
			}
		case "mtime":
			if !a.ModTime.Equal(b.ModTime) {
				return a.ModTime.Before(b.ModTime)
			}
		case "type":
			ea, eb := strings.ToLower(filepath.Ext(a.Name)), strings.ToLower(filepath.Ext(b.Name))
			if ea != eb {
				return ea < eb
			}
		}
		return naturalLess(a.Name, b.Name)
	}
	sort.SliceStable(es, func(i, j int) bool {
		di, dj := isDir(es[i]), isDir(es[j])
		if di != dj {
			return di
		}
		if desc {
			return less(es[j], es[i])
		}
		return less(es[i], es[j])
	})
}

// naturalLess compares strings case-insensitively, treating digit runs as
// numbers.
func naturalLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	i, j := 0, 0
	for i < len(la) && j < len(lb) {
		ca, cb := la[i], lb[j]
		if isDigit(ca) && isDigit(cb) {
			si := i
			for i < len(la) && isDigit(la[i]) {
				i++
			}
			sj := j
			for j < len(lb) && isDigit(lb[j]) {
				j++
			}
			na := strings.TrimLeft(la[si:i], "0")
			nb := strings.TrimLeft(lb[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	if len(la)-i != len(lb)-j {
		return len(la)-i < len(lb)-j
	}
	return a < b
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Stat returns the entry for path (symlinks are described, not followed,
// except that the final path must still resolve inside the root).
func (s *Service) Stat(p string) (*api.FileEntry, error) {
	c, err := s.res.clean(p)
	if err != nil {
		return nil, err
	}
	if c == s.res.Root() {
		fi, err := os.Lstat(c)
		if err != nil {
			return nil, mapFSError(err)
		}
		e := s.entryFor(c, fi)
		return &e, nil
	}
	full, fi, err := s.res.ResolveEntry(p)
	if err != nil {
		return nil, err
	}
	e := s.entryFor(full, fi)
	if e.Type == "dir" {
		e.Children = countChildren(full)
	}
	if root, status := s.git.statusFor(s.ctx, filepath.Dir(full)); root != "" {
		one := []api.FileEntry{e}
		applyGit(one, root, status)
		e = one[0]
	}
	return &e, nil
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	o := listOptions{
		hidden: httpx.QueryBool(r, "hidden") || (q.Get("hidden") == "" && s.d.Cfg.Files.ShowHidden),
		sort:   q.Get("sort"),
		desc:   httpx.QueryBool(r, "desc"),
		offset: httpx.QueryInt(r, "offset", 0, 0, 1<<30),
		limit:  httpx.QueryInt(r, "limit", listDefaultLimit, 1, listMaxLimit),
	}
	switch o.sort {
	case "", "name", "size", "mtime", "type":
	default:
		httpx.Fail(w, &httpx.Err{Status: 400, Code: "bad_request", Message: "sort must be name, size, mtime or type", Field: "sort"})
		return
	}
	l, err := s.List(q.Get("path"), o)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, l)
}

func (s *Service) handleStat(w http.ResponseWriter, r *http.Request) {
	e, err := s.Stat(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, e)
}
