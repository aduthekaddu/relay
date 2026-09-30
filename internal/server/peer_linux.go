//go:build linux

package server

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// peerIsSelf reports whether the process on the other end of a unix
// socket runs as our uid. The socket is already 0600, this is defence in
// depth against a permissive umask or a copied socket path.
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
	err = raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil || cerr != nil || cred == nil {
		return false
	}
	return int(cred.Uid) == os.Getuid()
}
