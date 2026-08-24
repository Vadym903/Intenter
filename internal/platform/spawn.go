package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// SpawnDetached starts a background process that survives its parent, with
// stdout and stderr redirected to logPath. `daemon start` uses it in unmanaged
// mode and the hook client uses it for lazy start (§9.2, §9.4, §9.5).
//
// On Windows the child gets no console window (research R-15).
func SpawnDetached(executable string, args []string, logPath string) (int, error) {
	if executable == "" {
		return 0, fmt.Errorf("platform: no executable to spawn")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return 0, fmt.Errorf("platform: create log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("platform: open %s: %w", logPath, err)
	}
	defer logFile.Close()

	cmd := exec.Command(executable, args...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	applyDetachedAttrs(cmd)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("platform: start %s: %w", executable, err)
	}
	pid := cmd.Process.Pid
	// Release the child so it is not tied to this process's lifetime.
	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("platform: release %s: %w", executable, err)
	}
	return pid, nil
}
