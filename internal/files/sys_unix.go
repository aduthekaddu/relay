//go:build unix

package files

import (
	"os"
	"syscall"
)

const (
	oNoFollow = syscall.O_NOFOLLOW
	oNonBlock = syscall.O_NONBLOCK
)

// fileID returns (device, inode, link count) for fi, or ok=false.
func fileID(fi os.FileInfo) (dev, ino, nlink uint64, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, 0, false
	}
	return uint64(st.Dev), uint64(st.Ino), uint64(st.Nlink), true //nolint:unconvert // widths differ per OS
}

// ownerUID returns the owning uid of fi, or -1.
func ownerUID(fi os.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}
