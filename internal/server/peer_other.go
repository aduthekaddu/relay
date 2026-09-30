//go:build !linux

package server

import "net"

// peerIsSelf: on non-Linux platforms the 0600 socket permissions are the
// boundary; any connection that can open the socket is the owner.
func peerIsSelf(c net.Conn) bool {
	_, ok := c.(*net.UnixConn)
	return ok
}
