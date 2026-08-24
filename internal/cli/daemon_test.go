package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/version"
)

// runCLI executes a command in-process and returns stdout, stderr and the exit
// code.
func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

// isolate points the CLI at temporary directories so tests never touch real
// user state (§28.3).
func isolate(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	if runtime.GOOS != "windows" {
		// Unix sockets need a short path (§10.1).
		short, err := os.MkdirTemp("/tmp", "agc")
		if err != nil {
			t.Fatalf("temp dir: %v", err)
		}
		t.Cleanup(func() { os.RemoveAll(short) })
		base = short
	}
	// The home must exist before the platform resolves it: HomeDir is the
	// symlink-resolved path (macOS /tmp → /private/tmp, a Windows 8.3 temp
	// directory to its long name), and a test comparing paths against a
	// later command's output needs the same form on both sides.
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv(platform.EnvTestMode, "1")
	t.Setenv(platform.EnvTestHome, home)
	t.Setenv(platform.EnvDataDir, filepath.Join(base, "data"))
	t.Setenv(platform.EnvConfigDir, filepath.Join(base, "config"))
	t.Setenv(platform.EnvRuntimeDir, filepath.Join(base, "run"))
	t.Setenv(platform.EnvEndpoint, "")
	return base
}

func TestVersionCommand(t *testing.T) {
	isolate(t)

	out, _, code := runCLI(t, "version")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{version.Version, "engine", "protocol", "schema"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q:\n%s", want, out)
		}
	}
}

func TestVersionCommandJSON(t *testing.T) {
	isolate(t)

	out, _, code := runCLI(t, "version", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("--json must emit JSON: %v (%s)", err, out)
	}
	for _, key := range []string{"version", "engine_version", "protocol_version", "schema_version"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing key %q", key)
		}
	}
}

func TestUnknownCommandFails(t *testing.T) {
	isolate(t)

	_, errOut, code := runCLI(t, "definitely-not-a-command")
	if code != ExitError {
		t.Errorf("exit code = %d, want %d", code, ExitError)
	}
	if errOut == "" {
		t.Error("an unknown command must explain itself on stderr")
	}
}

func TestDaemonStatusWhenNotRunning(t *testing.T) {
	isolate(t)

	out, _, code := runCLI(t, "daemon", "status")
	if code != ExitDaemonUnreached {
		t.Errorf("exit code = %d, want %d for an unreachable daemon", code, ExitDaemonUnreached)
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("status output = %q", out)
	}
	if !strings.Contains(out, "daemon start") {
		t.Error("status must suggest how to start the daemon")
	}
}

func TestDaemonStatusJSONWhenNotRunning(t *testing.T) {
	isolate(t)

	out, _, code := runCLI(t, "daemon", "status", "--json")
	if code != ExitDaemonUnreached {
		t.Errorf("exit code = %d, want %d", code, ExitDaemonUnreached)
	}
	var status DaemonStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("--json must emit JSON: %v (%s)", err, out)
	}
	if status.Running {
		t.Error("running must be false")
	}
	if status.Endpoint == "" || status.DBPath == "" {
		t.Errorf("status = %+v", status)
	}
}

func TestDaemonStopWhenNotRunning(t *testing.T) {
	isolate(t)

	_, errOut, code := runCLI(t, "daemon", "stop")
	if code != ExitDaemonUnreached {
		t.Errorf("exit code = %d, want %d", code, ExitDaemonUnreached)
	}
	if !strings.Contains(errOut, "not running") {
		t.Errorf("stderr = %q", errOut)
	}
}

// runCLIWithServices runs a command against an injected service manager, so no
// test registers or removes anything on the machine running it.
func runCLIWithServices(t *testing.T, manager platform.ServiceManager, args ...string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer
	root, app := NewRootCommand(&out, &errOut)
	app.Services = manager
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errOut)

	code := ExitOK
	if err := root.Execute(); err != nil {
		var exit *exitError
		if errors.As(err, &exit) {
			fmt.Fprintf(&errOut, "intenter: %v\n", exit.err)
			code = exit.code
		} else {
			fmt.Fprintf(&errOut, "intenter: %v\n", err)
			code = ExitError
		}
	}
	return out.String(), errOut.String(), code
}

func TestDaemonServiceUninstallIsAlwaysSafe(t *testing.T) {
	// Unregistering a service that was never registered has to succeed, so
	// `intenter uninstall claude` works on any machine.
	isolate(t)

	out, errOut, code := runCLIWithServices(t, platform.NewUnmanagedService(), "daemon", "uninstall")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "unregistered") {
		t.Errorf("output = %q, want confirmation", out)
	}
}

func TestDaemonServiceInstallFallsBackToUnmanaged(t *testing.T) {
	// A machine with no per-user service manager is supported, and the user is
	// told what to expect rather than shown a failure (FR-022).
	isolate(t)

	out, errOut, code := runCLIWithServices(t, platform.NewUnmanagedService(), "daemon", "install")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "unmanaged") {
		t.Errorf("output = %q, want it to name unmanaged mode", out)
	}
	if !strings.Contains(out, "intenter daemon start") {
		t.Errorf("output = %q, want it to say how to start the daemon", out)
	}
}

// fakeServiceManager records what the CLI asked it to do.
type fakeServiceManager struct {
	available  bool
	installed  bool
	executable string
	state      platform.ServiceState
}

func (f *fakeServiceManager) Name() string                   { return "fake" }
func (f *fakeServiceManager) Available(context.Context) bool { return f.available }

func (f *fakeServiceManager) Install(_ context.Context, executable string) error {
	f.installed = true
	f.executable = executable
	f.state = platform.ServiceRunning
	return nil
}

func (f *fakeServiceManager) Uninstall(context.Context) error {
	f.installed = false
	f.state = platform.ServiceNotInstalled
	return nil
}

func (f *fakeServiceManager) Start(context.Context) error { return nil }
func (f *fakeServiceManager) Stop(context.Context) error  { return nil }

func (f *fakeServiceManager) Status(context.Context) (platform.ServiceState, error) {
	if f.state == "" {
		return platform.ServiceNotInstalled, nil
	}
	return f.state, nil
}

func TestDaemonServiceInstallRegistersTheRunningBinary(t *testing.T) {
	// A hook or service entry that names a binary which has moved is worse
	// than none, so what gets registered is the executable actually running.
	isolate(t)
	manager := &fakeServiceManager{available: true}

	out, errOut, code := runCLIWithServices(t, manager, "daemon", "install")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !manager.installed {
		t.Fatal("the service must be registered")
	}
	if manager.executable == "" {
		t.Error("the registration must name the executable")
	}
	if !strings.Contains(out, "registered") {
		t.Errorf("output = %q, want confirmation", out)
	}

	if _, _, code := runCLIWithServices(t, manager, "daemon", "uninstall"); code != ExitOK {
		t.Fatalf("uninstall exit code = %d", code)
	}
	if manager.installed {
		t.Error("the registration must be removed")
	}
}

func TestDaemonServiceInstallReportsItsMode(t *testing.T) {
	// Either it registers a service, or it says the daemon will run unmanaged
	// — both are supported outcomes, and the user is told which one they got
	// (FR-022).
	//
	// This is opt-in because it reaches the machine's real service manager:
	// registering a background agent pointing at a temporary test binary is not
	// something an ordinary `go test` run may do. The manager's own behavior is
	// covered with an injected command runner in internal/platform.
	if os.Getenv("INTENTER_SERVICE_TESTS") != "1" {
		t.Skip("set INTENTER_SERVICE_TESTS=1 to exercise the real service manager")
	}
	isolate(t)

	out, errOut, code := runCLI(t, "daemon", "install")
	t.Cleanup(func() { runCLI(t, "daemon", "uninstall") })

	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	managed := strings.Contains(out, "registered")
	unmanaged := strings.Contains(out, "unmanaged")
	if !managed && !unmanaged {
		t.Errorf("output must say which mode is in effect:\n%s", out)
	}
}

func TestDataDirFlagOverridesEnvironment(t *testing.T) {
	isolate(t)
	override := t.TempDir()

	out, _, _ := runCLI(t, "daemon", "status", "--json", "--data-dir", override)

	var status DaemonStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if !strings.HasPrefix(status.DBPath, override) {
		t.Errorf("db path = %q, want it under the --data-dir override %q", status.DBPath, override)
	}
}

func TestUnknownConfigKeysWarnOnStderr(t *testing.T) {
	base := isolate(t)
	configDir := filepath.Join(base, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"),
		[]byte("[log]\nlevel = \"info\"\nunknown_key = 1\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, errOut, code := runCLI(t, "version")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(errOut, "unknown configuration key") {
		t.Errorf("stderr = %q, want an unknown-key warning", errOut)
	}
}

func TestInvalidConfigFails(t *testing.T) {
	base := isolate(t)
	configPath := filepath.Join(base, "bad.toml")
	if err := os.WriteFile(configPath, []byte("[daemon]\nrequest_timeout_ms = 0\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, errOut, code := runCLI(t, "version", "--config", configPath)
	if code != ExitError {
		t.Errorf("exit code = %d, want %d", code, ExitError)
	}
	if !strings.Contains(errOut, "request_timeout_ms") {
		t.Errorf("stderr = %q", errOut)
	}
}
