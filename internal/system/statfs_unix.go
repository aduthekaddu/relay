//go:build unix

package system

import "syscall"

// statfs returns total, free (incl. reserved) and available bytes of the
// filesystem mounted at path.
func statfs(path string) (total, free, avail uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, 0, err
	}
	bs := uint64(st.Bsize) //nolint:unconvert // int64 on Linux, uint32 on macOS
	return st.Blocks * bs, st.Bfree * bs, st.Bavail * bs, nil
}
