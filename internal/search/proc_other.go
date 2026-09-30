//go:build !unix

package search

import "os/exec"

// setProcessGroup is a no-op where process groups are unavailable; the
// default CommandContext cancellation kills the direct child.
func setProcessGroup(*exec.Cmd) {}
