package updater

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
)

// A self-updating security tool has to be at least as trustworthy as the
// installer it replaces. These are the properties that make it so: the right
// channel, never backwards, never someone else's file, and never without
// saying what it is about to do.

func TestAStableInstallationIsNeverOfferedAPrerelease(t *testing.T) {
	// SC: a pre-release is a build the maintainers are not yet standing
	// behind. Nobody gets one by accident.
	server, log := releaseHost(t, "v0.3.0-rc.1")
	store := newStore(t)

	// The stable channel reads the redirect, which by construction points at
	// the newest *stable* release — so a repository whose newest tag is a
	// pre-release simply never offers it.
	stable := updatesConfig()
	checker := checkerFor(t, store, server.URL, stable)
	if _, err := checker.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if paths := pathsOf(log.all()); len(paths) != 1 || paths[0] != "/releases/latest" {
		t.Errorf("the stable channel used %v, want the redirect only", paths)
	}

	// Even if a pre-release does end up in the state — a channel change, a
	// hand-edited file — eligibility refuses it.
	now := at(t, "2026-08-16T12:00:00Z")
	state := UpdateState{LatestKnown: knownLatest("0.3.0-rc.1", now)}
	if state.Eligible(now, "0.2.0", stable) {
		t.Error("a stable installation must not be prompted about a pre-release")
	}
}

func TestOptingIntoPrereleasesUsesTheFeed(t *testing.T) {
	server, log := releaseHost(t, "v0.3.0-rc.1")
	cfg := updatesConfig()
	cfg.Channel = config.ChannelPrerelease

	state, err := checkerFor(t, newStore(t), server.URL, cfg).Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if state.LatestKnown == nil || state.LatestKnown.Version != "0.3.0-rc.1" {
		t.Fatalf("latest_known = %+v", state.LatestKnown)
	}
	if paths := pathsOf(log.all()); len(paths) != 1 || paths[0] != "/releases.atom" {
		t.Errorf("the pre-release channel used %v, want the feed", paths)
	}

	now := at(t, "2026-08-16T12:00:00Z")
	if !state.Eligible(now, "0.2.0", cfg) {
		t.Error("someone who opted in must be offered it")
	}
}

func TestSomeoneAlreadyOnAPrereleaseFollowsThem(t *testing.T) {
	// They are already running one; the next one is not a surprise.
	now := at(t, "2026-08-16T12:00:00Z")
	state := UpdateState{LatestKnown: knownLatest("0.3.0-rc.2", now)}

	if !state.Eligible(now, "0.3.0-rc.1", updatesConfig()) {
		t.Error("a pre-release installation must be offered the next pre-release")
	}
}

func TestAnUpdateIsNeverADowngradeByAccident(t *testing.T) {
	now := at(t, "2026-08-16T12:00:00Z")
	state := UpdateState{LatestKnown: knownLatest("0.1.0", now)}

	// A yanked release, or a developer build newer than anything published.
	if state.Eligible(now, "0.2.0", updatesConfig()) {
		t.Error("an older release must never be offered")
	}
	if state.Eligible(now, "0.2.0-dev", updatesConfig()) {
		t.Error("a development build newer than the latest must not be downgraded")
	}
}

func TestAnExplicitOlderVersionIsMarkedAsADowngrade(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	applier, _, out, _ := applierFor(t, release, ChannelScript)

	plan, err := applier.BuildPlan("0.0.1")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !plan.Downgrade {
		t.Fatal("0.0.1 under 0.1.0 must be marked a downgrade")
	}

	applier.PrintPlan(plan)
	if !strings.Contains(out.String(), "older") {
		t.Errorf("the plan must say it is going backwards:\n%s", out.String())
	}
}

func TestAnUnknownInstallationIsNeverUpdatedUnattended(t *testing.T) {
	// A copy this tool cannot place may be one something else manages;
	// replacing it would be somebody else's outage.
	release := publishRelease(t, "0.2.0")
	applier, _, _, _ := applierFor(t, release, ChannelUnknown)

	plan, err := applier.BuildPlan("0.2.0")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Channel != ChannelUnknown {
		t.Errorf("channel = %q", plan.Channel)
	}
	install := Install{Channel: ChannelUnknown}
	if install.SelfManaged() || install.PackageManaged() {
		t.Error("an unknown installation is neither self-managed nor package-managed")
	}
}

func TestAShadowingCopyOnPathIsReported(t *testing.T) {
	// Two installations is a common accident — Homebrew plus `curl | sh`. An
	// update to the one that is not on PATH looks, to the user, like an update
	// that did nothing.
	if runtime.GOOS == "windows" {
		t.Skip("PATHEXT lookup differs; the unit is the same")
	}

	first := t.TempDir()
	second := t.TempDir()
	for _, dir := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dir, ExecutableName()), []byte("binary"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)

	if got := Shadowing(filepath.Join(second, ExecutableName())); got != filepath.Join(first, ExecutableName()) {
		t.Errorf("Shadowing = %q, want the earlier PATH entry", got)
	}
	if got := Shadowing(filepath.Join(first, ExecutableName())); got != "" {
		t.Errorf("Shadowing = %q, want empty for the copy PATH already finds", got)
	}
	if found := OnPath(); len(found) != 2 {
		t.Errorf("OnPath = %v, want both copies", found)
	}
}

func TestASymlinkedCopyIsNotASecondInstallation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	real := t.TempDir()
	link := t.TempDir()

	target := filepath.Join(real, ExecutableName())
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(link, ExecutableName())); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv("PATH", link+string(os.PathListSeparator)+real)

	if found := OnPath(); len(found) != 1 {
		t.Errorf("OnPath = %v, want one installation reached two ways", found)
	}
	if got := Shadowing(target); got != "" {
		t.Errorf("Shadowing = %q — a symlink to the same file is not a rival copy", got)
	}
}

func TestThePlanIsPrintedBeforeAnythingHappens(t *testing.T) {
	// FR-016: the user can always see what will happen before it does.
	release := publishRelease(t, "0.2.0")
	applier, store, out, installPath := applierFor(t, release, ChannelScript)

	plan, err := applier.BuildPlan("0.2.0")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	applier.PrintPlan(plan)

	text := out.String()
	for _, want := range []string{"0.1.0", "0.2.0", ChannelScript, installPath, AssetName("0.2.0")} {
		if !strings.Contains(text, want) {
			t.Errorf("the plan does not mention %q:\n%s", want, text)
		}
	}
	if content, _ := os.ReadFile(installPath); string(content) != "the old binary" {
		t.Error("printing a plan must change nothing")
	}
	if store.LoadOrZero().LastUpdate != nil {
		t.Error("printing a plan must record no update")
	}
}

func TestTheCheckRecordsWhenItRanAndWhatItFound(t *testing.T) {
	// FR-020: support has to be able to answer "why was I not told".
	server, _ := releaseHost(t, "v0.2.0")
	store := newStore(t)
	now := at(t, "2026-08-16T12:00:00Z")

	if _, err := checkerFor(t, store, server.URL, updatesConfig()).Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	state := store.LoadOrZero()
	if state.LastCheckAt == nil || !state.LastCheckAt.Equal(now) {
		t.Errorf("last_check_at = %v, want %v", state.LastCheckAt, now)
	}
	if state.NextCheckAfter == nil || !state.NextCheckAfter.Equal(now.Add(24*time.Hour)) {
		t.Errorf("next_check_after = %v", state.NextCheckAfter)
	}
	entries := store.Tail(10)
	if len(entries) != 1 || entries[0].Event != EventCheckOK || entries[0].TargetVersion != "0.2.0" {
		t.Errorf("history = %+v", entries)
	}
}
