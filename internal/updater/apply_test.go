package updater

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installedAt puts a stand-in binary where an update would replace it.
func installedAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, ExecutableName())
	if err := os.WriteFile(path, []byte("the old binary"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// applierFor builds an applier against a fake release and a fake installation.
func applierFor(t *testing.T, release *fakeRelease, channel string) (*Applier, *Store, *bytes.Buffer, string) {
	t.Helper()
	dataDir := t.TempDir()
	store := NewStore(dataDir)
	installPath := installedAt(t, filepath.Join(t.TempDir(), "bin"))
	out := &bytes.Buffer{}

	applier := &Applier{
		Store:     store,
		Updates:   updatesConfig(),
		Sources:   release.sources(),
		Install:   Install{Channel: channel, Path: installPath, Resolved: installPath, Writable: true},
		Installed: "0.1.0",
		DataDir:   dataDir,
		Out:       out,
		Fetcher:   &Fetcher{Sources: release.sources(), PublicKey: release.PublicKey, SanityCheck: acceptAnyBinary},
		Now:       func() time.Time { return at(t, "2026-08-16T12:00:00Z") },
		// No daemon answers in these tests; the wait is what is being skipped,
		// not what is being tested. The end-to-end suite covers a real one.
		VerifyWait: 50 * time.Millisecond,
	}
	return applier, store, out, installPath
}

func TestThePlanSaysWhatWillHappenBeforeItHappens(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, _, out, installPath := applierFor(t, release, ChannelScript)

	plan, err := applier.BuildPlan("0.2.0")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	applier.PrintPlan(plan)

	if plan.Target != "0.2.0" || plan.Installed != "0.1.0" || plan.Path != installPath {
		t.Errorf("plan = %+v", plan)
	}
	if plan.Downgrade {
		t.Error("0.2.0 over 0.1.0 is not a downgrade")
	}
	if len(plan.Actions) == 0 {
		t.Fatal("a plan with no actions tells the user nothing")
	}

	text := out.String()
	for _, want := range []string{"Update plan", "installed", "0.1.0", "target", "0.2.0", installPath, "checksums"} {
		if !strings.Contains(text, want) {
			t.Errorf("the printed plan is missing %q:\n%s", want, text)
		}
	}
}

func TestAPlanForAnOlderVersionIsMarkedADowngrade(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, _, _, _ := applierFor(t, release, ChannelScript)

	plan, err := applier.BuildPlan("0.0.9")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !plan.Downgrade {
		t.Error("0.0.9 under 0.1.0 must be marked as a downgrade")
	}
}

func TestAPlanWithoutATargetUsesWhatTheCheckFound(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, store, _, _ := applierFor(t, release, ChannelScript)

	if _, err := applier.BuildPlan(""); err == nil {
		t.Fatal("without any release information there is nothing to plan")
	}

	if _, err := store.Mutate(func(s *UpdateState) {
		s.LatestKnown = knownLatest("0.2.0", at(t, "2026-08-16T12:00:00Z"))
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	plan, err := applier.BuildPlan("")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Target != "0.2.0" {
		t.Errorf("target = %q, want the known latest", plan.Target)
	}
}

func TestAnUpdateInstallsAndRecordsItself(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, store, out, installPath := applierFor(t, release, ChannelScript)

	restarted := false
	applier.Restart = func(context.Context) error {
		restarted = true
		return nil
	}
	// Verification would need a daemon; the restart itself is what this test is
	// about, so the daemon check is covered by the end-to-end suite.
	applier.DataDir = t.TempDir()

	plan, err := applier.BuildPlan("0.2.0")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	applyErr := applier.Apply(context.Background(), plan)

	// The binary is in place whether or not the daemon came back.
	content, readErr := os.ReadFile(installPath)
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if string(content) == "the old binary" {
		t.Fatalf("the binary was not replaced (apply said: %v)\n%s", applyErr, out.String())
	}
	if !restarted {
		t.Error("the daemon must be restarted onto the new binary")
	}

	state := store.LoadOrZero()
	if state.UpdateInProgress != nil {
		t.Error("the in-progress marker must be cleared when the update ends")
	}
	if state.LastUpdate == nil || state.LastUpdate.To != "0.2.0" {
		t.Errorf("last_update = %+v", state.LastUpdate)
	}

	events := eventNames(store.Tail(10))
	if !contains(events, EventUpdateStarted) {
		t.Errorf("history = %v, want an update_started entry", events)
	}
}

func TestAFailedDownloadChangesNothing(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	release.CorruptArchive(t)
	applier, store, _, installPath := applierFor(t, release, ChannelScript)
	applier.Restart = func(context.Context) error {
		t.Error("nothing may be restarted when nothing was installed")
		return nil
	}

	plan, err := applier.BuildPlan("0.2.0")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	err = applier.Apply(context.Background(), plan)

	if ExitCodeFor(err) != ExitChecksum {
		t.Fatalf("err = %v (exit %d), want a checksum failure", err, ExitCodeFor(err))
	}
	content, readErr := os.ReadFile(installPath)
	if readErr != nil || string(content) != "the old binary" {
		t.Errorf("the installed binary must be untouched, got %q (%v)", content, readErr)
	}

	state := store.LoadOrZero()
	if state.UpdateInProgress != nil {
		t.Error("a failed update must clear its in-progress marker too")
	}
	if state.LastUpdate == nil || state.LastUpdate.Result != UpdateResultFailed {
		t.Errorf("last_update = %+v, want a recorded failure", state.LastUpdate)
	}
	if !contains(eventNames(store.Tail(10)), EventUpdateFailed) {
		t.Error("the failure must be in the history")
	}
}

func TestAReadOnlyInstallLocationIsRefusedBeforeDownloading(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, _, out, _ := applierFor(t, release, ChannelScript)
	applier.Install.Writable = false

	plan, err := applier.BuildPlan("0.2.0")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	err = applier.Apply(context.Background(), plan)

	if ExitCodeFor(err) != ExitNotWritable {
		t.Fatalf("err = %v (exit %d), want %d", err, ExitCodeFor(err), ExitNotWritable)
	}
	if strings.Contains(out.String(), "Downloading") {
		t.Error("nothing should be downloaded into a location that cannot be written")
	}
}

func TestAPackageManagerInstallIsDelegatedNotOverwritten(t *testing.T) {
	// SC-006. Writing over a file brew owns corrupts its bookkeeping, and the
	// next `brew upgrade` silently reverts us.
	release := publishRelease(t, "0.2.0")
	applier, _, out, installPath := applierFor(t, release, ChannelHomebrew)

	var ran []string
	applier.Run = func(_ context.Context, name string, args ...string) error {
		ran = append([]string{name}, args...)
		return nil
	}
	applier.Restart = func(context.Context) error { return nil }
	applier.DataDir = t.TempDir()

	plan, err := applier.BuildPlan("0.2.0")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Delegate) == 0 {
		t.Fatal("a homebrew install must plan to delegate")
	}
	_ = applier.Apply(context.Background(), plan)

	if strings.Join(ran, " ") != "brew upgrade Vadym903/tap/intenter" {
		t.Errorf("ran %v, want brew upgrade", ran)
	}
	if content, err := os.ReadFile(installPath); err != nil || string(content) != "the old binary" {
		t.Errorf("brew's file must not be touched, got %q (%v)", content, err)
	}
	if strings.Contains(out.String(), "Downloading") {
		t.Error("a delegated update must not download an archive itself")
	}
}

func TestAWingetInstallDelegatesToWinget(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, _, _, _ := applierFor(t, release, ChannelWinget)

	plan, err := applier.BuildPlan("0.2.0")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if strings.Join(plan.Delegate, " ") != "winget upgrade --id Intenter.Intenter --exact" {
		t.Errorf("delegate = %v", plan.Delegate)
	}
}

func TestAFailedDelegationIsReportedAsSuch(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, _, _, _ := applierFor(t, release, ChannelHomebrew)
	applier.Run = func(context.Context, string, ...string) error {
		return errors.New("Error: No available formula")
	}

	plan, _ := applier.BuildPlan("0.2.0")
	err := applier.Apply(context.Background(), plan)

	if ExitCodeFor(err) != ExitDelegation {
		t.Fatalf("err = %v (exit %d), want %d", err, ExitCodeFor(err), ExitDelegation)
	}
	if !strings.Contains(err.Error(), "brew upgrade") {
		t.Errorf("the error must name the command that failed:\n%v", err)
	}
}

func TestASecondUpdateIsTurnedAway(t *testing.T) {
	// Two terminals answering "yes" at the same moment must not race for the
	// same file.
	release := publishRelease(t, "0.2.0")
	applier, store, _, _ := applierFor(t, release, ChannelScript)

	held, err := AcquirePromptLock(store.PromptLockPath())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer held.Release()

	plan, _ := applier.BuildPlan("0.2.0")
	err = applier.Apply(context.Background(), plan)

	if ExitCodeFor(err) != ExitInProgress {
		t.Fatalf("err = %v (exit %d), want %d", err, ExitCodeFor(err), ExitInProgress)
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("the error must say what is happening:\n%v", err)
	}
}

func TestTheCallersOwnLockIsNotTakenTwice(t *testing.T) {
	// The start-up path holds the prompt lock from before the prompt; acquiring
	// it again in the same process would be a deadlock against ourselves.
	release := publishRelease(t, "0.2.0")
	applier, store, _, _ := applierFor(t, release, ChannelScript)
	applier.LockHeld = true
	applier.Restart = func(context.Context) error { return nil }
	applier.DataDir = t.TempDir()

	held, err := AcquirePromptLock(store.PromptLockPath())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer held.Release()

	plan, _ := applier.BuildPlan("0.2.0")
	if err := applier.Apply(context.Background(), plan); ExitCodeFor(err) == ExitInProgress {
		t.Fatal("the caller's own lock must not turn its own update away")
	}
}

func TestWithoutARestartTheUserIsToldToDoIt(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, _, out, _ := applierFor(t, release, ChannelScript)
	applier.Restart = nil

	plan, _ := applier.BuildPlan("0.2.0")
	if err := applier.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(out.String(), "intenter daemon restart") {
		t.Errorf("a skipped restart must be said out loud:\n%s", out.String())
	}
}

func TestAFailedRestartSaysTheBinaryIsAlreadyInstalled(t *testing.T) {
	// Exit 6 is its own code precisely because the state after it is different:
	// the new binary is in place, and only the follow-up steps are missing.
	release := publishRelease(t, "0.2.0")
	applier, _, _, installPath := applierFor(t, release, ChannelScript)
	applier.Restart = func(context.Context) error { return errors.New("launchctl: no such service") }

	plan, _ := applier.BuildPlan("0.2.0")
	err := applier.Apply(context.Background(), plan)

	if ExitCodeFor(err) != ExitPostUpdate {
		t.Fatalf("err = %v (exit %d), want %d", err, ExitCodeFor(err), ExitPostUpdate)
	}
	for _, want := range []string{"is installed", "intenter daemon restart", "intenter doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is missing %q:\n%v", want, err)
		}
	}
	if content, _ := os.ReadFile(installPath); string(content) == "the old binary" {
		t.Error("the new binary should already be installed at this point")
	}
}

func TestAnUnparsableTargetIsRefused(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, _, _, _ := applierFor(t, release, ChannelScript)

	if _, err := applier.BuildPlan("latest"); ExitCodeFor(err) != ExitUsage {
		t.Errorf("err = %v (exit %d), want a usage failure", err, ExitCodeFor(err))
	}
}

func eventNames(entries []UpdateDecision) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Event)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
