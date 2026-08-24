package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The service managers are tested through an injected command runner, so no
// test touches the machine's real service system. Opt in to exercising the real
// one with INTENTER_SERVICE_TESTS=1.

// recordingRunner captures the commands a manager would run.
type recordingRunner struct {
	calls   []string
	outputs map[string]string
	fail    map[string]error
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{outputs: map[string]string{}, fail: map[string]error{}}
}

func (r *recordingRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.calls = append(r.calls, call)

	for prefix, err := range r.fail {
		if strings.HasPrefix(call, prefix) {
			return nil, err
		}
	}
	for prefix, output := range r.outputs {
		if strings.HasPrefix(call, prefix) {
			return []byte(output), nil
		}
	}
	return nil, nil
}

// ranMatching reports whether any recorded call contains every fragment.
func (r *recordingRunner) ranMatching(fragments ...string) bool {
	for _, call := range r.calls {
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(call, fragment) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// servicePlatform builds a platform rooted in a temp directory.
func servicePlatform(t *testing.T) Platform {
	t.Helper()
	base := t.TempDir()

	t.Setenv(EnvTestMode, "1")
	t.Setenv(EnvTestHome, filepath.Join(base, "home"))
	t.Setenv(EnvDataDir, filepath.Join(base, "data"))
	t.Setenv(EnvConfigDir, filepath.Join(base, "config"))
	t.Setenv(EnvRuntimeDir, filepath.Join(base, "run"))

	if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	p, err := New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}
	return p
}

func TestServiceInstallIsIdempotent(t *testing.T) {
	// §12.4: running setup twice must converge, and on macOS that means
	// replacing a loaded agent rather than failing to bootstrap it again.
	if runtime.GOOS == "windows" {
		t.Skip("the Windows manager writes the registry, covered separately")
	}

	p := servicePlatform(t)
	runner := newRecordingRunner()
	runner.outputs["systemctl --user is-system-running"] = "running"
	runner.outputs["launchctl help"] = "usage"

	manager := newServiceManagerWithRunner(p, runner.run)
	for i := 0; i < 2; i++ {
		if err := manager.Install(context.Background(), "/usr/local/bin/intenter"); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}

	state, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if state == ServiceNotInstalled {
		t.Error("the service must be registered after install")
	}
}

func TestServiceInstallWritesADefinitionNamingTheBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows manager writes the registry, not a file")
	}

	p := servicePlatform(t)
	runner := newRecordingRunner()
	runner.outputs["systemctl --user is-system-running"] = "running"
	runner.outputs["launchctl help"] = "usage"

	const executable = "/opt/intenter/bin/intenter"
	manager := newServiceManagerWithRunner(p, runner.run)
	if err := manager.Install(context.Background(), executable); err != nil {
		t.Fatalf("install: %v", err)
	}

	definition := findServiceDefinition(t, p)
	if !strings.Contains(definition, executable) {
		t.Errorf("the definition must name the binary:\n%s", definition)
	}
	if !strings.Contains(definition, "daemon run") && !strings.Contains(definition, "<string>run</string>") {
		t.Errorf("the definition must run `daemon run`:\n%s", definition)
	}

	// It asks the platform to keep the daemon alive, which is the point of
	// registering at all.
	if !strings.Contains(definition, "KeepAlive") && !strings.Contains(definition, "Restart=") {
		t.Errorf("the definition must ask for supervision:\n%s", definition)
	}

	// Unconditional supervision, not only on failure: after an upgrade the
	// daemon stops itself on purpose so the new binary can take over, and
	// `Restart=on-failure` would be a coin flip on whether it comes back.
	if strings.Contains(definition, "Restart=") && !strings.Contains(definition, "Restart=always") {
		t.Errorf("the systemd unit must use Restart=always so a self-refresh returns:\n%s", definition)
	}
	if strings.Contains(definition, "KeepAlive") && !strings.Contains(definition, "<true/>") {
		t.Errorf("the launchd plist must keep the daemon alive:\n%s", definition)
	}
}

// findServiceDefinition returns the unit or plist the manager wrote.
func findServiceDefinition(t *testing.T, p Platform) string {
	t.Helper()

	candidates := []string{
		filepath.Join(p.HomeDir(), "Library", "LaunchAgents", ServiceLabel+".plist"),
		filepath.Join(p.HomeDir(), ".config", "systemd", "user", "intenter.service"),
	}
	for _, path := range candidates {
		content, err := os.ReadFile(path)
		if err == nil {
			return string(content)
		}
	}
	t.Fatalf("no service definition was written; looked in %v", candidates)
	return ""
}

func TestServiceUninstallRemovesTheDefinition(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows manager writes the registry, not a file")
	}

	p := servicePlatform(t)
	runner := newRecordingRunner()
	runner.outputs["systemctl --user is-system-running"] = "running"
	runner.outputs["launchctl help"] = "usage"

	manager := newServiceManagerWithRunner(p, runner.run)
	if err := manager.Install(context.Background(), "/usr/local/bin/intenter"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := manager.Uninstall(context.Background()); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	state, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if state != ServiceNotInstalled && state != ServiceUnsupported {
		t.Errorf("state = %s, want the registration gone", state)
	}
}

func TestUninstallingWhatWasNeverInstalledIsNotAnError(t *testing.T) {
	// Uninstall has to be safe to run on a machine that never had a service.
	p := servicePlatform(t)
	runner := newRecordingRunner()
	runner.outputs["systemctl --user is-system-running"] = "running"
	runner.outputs["launchctl help"] = "usage"

	manager := newServiceManagerWithRunner(p, runner.run)
	if err := manager.Uninstall(context.Background()); err != nil {
		t.Errorf("uninstall: %v", err)
	}
}

func TestServiceStatusWithoutInstallation(t *testing.T) {
	p := servicePlatform(t)
	runner := newRecordingRunner()
	runner.outputs["systemctl --user is-system-running"] = "running"
	runner.outputs["launchctl help"] = "usage"

	manager := newServiceManagerWithRunner(p, runner.run)
	state, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if state == ServiceRunning {
		t.Errorf("state = %s, want nothing running", state)
	}
}

func TestUnmanagedFallbackIsHonest(t *testing.T) {
	// A machine with no per-user service manager is a supported configuration,
	// not a failure: the hook's lazy start covers it (FR-022). What matters is
	// that status says so rather than pretending.
	manager := NewUnmanagedService()

	if manager.Name() != ModeUnmanaged {
		t.Errorf("name = %q, want %q", manager.Name(), ModeUnmanaged)
	}
	if manager.Available(context.Background()) {
		t.Error("the unmanaged fallback manages nothing")
	}

	state, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if state != ServiceUnsupported {
		t.Errorf("state = %s, want unsupported", state)
	}

	if err := manager.Install(context.Background(), "/usr/local/bin/intenter"); err == nil {
		t.Error("installing must fail clearly rather than silently do nothing")
	}
	// Removing and stopping nothing is fine, so uninstall stays safe to run.
	if err := manager.Uninstall(context.Background()); err != nil {
		t.Errorf("uninstall: %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Errorf("stop: %v", err)
	}
}

func TestLinuxWithoutSystemdFallsBackCleanly(t *testing.T) {
	// The common container case: no systemd user instance.
	if runtime.GOOS != "linux" {
		t.Skip("systemd is a Linux concern")
	}

	p := servicePlatform(t)
	runner := newRecordingRunner()
	runner.fail["systemctl"] = errors.New("Failed to connect to bus")

	manager := newServiceManagerWithRunner(p, runner.run)
	if manager.Available(context.Background()) {
		t.Fatal("systemd must be reported unavailable")
	}

	state, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if state != ServiceUnsupported {
		t.Errorf("state = %s, want unsupported", state)
	}
	if err := manager.Install(context.Background(), "/usr/local/bin/intenter"); err == nil {
		t.Error("install must fail clearly so setup can report unmanaged mode")
	}
}

func TestRealServiceInstallOptIn(t *testing.T) {
	// Touching the machine's real service system is opt-in: a CI run must not
	// register a background service as a side effect.
	if os.Getenv("INTENTER_SERVICE_TESTS") != "1" {
		t.Skip("set INTENTER_SERVICE_TESTS=1 to exercise the real service manager")
	}

	p := servicePlatform(t)
	manager := NewServiceManager(p)
	if !manager.Available(context.Background()) {
		t.Skipf("%s is not available here", manager.Name())
	}

	executable, err := p.SelfExecutablePath()
	if err != nil {
		t.Fatalf("self path: %v", err)
	}
	if err := manager.Install(context.Background(), executable); err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Uninstall(context.Background()); err != nil {
			t.Errorf("uninstall: %v", err)
		}
	})

	state, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if state == ServiceNotInstalled {
		t.Error("the service must be registered")
	}
}
