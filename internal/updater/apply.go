package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/platform"
)

// pingWait is how long the updater waits for the restarted daemon to answer
// with the new version. A daemon that has not come back within this has a
// problem the user needs to know about, not one more second of patience.
const pingWait = 5 * time.Second

// Plan is what an update would do, in the order it would do it. It is printed
// before anything happens — and on its own by `update --plan` — because a tool
// that replaces its own executable should say so first.
type Plan struct {
	Installed string `json:"installed"`
	Target    string `json:"target"`
	// Channel is how this copy was installed, which decides the actions.
	Channel string `json:"channel"`
	// Path is the executable that would be replaced.
	Path     string `json:"path"`
	NotesURL string `json:"notes_url,omitempty"`
	// Asset is the release archive that would be downloaded, when the update
	// is performed by Intenter itself.
	Asset string `json:"asset,omitempty"`
	// Delegate is the package manager's upgrade command, when it is not.
	Delegate []string `json:"delegate,omitempty"`
	// Downgrade marks a target older than what is installed.
	Downgrade bool `json:"downgrade,omitempty"`
	// Shadowing is another copy on PATH that would run instead of the one being
	// replaced. Updating the copy a user never types is a silent no-op from
	// their point of view, so it is said out loud.
	Shadowing string   `json:"shadowing,omitempty"`
	Actions   []string `json:"actions"`
}

// Applier performs one update.
type Applier struct {
	Store   *Store
	Updates config.UpdatesConfig
	Sources Sources
	// Install describes the copy being replaced.
	Install Install
	// Installed is the running version.
	Installed string
	// DataDir is where downloads are staged and the daemon is discovered.
	DataDir string
	// Endpoint overrides daemon discovery; empty uses the platform default.
	Endpoint string
	Out      io.Writer

	// Services is the platform's service manager, used to verify that the
	// registration still points at an executable that exists.
	Services platform.ServiceManager
	// Restart brings the daemon back on the new binary. It is injected by the
	// CLI rather than implemented here: starting a daemon managed or unmanaged
	// is already `intenter daemon restart`, and two implementations of it
	// would be one too many. A nil Restart reports the step as the user's to
	// perform instead of silently skipping it.
	Restart func(context.Context) error
	// Run executes a package manager's upgrade command with inherited stdio.
	Run func(ctx context.Context, name string, args ...string) error
	// Fetcher downloads and verifies; nil builds the default.
	Fetcher *Fetcher
	// LockHeld says the caller already holds the prompt lock — the start-up
	// path does, because it took it to show the prompt that led here. The lock
	// is per open file, so acquiring it a second time in the same process would
	// deadlock against ourselves.
	LockHeld bool
	// VerifyWait is how long to wait for the restarted daemon; zero means
	// pingWait.
	VerifyWait time.Duration
	// Now is injectable so recorded times can be asserted.
	Now func() time.Time
}

func (a *Applier) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return Now()
}

// BuildPlan describes what updating to target would do. An empty target means
// the newest known version.
func (a *Applier) BuildPlan(target string) (Plan, error) {
	if strings.TrimSpace(target) == "" {
		state := a.Store.LoadOrZero()
		if state.LatestKnown == nil {
			return Plan{}, failf(ExitUsage,
				"updater: no release information yet — run `intenter update --check` first")
		}
		target = state.LatestKnown.Version
	}

	version, err := ParseVersion(target)
	if err != nil {
		return Plan{}, &Failure{Code: ExitUsage, Err: err}
	}

	plan := Plan{
		Installed: a.Installed,
		Target:    version,
		Channel:   a.Install.Channel,
		Path:      a.Install.Path,
		NotesURL:  a.Sources.NotesURL(version),
		Downgrade: Older(version, a.Installed),
		Shadowing: Shadowing(a.Install.Path),
	}

	switch a.Install.Channel {
	case ChannelHomebrew, ChannelWinget:
		plan.Delegate = delegateCommand(a.Install.Channel)
		plan.Actions = []string{
			fmt.Sprintf("run %q (%s owns %s)", strings.Join(plan.Delegate, " "), a.Install.Channel, plan.Path),
			"restart the Intenter daemon",
			"verify the integration",
		}
	default:
		plan.Asset = AssetName(version)
		plan.Actions = []string{
			fmt.Sprintf("download %s and verify it against the published checksums", plan.Asset),
			fmt.Sprintf("replace %s", plan.Path),
			"restart the Intenter daemon",
			"verify the integration",
		}
	}
	return plan, nil
}

// delegateCommand is the package manager's own upgrade command (003 R-07).
// Overwriting a file a package manager owns corrupts its bookkeeping and gets
// silently reverted by the next upgrade, so this is the only correct way.
func delegateCommand(channel string) []string {
	switch channel {
	case ChannelHomebrew:
		return []string{"brew", "upgrade", "Vadym903/tap/intenter"}
	case ChannelWinget:
		return []string{"winget", "upgrade", "--id", "Intenter.Intenter", "--exact"}
	default:
		return nil
	}
}

// PrintPlan writes a plan in the form both `--plan` and the confirmation use.
func (a *Applier) PrintPlan(plan Plan) {
	fmt.Fprintf(a.Out, "Update plan\n")
	fmt.Fprintf(a.Out, "  installed  %s\n", orNone(plan.Installed))
	fmt.Fprintf(a.Out, "  target     %s", plan.Target)
	if plan.Downgrade {
		fmt.Fprintf(a.Out, "  (older than what is installed)")
	}
	fmt.Fprintln(a.Out)
	fmt.Fprintf(a.Out, "  channel    %s\n", plan.Channel)
	fmt.Fprintf(a.Out, "  path       %s\n", orNone(plan.Path))
	if plan.NotesURL != "" {
		fmt.Fprintf(a.Out, "  notes      %s\n", plan.NotesURL)
	}
	fmt.Fprintf(a.Out, "  actions\n")
	for _, action := range plan.Actions {
		fmt.Fprintf(a.Out, "    - %s\n", action)
	}
	if plan.Shadowing != "" {
		fmt.Fprintf(a.Out, "\n! another Intenter is earlier on your PATH and would run instead:\n")
		fmt.Fprintf(a.Out, "    %s\n", plan.Shadowing)
		fmt.Fprintf(a.Out, "  this update replaces %s; remove one of them to avoid confusion\n", plan.Path)
	}
}

// Apply performs the plan. It holds the prompt lock for the whole update, so a
// second terminal answering "yes" at the same moment is told to wait rather
// than racing this one for the same file.
func (a *Applier) Apply(ctx context.Context, plan Plan) (err error) {
	if !a.LockHeld {
		lock, lockErr := AcquirePromptLock(a.Store.PromptLockPath())
		if lockErr != nil {
			if errors.Is(lockErr, ErrLockHeld) {
				return failf(ExitInProgress, "%s", inProgressMessage(a.Store.PromptLockPath()))
			}
			return &Failure{Code: ExitUsage, Err: lockErr}
		}
		defer lock.Release()
	}

	if err := a.markInProgress(plan.Target); err != nil {
		return err
	}
	defer func() { a.finish(plan, err) }()

	a.record(EventUpdateStarted, plan.Target, "")

	if len(plan.Delegate) > 0 {
		if err := a.delegate(ctx, plan); err != nil {
			return err
		}
	} else if err := a.replaceSelf(ctx, plan); err != nil {
		return err
	}

	return a.afterReplacement(ctx, plan)
}

// replaceSelf downloads, verifies and installs the new binary.
//
// Everything before the rename happens in a directory that is deleted whatever
// happens, so a failure at any point up to the swap leaves the installed copy
// byte-for-byte as it was.
func (a *Applier) replaceSelf(ctx context.Context, plan Plan) error {
	if plan.Path == "" {
		return failf(ExitNotWritable, "updater: cannot tell where Intenter is installed")
	}
	if !a.Install.Writable {
		return failf(ExitNotWritable,
			"updater: %s is not writable\n"+
				"install the new version another way, or re-run the installer with a writable location",
			filepath.Dir(plan.Path))
	}

	staging, err := os.MkdirTemp(a.Store.TempDir(), "update-")
	if err != nil {
		if mkErr := os.MkdirAll(a.Store.TempDir(), 0o700); mkErr != nil {
			return failf(ExitDownload, "updater: create %s: %w", a.Store.TempDir(), mkErr)
		}
		if staging, err = os.MkdirTemp(a.Store.TempDir(), "update-"); err != nil {
			return failf(ExitDownload, "updater: stage the download: %w", err)
		}
	}
	defer os.RemoveAll(staging)

	fetcher := a.Fetcher
	if fetcher == nil {
		fetcher = &Fetcher{Sources: a.Sources}
	}
	fmt.Fprintf(a.Out, "Downloading %s …\n", plan.Asset)
	binary, err := fetcher.Fetch(ctx, plan.Target, staging)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Signature verified.\n")

	fmt.Fprintf(a.Out, "Installing to %s …\n", plan.Path)
	return Replace(binary, plan.Path)
}

// delegate hands the update to the package manager that owns the file.
func (a *Applier) delegate(ctx context.Context, plan Plan) error {
	if a.Run == nil {
		return failf(ExitDelegation,
			"updater: %s installed Intenter; update it with:\n  %s",
			plan.Channel, strings.Join(plan.Delegate, " "))
	}
	fmt.Fprintf(a.Out, "Running %s …\n", strings.Join(plan.Delegate, " "))
	if err := a.Run(ctx, plan.Delegate[0], plan.Delegate[1:]...); err != nil {
		return failf(ExitDelegation, "updater: %s failed: %w\nrun it yourself to see why",
			strings.Join(plan.Delegate, " "), err)
	}
	return nil
}

// afterReplacement restarts the daemon and confirms the new build is the one
// answering. Everything here happens with the new binary already installed, so
// a failure is reported as "finish this yourself" rather than as an update that
// did not happen.
func (a *Applier) afterReplacement(ctx context.Context, plan Plan) error {
	if a.Restart == nil {
		fmt.Fprintf(a.Out, "\nIntenter %s is installed.\n", plan.Target)
		fmt.Fprintf(a.Out, "Restart the daemon to run it: intenter daemon restart\n")
		return nil
	}

	fmt.Fprintf(a.Out, "Restarting the daemon …\n")
	if err := a.Restart(ctx); err != nil {
		return failf(ExitPostUpdate,
			"updater: Intenter %s is installed, but the daemon did not restart: %w\n"+
				"finish with: intenter daemon restart && intenter doctor",
			plan.Target, err)
	}

	if err := a.verify(ctx, plan); err != nil {
		return err
	}

	fmt.Fprintf(a.Out, "\nUpdated %s → %s\n", orNone(plan.Installed), plan.Target)
	if plan.NotesURL != "" {
		fmt.Fprintf(a.Out, "Release notes: %s\n", plan.NotesURL)
	}
	return nil
}

// verify confirms the two things this package can know about on its own: the
// daemon answering is the new build, and the service registration still names
// an executable that exists. Whether Claude's hooks still resolve is checked
// afterwards by the CLI layer, which is the half that may know about Claude.
func (a *Applier) verify(ctx context.Context, plan Plan) error {
	version, err := a.waitForDaemon(ctx, plan.Target)
	if err != nil {
		return failf(ExitPostUpdate,
			"updater: Intenter %s is installed, but the daemon did not come back: %w\n"+
				"finish with: intenter daemon restart && intenter doctor", plan.Target, err)
	}
	if !Same(version, plan.Target) {
		return failf(ExitPostUpdate,
			"updater: Intenter %s is installed, but the daemon reports %s\n"+
				"finish with: intenter daemon restart && intenter doctor", plan.Target, version)
	}

	if registered, ok := platform.RegisteredExecutable(a.Services); ok && registered != "" {
		if _, statErr := os.Stat(registered); statErr != nil {
			fmt.Fprintf(a.Out,
				"\n! the background service still points at %s, which no longer exists\n"+
					"  fix it with: intenter setup claude\n", registered)
		}
	}
	return nil
}

// waitForDaemon polls until the daemon answers, or the wait runs out.
func (a *Applier) waitForDaemon(ctx context.Context, want string) (string, error) {
	endpoint := ipc.DiscoverEndpoint(os.Getenv(platform.EnvEndpoint), a.DataDir, a.Endpoint)
	client := ipc.NewClient(endpoint)

	wait := a.VerifyWait
	if wait <= 0 {
		wait = pingWait
	}
	deadline := time.Now().Add(wait)
	var lastErr error
	for time.Now().Before(deadline) {
		result, err := client.Ping(ctx)
		if err == nil {
			if Same(result.Version, want) {
				return result.Version, nil
			}
			// The old daemon may still be finishing its shutdown; keep asking
			// until the new one has the socket.
			lastErr = fmt.Errorf("the daemon still reports %s", result.Version)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no answer within %s", wait)
	}
	return "", lastErr
}

// markInProgress records that this process is updating, so other terminals
// stay quiet even if they cannot see the lock.
func (a *Applier) markInProgress(target string) error {
	_, err := a.Store.Mutate(func(s *UpdateState) {
		s.UpdateInProgress = &InProgress{
			PID:           os.Getpid(),
			StartedAt:     a.now().UTC(),
			TargetVersion: target,
		}
	})
	return err
}

// finish clears the in-progress marker and records the outcome, whichever way
// the update went.
func (a *Applier) finish(plan Plan, err error) {
	now := a.now()
	result, detail := UpdateResultOK, ""
	event := EventUpdateOK
	if err != nil {
		result, detail, event = UpdateResultFailed, err.Error(), EventUpdateFailed
	}

	_, _ = a.Store.Mutate(func(s *UpdateState) {
		s.UpdateInProgress = nil
		s.LastUpdate = &LastUpdate{
			From:   plan.Installed,
			To:     plan.Target,
			At:     now.UTC(),
			Result: result,
			Error:  detail,
		}
		if err == nil {
			s.InstalledVersion = plan.Target
			// The version just installed is the one running from now on, so a
			// prompt about it would be nonsense.
			s.DeferredUntil = nil
		}
	})
	a.record(event, plan.Target, detail)
}

func (a *Applier) record(event, target, detail string) {
	_ = a.Store.Append(UpdateDecision{
		At:               a.now(),
		Event:            event,
		InstalledVersion: a.Installed,
		TargetVersion:    target,
		Channel:          a.Install.Channel,
		Detail:           detail,
	})
}

// inProgressMessage names the other terminal where one can be named.
func inProgressMessage(lockPath string) string {
	if owner, ok := PromptLockHolder(lockPath); ok {
		return fmt.Sprintf("updater: an update is already running (pid %d, started %s)",
			owner.PID, owner.StartedAt.Format(time.RFC3339))
	}
	return "updater: an update is already running"
}

func orNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
