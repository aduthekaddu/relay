//go:build !linux

package apps

import "syscall"

// childAttr puts the child in its own process group so the whole tree can
// be signalled.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
