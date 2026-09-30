package files

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/httpx"
)

// Resolver turns client-supplied paths into real, validated paths inside a
// root directory. Every filesystem operation in this package goes through
// it (docs/dev/SECURITY.md rule 2).
type Resolver struct {
	root string // absolute, symlink-free
	home string // for "~" expansion
}

// NewResolver returns a resolver for root. root and home are expanded
// and symlink-resolved; root must be an existing directory.
func NewResolver(root, home string) (*Resolver, error) {
	if home == "" {
		return nil, errors.New("files: home directory unknown")
	}
	if root == "" {
		root = home
	}
	root = expandTilde(root, home)
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("files: root %q is not absolute", root)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("files: resolve root: %w", err)
	}
	st, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("files: stat root: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("files: root %q is not a directory", real)
	}
	return &Resolver{root: real, home: home}, nil
}

// Root returns the resolved root directory.
func (r *Resolver) Root() string { return r.root }

// Inside reports whether the absolute, clean path p is the root or below it.
func (r *Resolver) Inside(p string) bool { return within(r.root, p) }

func within(root, p string) bool {
	if p == root {
		return true
	}
	if root == "/" {
		return strings.HasPrefix(p, "/")
	}
	return strings.HasPrefix(p, root+"/")
}

func expandTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// clean expands "~", requires an absolute path (empty means the root) and
// cleans it. It does not touch the filesystem.
func (r *Resolver) clean(p string) (string, error) {
	if strings.IndexByte(p, 0) >= 0 {
		return "", httpx.BadRequest("invalid path")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return r.root, nil
	}
	p = expandTilde(p, r.home)
	if !filepath.IsAbs(p) {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "path must be absolute or start with ~", Field: "path"}
	}
	return filepath.Clean(p), nil
}

// errOutside is returned for any path that escapes the root; the message
// deliberately does not say whether the target exists.
func errOutside() error {
	return &httpx.Err{Status: 403, Code: "forbidden", Message: "path is outside the allowed folder", Field: "path"}
}

func errNotFound() error { return httpx.NotFound("no such file or folder") }

// mapFSError converts common filesystem errors into API errors.
func mapFSError(err error) error {
	var he *httpx.Err
	switch {
	case err == nil:
		return nil
	case errors.As(err, &he):
		return err
	case errors.Is(err, fs.ErrNotExist):
		return errNotFound()
	case errors.Is(err, fs.ErrPermission):
		return httpx.Forbidden("permission denied")
	case errors.Is(err, fs.ErrExist):
		return httpx.Conflict("a file or folder with that name already exists")
	}
	return err
}

// Resolve follows every symlink in p and returns the real path, which must
// exist and be inside the root. Use it for reads (list, raw, text, zip).
func (r *Resolver) Resolve(p string) (string, error) {
	c, err := r.clean(p)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(c)
	if err != nil {
		// Answer "outside" rather than "not found" for paths that are
		// lexically outside the root, so errors never reveal what exists
		// elsewhere on the machine.
		if !r.Inside(c) {
			return "", errOutside()
		}
		return "", mapFSError(err)
	}
	if !r.Inside(real) {
		return "", errOutside()
	}
	return real, nil
}

// ResolveEntry resolves the parent of p and keeps the final component
// as-is, so operations on a symlink act on the link, not its target. The
// entry must exist (checked with Lstat). The root itself is refused: it
// can be listed but not renamed, moved or deleted.
func (r *Resolver) ResolveEntry(p string) (string, os.FileInfo, error) {
	c, err := r.clean(p)
	if err != nil {
		return "", nil, err
	}
	if c == "/" {
		return "", nil, httpx.Forbidden("refusing to operate on /")
	}
	parent, err := r.Resolve(filepath.Dir(c))
	if err != nil {
		return "", nil, err
	}
	full := filepath.Join(parent, filepath.Base(c))
	if full == r.root || !r.Inside(full) {
		return "", nil, httpx.Forbidden("refusing to operate on the root folder")
	}
	st, err := os.Lstat(full)
	if err != nil {
		return "", nil, mapFSError(err)
	}
	return full, st, nil
}

// ResolveCreate resolves a path that may not exist yet (mkdir -p, touch,
// save as). The deepest existing ancestor is symlink-resolved and must be
// inside the root; the missing components are appended verbatim (they
// cannot be symlinks because they do not exist).
func (r *Resolver) ResolveCreate(p string) (string, error) {
	c, err := r.clean(p)
	if err != nil {
		return "", err
	}
	var missing []string
	cur := c
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", mapFSError(err)
		}
		missing = append(missing, filepath.Base(cur))
		next := filepath.Dir(cur)
		if next == cur {
			return "", errOutside()
		}
		cur = next
	}
	base, err := r.Resolve(cur)
	if err != nil {
		if statusOf404(err) {
			// cur exists (Lstat) but does not resolve: a dangling link.
			return "", httpx.Forbidden("the path goes through a broken symbolic link")
		}
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := ValidName(missing[i]); err != nil {
			return "", err
		}
		base = filepath.Join(base, missing[i])
	}
	if !r.Inside(base) {
		return "", errOutside()
	}
	return base, nil
}

func statusOf404(err error) bool {
	var he *httpx.Err
	return errors.As(err, &he) && he.Status == 404
}

// ValidName checks a single path component supplied by a client.
func ValidName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid name", Field: "name"}
	case strings.ContainsAny(name, "/\x00"):
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "names cannot contain / or NUL", Field: "name"}
	case len(name) > 255:
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "name is too long", Field: "name"}
	case !utf8.ValidString(name):
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "name is not valid UTF-8", Field: "name"}
	}
	return nil
}

// isSpecial reports device files, sockets and named pipes, which Relay
// never opens (reading a FIFO blocks, devices can be dangerous).
func isSpecial(m fs.FileMode) bool {
	return m&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeSocket|fs.ModeNamedPipe|fs.ModeIrregular) != 0
}

// openRegular opens a resolved path for reading and verifies it is a
// regular file (after open, so a swap between check and open is caught).
func openRegular(real string) (*os.File, os.FileInfo, error) {
	st, err := os.Stat(real)
	if err != nil {
		return nil, nil, mapFSError(err)
	}
	if st.IsDir() {
		return nil, nil, httpx.BadRequest("that is a folder")
	}
	if isSpecial(st.Mode()) || !st.Mode().IsRegular() {
		return nil, nil, httpx.Forbidden("refusing to open a device, socket or pipe")
	}
	f, err := os.OpenFile(real, os.O_RDONLY|oNoFollow|oNonBlock, 0)
	if err != nil {
		return nil, nil, mapFSError(err)
	}
	fst, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, mapFSError(err)
	}
	if !fst.Mode().IsRegular() || !os.SameFile(st, fst) {
		f.Close()
		return nil, nil, httpx.Conflict("file changed while opening")
	}
	return f, fst, nil
}
