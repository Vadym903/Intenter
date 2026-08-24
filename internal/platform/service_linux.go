//go:build linux

package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// newServiceManager returns the systemd --user manager, or the unmanaged
// fallback where systemd's user instance is unavailable — containers and some
// minimal distributions have none, and that must not stop Intenter working
// (research R-09, FR-022).
func newServiceManager(p Platform, runner CommandRunner) ServiceManager {
	return &systemdService{platform: p, run: runner}
}

// unitName is the systemd unit Intenter registers.
const unitName = "intenter.service"

// systemdService registers the daemon as a systemd --user unit.
type systemdService struct {
	platform Platform
	run      CommandRunner
}

func (s *systemdService) Name() string { return "systemd --user" }

func (s *systemdService) Available(ctx context.Context) bool {
	// `is-system-running` answers even when the state is degraded; what matters
	// is whether the user instance responds at all.
	if _, err := s.run(ctx, "systemctl", "--user", "is-system-running"); err == nil {
		return true
	}
	_, err := s.run(ctx, "systemctl", "--user", "show-environment")
	return err == nil
}

// unitPath is where the unit file lives.
func (s *systemdService) unitPath() string {
	return filepath.Join(s.platform.HomeDir(), ".config", "systemd", "user", unitName)
}

func (s *systemdService) Install(ctx context.Context, executable string) error {
	if !s.Available(ctx) {
		return fmt.Errorf("platform: systemd --user is not available on this machine")
	}

	path := s.unitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("platform: create the systemd user directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(s.unit(executable)), 0o644); err != nil {
		return fmt.Errorf("platform: write %s: %w", path, err)
	}

	if _, err := s.run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if _, err := s.run(ctx, "systemctl", "--user", "enable", "--now", unitName); err != nil {
		return err
	}
	return nil
}

func (s *systemdService) Uninstall(ctx context.Context) error {
	// Disabling a unit that is not enabled is not an error worth reporting.
	_, _ = s.run(ctx, "systemctl", "--user", "disable", "--now", unitName)

	if err := os.Remove(s.unitPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("platform: remove %s: %w", s.unitPath(), err)
	}
	_, _ = s.run(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}

func (s *systemdService) Start(ctx context.Context) error {
	_, err := s.run(ctx, "systemctl", "--user", "start", unitName)
	return err
}

func (s *systemdService) Stop(ctx context.Context) error {
	_, err := s.run(ctx, "systemctl", "--user", "stop", unitName)
	return err
}

func (s *systemdService) Status(ctx context.Context) (ServiceState, error) {
	if !s.Available(ctx) {
		return ServiceUnsupported, nil
	}
	if _, err := os.Stat(s.unitPath()); os.IsNotExist(err) {
		return ServiceNotInstalled, nil
	}

	output, err := s.run(ctx, "systemctl", "--user", "is-active", unitName)
	if err == nil && strings.TrimSpace(string(output)) == "active" {
		return ServiceRunning, nil
	}
	return ServiceStopped, nil
}

// RegisteredExecutable reads the binary path out of the installed unit's
// ExecStart line, which is what systemd will run at the next login
// (ServiceInspector).
func (s *systemdService) RegisteredExecutable() (string, bool) {
	content, err := os.ReadFile(s.unitPath())
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		command := strings.TrimSpace(strings.TrimPrefix(line, "ExecStart="))
		// The unit runs "<executable> daemon run"; take the program only.
		if space := strings.Index(command, " "); space > 0 {
			command = command[:space]
		}
		if command == "" {
			return "", false
		}
		return command, true
	}
	return "", false
}

// unit renders the systemd unit.
//
// Restart=always is what keeps the gate present: it covers a crash, and it also
// covers the daemon stopping itself on purpose after an upgrade replaced its
// binary (exit 75). A deliberate `systemctl --user stop` — which is what
// `intenter daemon stop` runs — is not restarted, because systemd
// distinguishes an administrative stop from the process exiting on its own.
func (s *systemdService) unit(executable string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Intenter semantic permission daemon",
		"Documentation=https://github.com/Vadym903/Intenter",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + executable + " daemon run",
		"Restart=always",
		"RestartSec=1",
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	}, "\n")
}
