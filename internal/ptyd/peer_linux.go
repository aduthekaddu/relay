//go:build linux

package ptyd

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// peerIsSelf reports whether the peer of a unix socket runs as our uid.
func peerIsSelf(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || cerr != nil || cred == nil {
		return false
	}
	return int(cred.Uid) == os.Getuid()
}
