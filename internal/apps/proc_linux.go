//go:build linux

package apps

import "syscall"

// childAttr puts the child in its own process group (so the whole tree
// can be signalled) and has the kernel SIGTERM it if Relay dies, so no
// orphaned IDE or X server outlives the server.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
