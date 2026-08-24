package platform

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ServiceState is what a per-user service manager reports.
type ServiceState string

const (
	// ServiceNotInstalled means no service entry exists.
	ServiceNotInstalled ServiceState = "not_installed"
	// ServiceStopped means the entry exists but nothing is running.
	ServiceStopped ServiceState = "stopped"
	// ServiceRunning means the daemon is registered and alive.
	ServiceRunning ServiceState = "running"
	// ServiceUnsupported means this machine has no usable per-user service
	// manager, so the daemon runs unmanaged.
	ServiceUnsupported ServiceState = "unsupported"
)

// ServiceMode names how the daemon is kept running, for `status` and `doctor`.
const (
	ModeManaged   = "managed"
	ModeUnmanaged = "unmanaged"
)

// ServiceManager registers the daemon with the platform's per-user service
// system (§9.4). Every implementation runs entirely in the user's own session:
// Intenter never needs elevation, because it only ever gates that user's
// agent.
type ServiceManager interface {
	// Name identifies the mechanism, e.g. "launchd" or "systemd --user".
	Name() string
	// Available reports whether this machine can manage a service at all.
	Available(ctx context.Context) bool
	// Install registers the daemon to start with the user's session.
	Install(ctx context.Context, executable string) error
	// Uninstall removes the registration, stopping the service first.
	Uninstall(ctx context.Context) error
	// Start starts the registered service.
	Start(ctx context.Context) error
	// Stop stops it, leaving the registration in place.
	Stop(ctx context.Context) error
	// Status reports the current state.
	Status(ctx context.Context) (ServiceState, error)
}

// ServiceInspector is implemented by managers that can report which executable
// their registration actually points at.
//
// A service definition is written once and read at every login, so it outlives
// the binary that created it. `doctor` uses this to notice a registration left
// pointing at a path an upgrade moved or removed.
type ServiceInspector interface {
	// RegisteredExecutable returns the path in the definition, and whether a
	// definition was found and could be read.
	RegisteredExecutable() (string, bool)
}

// RegisteredExecutable asks a manager what its registration points at, for
// managers that can answer.
func RegisteredExecutable(manager ServiceManager) (string, bool) {
	inspector, ok := manager.(ServiceInspector)
	if !ok {
		return "", false
	}
	return inspector.RegisteredExecutable()
}

// CommandRunner executes an external command. It is injectable so service
// managers can be tested without touching the machine's real service system.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// execRunner runs a command for real.
func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w: %s",
			name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

// NewServiceManager returns the manager for this platform. When the platform
// has no usable per-user service system the unmanaged fallback is returned,
// which is a valid mode rather than a failure: the hook's lazy start covers it
// (§9.5, FR-022).
func NewServiceManager(p Platform) ServiceManager {
	return newServiceManager(p, execRunner)
}

// newServiceManagerWithRunner builds the platform's manager with an injected
// runner, for tests.
func newServiceManagerWithRunner(p Platform, runner CommandRunner) ServiceManager {
	return newServiceManager(p, runner)
}

// unmanagedService is the fallback for a machine with no per-user service
// system. It registers nothing and reports honestly, so `status` and `doctor`
// can tell the user the daemon is theirs to start.
type unmanagedService struct{}

// NewUnmanagedService returns the do-nothing manager.
func NewUnmanagedService() ServiceManager { return unmanagedService{} }

func (unmanagedService) Name() string                   { return ModeUnmanaged }
func (unmanagedService) Available(context.Context) bool { return false }
func (unmanagedService) Install(context.Context, string) error {
	return fmt.Errorf("platform: no per-user service manager is available on this machine")
}
func (unmanagedService) Uninstall(context.Context) error { return nil }
func (unmanagedService) Start(context.Context) error {
	return fmt.Errorf("platform: no per-user service manager is available on this machine")
}
func (unmanagedService) Stop(context.Context) error { return nil }
func (unmanagedService) Status(context.Context) (ServiceState, error) {
	return ServiceUnsupported, nil
}

// ServiceLabel is the identifier every platform registers the daemon under.
const ServiceLabel = "com.intenter.daemon"
