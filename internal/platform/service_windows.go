//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// newServiceManager returns the Windows per-user manager (research R-09).
//
// A real Windows Service would need administrator rights, which Intenter must
// never ask for: it only gates one user's agent. The HKCU Run key starts the
// daemon with that user's session and nothing else, and the hook's lazy start
// covers the gap before the first logon.
func newServiceManager(p Platform, runner CommandRunner) ServiceManager {
	return &runKeyService{platform: p, run: runner}
}

// runKeyPath is the per-user autostart key.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// runKeyValue is the value name Intenter owns there.
const runKeyValue = "Intenter"

type runKeyService struct {
	platform Platform
	run      CommandRunner
}

func (s *runKeyService) Name() string { return "run key" }

func (s *runKeyService) Available(context.Context) bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	key.Close()
	return true
}

func (s *runKeyService) Install(ctx context.Context, executable string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("platform: open the Run key: %w", err)
	}
	defer key.Close()

	command := fmt.Sprintf("%q daemon start", executable)
	if err := key.SetStringValue(runKeyValue, command); err != nil {
		return fmt.Errorf("platform: write the Run key value: %w", err)
	}
	return nil
}

func (s *runKeyService) Uninstall(context.Context) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("platform: open the Run key: %w", err)
	}
	defer key.Close()

	if err := key.DeleteValue(runKeyValue); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("platform: remove the Run key value: %w", err)
	}
	return nil
}

// Start launches the daemon now; the Run key only covers the next logon.
func (s *runKeyService) Start(ctx context.Context) error {
	executable, err := s.platform.SelfExecutablePath()
	if err != nil {
		return err
	}
	logPath := filepath.Join(LogDir(s.platform), "daemon.log")
	if _, err := SpawnDetached(executable, []string{"daemon", "run"}, logPath); err != nil {
		return fmt.Errorf("platform: start the daemon: %w", err)
	}
	return nil
}

// Stop leaves the registration in place; the caller asks the daemon itself to
// exit through the protocol.
func (s *runKeyService) Stop(context.Context) error { return nil }

func (s *runKeyService) Status(ctx context.Context) (ServiceState, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return ServiceNotInstalled, nil
	}
	defer key.Close()

	value, _, err := key.GetStringValue(runKeyValue)
	if err != nil || strings.TrimSpace(value) == "" {
		return ServiceNotInstalled, nil
	}

	// The Run key says the daemon will start at the next logon; whether one is
	// running now is answered by the pid file the daemon maintains.
	if pid := readPidFile(PidFilePath(s.platform)); pid > 0 {
		if process, err := os.FindProcess(pid); err == nil && process != nil {
			return ServiceRunning, nil
		}
	}
	return ServiceStopped, nil
}

// RegisteredExecutable reads the binary path out of the Run key value, which is
// what Windows will launch at the next logon (ServiceInspector).
func (s *runKeyService) RegisteredExecutable() (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()

	value, _, err := key.GetStringValue(runKeyValue)
	if err != nil {
		return "", false
	}
	return firstQuotedWord(value)
}

// firstQuotedWord extracts the program from a `"C:\path\intenter.exe" daemon
// start` command line. The path is quoted precisely because it may contain
// spaces, so splitting on whitespace would cut it in half.
func firstQuotedWord(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", false
	}
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : 1+end], true
		}
		return "", false
	}
	if space := strings.Index(command, " "); space > 0 {
		return command[:space], true
	}
	return command, true
}

// readPidFile reads the daemon's recorded process id, or 0. The daemon package
// owns the pid file, but platform cannot import it — that dependency runs the
// other way — so this reads the same one-line format.
func readPidFile(path string) int {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return 0
	}
	return pid
}
