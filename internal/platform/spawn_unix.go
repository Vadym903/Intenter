//go:build darwin || linux

package platform

import (
	"os/exec"
	"syscall"
)

// applyDetachedAttrs puts the child in its own session so it survives the
// parent's terminal closing.
func applyDetachedAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
