package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const maxOpItems = 1000 // paths per move/copy/delete request

// renameCheck is the non-atomic no-replace rename fallback.
func renameCheck(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrExist}
	}
	return os.Rename(from, to)
}

// Mkdir creates p and any missing parents inside the root.
func (s *Service) Mkdir(p string) (*api.FileEntry, error) {
	real, err := s.res.ResolveCreate(p)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(real); err == nil {
		return nil, httpx.Conflict("a file or folder with that name already exists")
	}
	if err := os.MkdirAll(real, 0o755); err != nil {
		return nil, mapFSError(err)
	}
	return s.Stat(real)
}

// Touch creates an empty file, or updates the modification time of an
// existing one. The parent folder must exist.
func (s *Service) Touch(p string) (*api.FileEntry, error) {
	if real, err := s.res.Resolve(p); err == nil {
		now := time.Now()
		if err := os.Chtimes(real, now, now); err != nil {
			return nil, mapFSError(err)
		}
		return s.Stat(real)
	}
	real, err := s.res.ResolveCreate(p)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(filepath.Dir(real)); err != nil || !st.IsDir() {
		return nil, httpx.NotFound("the folder does not exist")
	}
	f, err := os.OpenFile(real, os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, 0o644)
	if err != nil {
		return nil, mapFSError(err)
	}
	if err := f.Close(); err != nil {
		return nil, mapFSError(err)
	}
	return s.Stat(real)
}

// Rename renames the entry at p to name within the same folder.
func (s *Service) Rename(p, name string) (*api.FileEntry, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	full, fi, err := s.res.ResolveEntry(p)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(filepath.Dir(full), name)
	if dest == full {
		return s.Stat(full)
	}
	if s.trash.Contains(full) {
		return nil, httpx.BadRequest("restore the item before renaming it")
	}
	if dfi, err := os.Lstat(dest); err == nil && os.SameFile(fi, dfi) {
		// Case-only rename on a case-insensitive filesystem.
		err = os.Rename(full, dest)
		if err != nil {
			return nil, mapFSError(err)
		}
		return s.Stat(dest)
	}
	if err := renameNoReplace(full, dest); err != nil {
		return nil, mapFSError(err)
	}
	return s.Stat(dest)
}

// resolveDestDir resolves a move/copy destination folder.
func (s *Service) resolveDestDir(to string) (string, error) {
	if strings.TrimSpace(to) == "" {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "destination folder required", Field: "to"}
	}
	dir, err := s.res.Resolve(to)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return "", mapFSError(err)
	}
	if !st.IsDir() {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "destination is not a folder", Field: "to"}
	}
	return dir, nil
}

type opPair struct {
	src, dst string
	fi       os.FileInfo
}

// planTransfer validates sources for move/copy into dir.
func (s *Service) planTransfer(from []string, dir string, forCopy bool) ([]opPair, error) {
	if len(from) == 0 {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "nothing selected", Field: "from"}
	}
	if len(from) > maxOpItems {
		return nil, httpx.BadRequest(fmt.Sprintf("at most %d items at once", maxOpItems))
	}
	pairs := make([]opPair, 0, len(from))
	seen := map[string]bool{}
	for _, f := range from {
		src, fi, err := s.res.ResolveEntry(f)
		if err != nil {
			return nil, err
		}
		if seen[src] {
			continue
		}
		seen[src] = true
		if fi.IsDir() && within(src, dir) {
			return nil, httpx.BadRequest(fmt.Sprintf("cannot put %q inside itself", filepath.Base(src)))
		}
		dst := filepath.Join(dir, filepath.Base(src))
		if forCopy {
			dst = freeName(dst)
		}
		pairs = append(pairs, opPair{src: src, dst: dst, fi: fi})
	}
	return pairs, nil
}

// Move moves entries into dir. Items already there are left alone; name
// clashes fail with 409 before anything moves.
func (s *Service) Move(ctx context.Context, from []string, to string) ([]api.FileEntry, error) {
	dir, err := s.resolveDestDir(to)
	if err != nil {
		return nil, err
	}
	pairs, err := s.planTransfer(from, dir, false)
	if err != nil {
		return nil, err
	}
	for _, p := range pairs {
		if p.src == p.dst {
			continue
		}
		if _, err := os.Lstat(p.dst); err == nil {
			return nil, httpx.Conflict(fmt.Sprintf("%q already exists in the destination", filepath.Base(p.dst)))
		}
	}
	out := make([]api.FileEntry, 0, len(pairs))
	for _, p := range pairs {
		if p.src != p.dst {
			err := renameNoReplace(p.src, p.dst)
			if errors.Is(err, syscall.EXDEV) {
				err = s.moveAcross(ctx, p.src, p.dst)
			}
			if err != nil {
				return out, mapFSError(fmt.Errorf("move %s: %w", filepath.Base(p.src), err))
			}
		}
		if e, err := s.Stat(p.dst); err == nil {
			out = append(out, *e)
		}
	}
	return out, nil
}

// moveAcross copies then deletes, for moves between filesystems.
func (s *Service) moveAcross(ctx context.Context, src, dst string) error {
	var c copier
	c.ctx = ctx
	if err := c.copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	if c.skipped > 0 {
		return fmt.Errorf("%d items could not be read; the original was kept", c.skipped)
	}
	return os.RemoveAll(src)
}

// copier copies trees, preserving modes and times; symlinks are copied as
// links (never followed), special files and unreadable entries skipped.
type copier struct {
	ctx      context.Context
	files    int64
	bytes    int64
	skipped  int64
	current  string
	progress func(c *copier)
	last     time.Time
	buf      []byte
}

func (c *copier) tick() {
	if c.progress != nil && time.Since(c.last) > 250*time.Millisecond {
		c.last = time.Now()
		c.progress(c)
	}
}

func (c *copier) copyTree(src, dst string) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	fi, err := os.Lstat(src)
	if err != nil {
		c.skipped++
		return nil
	}
	c.current = src
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		t, err := os.Readlink(src)
		if err != nil {
			c.skipped++
			return nil
		}
		if err := os.Symlink(t, dst); err != nil {
			return err
		}
		c.files++
	case fi.IsDir():
		if err := os.Mkdir(dst, fi.Mode().Perm()|0o700); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			c.skipped++
		}
		for _, de := range des {
			if err := c.copyTree(filepath.Join(src, de.Name()), filepath.Join(dst, de.Name())); err != nil {
				return err
			}
		}
		_ = os.Chmod(dst, fi.Mode().Perm())
		_ = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	case fi.Mode().IsRegular():
		if err := c.copyFile(src, dst, fi); err != nil {
			if errors.Is(err, fs.ErrPermission) {
				c.skipped++
				return nil
			}
			return err
		}
		c.files++
	default:
		c.skipped++
	}
	c.tick()
	return nil
}

func (c *copier) copyFile(src, dst string, fi os.FileInfo) error {
	in, err := os.OpenFile(src, os.O_RDONLY|oNoFollow|oNonBlock, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, fi.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if c.buf == nil {
		c.buf = make([]byte, 1<<20)
	}
	for {
		if err := c.ctx.Err(); err != nil {
			out.Close()
			os.Remove(dst)
			return err
		}
		n, rerr := in.Read(c.buf)
		if n > 0 {
			if _, werr := out.Write(c.buf[:n]); werr != nil {
				out.Close()
				os.Remove(dst)
				return werr
			}
			c.bytes += int64(n)
			c.tick()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(dst)
			return rerr
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Chmod(dst, fi.Mode().Perm())
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

// Delete moves paths to the trash (trash=true) or removes them. Items
// already in the trash are always removed permanently.
func (s *Service) Delete(paths []string, trash bool) (removed []string, err error) {
	if len(paths) == 0 {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "nothing selected", Field: "paths"}
	}
	if len(paths) > maxOpItems {
		return nil, httpx.BadRequest(fmt.Sprintf("at most %d items at once", maxOpItems))
	}
	type target struct {
		full string
	}
	targets := make([]target, 0, len(paths))
	for _, p := range paths {
		c, err := s.res.clean(p)
		if err != nil {
			return nil, err
		}
		if s.trash.Contains(c) {
			targets = append(targets, target{c})
			continue
		}
		full, _, err := s.res.ResolveEntry(p)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target{full})
	}
	for _, t := range targets {
		switch {
		case s.trash.Contains(t.full):
			if it, ferr := s.trash.Find(t.full); ferr == nil {
				err = s.trash.Remove(it)
			} else {
				err = os.RemoveAll(t.full)
			}
		case trash:
			_, err = s.trash.Put(t.full)
			if errors.Is(err, ErrCrossDevice) {
				err = &httpx.Err{Status: 409, Code: "conflict", Message: fmt.Sprintf("%q is on another disk and cannot go to the trash; delete it permanently instead", filepath.Base(t.full))}
			}
		default:
			err = os.RemoveAll(t.full)
		}
		if err != nil {
			return removed, mapFSError(err)
		}
		removed = append(removed, t.full)
	}
	return removed, nil
}

func (s *Service) decodeOp(w http.ResponseWriter, r *http.Request) (*api.FileOpRequest, bool) {
	var req api.FileOpRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return nil, false
	}
	return &req, true
}

func (s *Service) handleMkdir(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeOp(w, r)
	if !ok {
		return
	}
	e, err := s.Mkdir(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, e)
}

func (s *Service) handleTouch(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeOp(w, r)
	if !ok {
		return
	}
	e, err := s.Touch(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, e)
}

func (s *Service) handleRename(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeOp(w, r)
	if !ok {
		return
	}
	e, err := s.Rename(req.Path, req.Name)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, e)
}

func (s *Service) handleMove(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeOp(w, r)
	if !ok {
		return
	}
	out, err := s.Move(r.Context(), req.From, req.To)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, out)
}

func (s *Service) handleCopy(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeOp(w, r)
	if !ok {
		return
	}
	dir, err := s.resolveDestDir(req.To)
	if err != nil {
		fail(w, err)
		return
	}
	pairs, err := s.planTransfer(req.From, dir, true)
	if err != nil {
		fail(w, err)
		return
	}
	job := s.jobs.startCopy(s.ctx, pairs, dir)
	httpx.JSON(w, http.StatusAccepted, job)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeOp(w, r)
	if !ok {
		return
	}
	trash := s.useTrash
	if req.Trash != nil {
		trash = *req.Trash
	}
	removed, err := s.Delete(req.Paths, trash)
	if len(removed) > 0 {
		ev := "file.delete"
		if trash {
			ev = "file.trash"
		}
		s.audit(r, ev, summarizePaths(removed))
	}
	if err != nil {
		fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) handleTrashList(w http.ResponseWriter, r *http.Request) {
	items, err := s.trash.List()
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]api.FileEntry, 0, len(items))
	for _, it := range items {
		e := s.entryFor(it.Path, it.Info)
		e.Name = filepath.Base(it.Original)
		e.Hidden = strings.HasPrefix(e.Name, ".")
		e.Target = it.Original // for trash entries: the original location
		if !it.DeletedAt.IsZero() {
			e.ModTime = it.DeletedAt.UTC()
		}
		if e.Type == "dir" {
			e.Children = countChildren(it.Path)
		}
		out = append(out, e)
	}
	httpx.OK(w, out)
}

// Restore puts trashed items back, renaming on clashes ("name (2)").
func (s *Service) Restore(paths []string) ([]api.FileEntry, error) {
	if len(paths) == 0 {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "nothing selected", Field: "paths"}
	}
	out := make([]api.FileEntry, 0, len(paths))
	for _, p := range paths {
		c, err := s.res.clean(p)
		if err != nil {
			return out, err
		}
		it, err := s.trash.Find(c)
		if err != nil {
			return out, err
		}
		dest, err := s.res.ResolveCreate(it.Original)
		if err != nil {
			return out, err
		}
		dest = freeName(dest)
		if err := s.trash.Restore(it, dest); err != nil {
			return out, mapFSError(err)
		}
		if e, err := s.Stat(dest); err == nil {
			out = append(out, *e)
		}
	}
	return out, nil
}

func (s *Service) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeOp(w, r)
	if !ok {
		return
	}
	out, err := s.Restore(req.Paths)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, out)
}

func (s *Service) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	n, err := s.trash.Empty()
	s.audit(r, "file.trash.empty", fmt.Sprintf("%d items", n))
	if err != nil {
		fail(w, err)
		return
	}
	httpx.NoContent(w)
}

// summarizePaths renders paths for an audit detail, capped at ~1 KiB.
func summarizePaths(ps []string) string {
	var b strings.Builder
	for i, p := range ps {
		if b.Len() > 1000 {
			fmt.Fprintf(&b, " … +%d more", len(ps)-i)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p)
	}
	return b.String()
}
