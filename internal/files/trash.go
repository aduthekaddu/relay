package files

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/aduthekaddu/relay/internal/httpx"
)

// Trash implements the freedesktop.org Trash specification (1.0) for the
// home trash: files live in <dir>/files, metadata in
// <dir>/info/<name>.trashinfo. Desktop file managers see the same items.
type Trash struct {
	dir string
}

// TrashItem is one trashed entry.
type TrashItem struct {
	Name      string // name inside the trash (unique)
	Path      string // <dir>/files/<Name>
	Original  string // absolute original location
	DeletedAt time.Time
	Info      os.FileInfo // Lstat of Path
}

// TrashDir returns the home trash location: $XDG_DATA_HOME/Trash or
// ~/.local/share/Trash.
func TrashDir(home string) string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "Trash")
	}
	return filepath.Join(home, ".local", "share", "Trash")
}

// NewTrash returns a trash rooted at dir (created lazily).
func NewTrash(dir string) *Trash { return &Trash{dir: filepath.Clean(dir)} }

// Dir returns the trash directory.
func (t *Trash) Dir() string { return t.dir }

func (t *Trash) filesDir() string { return filepath.Join(t.dir, "files") }
func (t *Trash) infoDir() string  { return filepath.Join(t.dir, "info") }

// Contains reports whether p is a top-level item of the trash
// (<dir>/files/<name>). Deeper paths are deliberately not matched, so a
// symlink inside the trash can never redirect a permanent delete.
func (t *Trash) Contains(p string) bool {
	return filepath.Dir(p) == t.filesDir()
}

func (t *Trash) ensure() error {
	for _, d := range []string{t.dir, t.filesDir(), t.infoDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// escapeTrashPath percent-encodes a path as the spec requires (RFC 2396
// path characters, slashes kept).
func escapeTrashPath(p string) string { return (&url.URL{Path: p}).EscapedPath() }

// ErrCrossDevice is returned when an item is on another filesystem than
// the trash; the caller may offer permanent deletion instead.
var ErrCrossDevice = errors.New("files: item is on a different filesystem than the trash")

// Put moves real (an absolute, validated path) into the trash and returns
// the item. The .trashinfo file is created first with O_EXCL, which
// reserves the name atomically as the spec prescribes.
func (t *Trash) Put(real string) (*TrashItem, error) {
	if within(real, t.dir) {
		return nil, httpx.BadRequest("cannot move the trash (or a folder containing it) to the trash")
	}
	if err := t.ensure(); err != nil {
		return nil, err
	}
	base := filepath.Base(real)
	now := time.Now()
	content := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", escapeTrashPath(real), now.Format("2006-01-02T15:04:05"))
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" { // dotfile such as ".env"
		stem, ext = base, ""
	}
	for i := 1; i < 10000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s.%d%s", stem, i, ext)
		}
		infoPath := filepath.Join(t.infoDir(), name+".trashinfo")
		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		_, werr := f.WriteString(content)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			os.Remove(infoPath)
			return nil, errors.Join(werr, cerr)
		}
		dest := filepath.Join(t.filesDir(), name)
		if _, err := os.Lstat(dest); err == nil { // orphan without info: pick another name
			os.Remove(infoPath)
			continue
		}
		if err := os.Rename(real, dest); err != nil {
			os.Remove(infoPath)
			if errors.Is(err, syscall.EXDEV) {
				return nil, ErrCrossDevice
			}
			return nil, err
		}
		fi, _ := os.Lstat(dest)
		return &TrashItem{Name: name, Path: dest, Original: real, DeletedAt: now, Info: fi}, nil
	}
	return nil, errors.New("files: could not find a free name in the trash")
}

// parseTrashInfo reads Path and DeletionDate from a .trashinfo file.
func (t *Trash) parseTrashInfo(p string) (orig string, at time.Time, err error) {
	f, err := os.Open(p)
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	inGroup := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inGroup = line == "[Trash Info]"
			continue
		}
		if !inGroup {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Path":
			if u, err := url.PathUnescape(strings.TrimSpace(v)); err == nil {
				orig = u
			}
		case "DeletionDate":
			if tm, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSpace(v), time.Local); err == nil {
				at = tm
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", time.Time{}, err
	}
	if orig == "" {
		return "", time.Time{}, errors.New("files: trashinfo without Path")
	}
	if !filepath.IsAbs(orig) { // relative to the trash's top directory
		orig = filepath.Join(filepath.Dir(t.dir), orig)
	}
	return filepath.Clean(orig), at, nil
}

// List returns every item, newest first. Orphaned info files are skipped.
func (t *Trash) List() ([]TrashItem, error) {
	des, err := os.ReadDir(t.infoDir())
	if errors.Is(err, fs.ErrNotExist) {
		return []TrashItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]TrashItem, 0, len(des))
	for _, de := range des {
		name, ok := strings.CutSuffix(de.Name(), ".trashinfo")
		if !ok || de.IsDir() {
			continue
		}
		orig, at, err := t.parseTrashInfo(filepath.Join(t.infoDir(), de.Name()))
		if err != nil {
			continue
		}
		p := filepath.Join(t.filesDir(), name)
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		items = append(items, TrashItem{Name: name, Path: p, Original: orig, DeletedAt: at, Info: fi})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeletedAt.After(items[j].DeletedAt) })
	return items, nil
}

// Find returns the item whose trash path is p, or else the most recently
// deleted item whose original location is p.
func (t *Trash) Find(p string) (*TrashItem, error) {
	items, err := t.List()
	if err != nil {
		return nil, err
	}
	p = filepath.Clean(p)
	for i := range items {
		if items[i].Path == p {
			return &items[i], nil
		}
	}
	for i := range items {
		if items[i].Original == p {
			return &items[i], nil
		}
	}
	return nil, httpx.NotFound("not in the trash")
}

// Restore moves it back to dest (normally it.Original, possibly renamed by
// the caller to avoid a clash) and removes its info file.
func (t *Trash) Restore(it *TrashItem, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := renameNoReplace(it.Path, dest); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(t.infoDir(), it.Name+".trashinfo"))
	return nil
}

// Remove permanently deletes one item.
func (t *Trash) Remove(it *TrashItem) error {
	if err := os.RemoveAll(it.Path); err != nil {
		return err
	}
	return os.Remove(filepath.Join(t.infoDir(), it.Name+".trashinfo"))
}

// Empty permanently deletes everything in the trash and returns how many
// top-level items were removed.
func (t *Trash) Empty() (int, error) {
	n := 0
	var errs []error
	for _, d := range []string{t.filesDir(), t.infoDir()} {
		des, err := os.ReadDir(d)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, de := range des {
			if err := os.RemoveAll(filepath.Join(d, de.Name())); err != nil {
				errs = append(errs, err)
			} else if d == t.filesDir() {
				n++
			}
		}
	}
	_ = os.Remove(filepath.Join(t.dir, "directorysizes"))
	return n, errors.Join(errs...)
}

// freeName returns p, or "name (2).ext", "name (3).ext"… if p exists.
func freeName(p string) string {
	if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		return p
	}
	dir, base := filepath.Split(p)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem, ext = base, ""
	}
	for i := 2; i < 10000; i++ {
		c := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Lstat(c); errors.Is(err, fs.ErrNotExist) {
			return c
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, time.Now().UnixNano(), ext))
}
