//go:build windows

package platform

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// applyDetachedAttrs starts the child without a console window and detached
// from the parent's console, so `daemon start` never flashes a window
// (research R-15).
func applyDetachedAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS,
	}
}
