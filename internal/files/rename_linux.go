//go:build linux

package files

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from → to, failing with fs.ErrExist instead of
// silently replacing an existing destination (atomic on Linux ≥ 3.15).
func renameNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS):
		// Filesystem without RENAME_NOREPLACE support (some FUSE/NFS).
		return renameCheck(from, to)
	}
	return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
}
