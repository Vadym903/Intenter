package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/ipc"
)

// An upgrade replaces the binary while the daemon is still running the previous
// one. Nothing restarts it on its own, so without this the machine keeps being
// gated by the old engine — with old rules and old resolution — while every
// other part of the system reports the new version.
//
// The opposite failure is worse: a daemon that restarts on every request
// because a *different* newer binary exists somewhere else on PATH. That is a
// gate that spends its life starting up, so the restart is conditional on this
// daemon's own file having actually changed.

// installedBinary creates a file to stand in for the daemon's executable.
func installedBinary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "intenter")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// replaceBinary does what an installer does: writes a new file and renames it
// over the old one, so the path names a different inode.
func replaceBinary(t *testing.T, path, content string) {
	t.Helper()
	staged := path + ".new"
	if err := os.WriteFile(staged, []byte(content), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Rename(staged, path); err != nil {
		t.Fatalf("rename: %v", err)
	}
}

func TestANewerClientAfterAnUpgradeAsksForARestart(t *testing.T) {
	path := installedBinary(t, "old")
	watch := newRefreshWatch(path)
	watch.version = "0.1.0"

	if got := watch.decide("0.1.0"); got != refreshNone {
		t.Errorf("an equal version = %v, want no action", got)
	}
	if got := watch.decide("0.2.0"); got != refreshReport {
		t.Errorf("a newer client with an unchanged binary = %v, want a report only", got)
	}

	replaceBinary(t, path, "new")

	if got := watch.decide("0.2.0"); got != refreshRestart {
		t.Errorf("a newer client after the binary was replaced = %v, want a restart", got)
	}
}

func TestAnOlderOrEqualClientChangesNothing(t *testing.T) {
	path := installedBinary(t, "current")
	watch := newRefreshWatch(path)
	watch.version = "0.2.0"
	replaceBinary(t, path, "also new")

	// Even with a replaced binary, only a newer client means anything: the
	// daemon must not restart because an old CLI is still lying around.
	for _, client := range []string{"", "0.1.0", "0.2.0", "0.2.0-rc.1", "nonsense"} {
		if got := watch.decide(client); got != refreshNone {
			t.Errorf("client %q = %v, want no action", client, got)
		}
	}
}

func TestARestartIsNotAskedForTwice(t *testing.T) {
	// The daemon is already shutting down after the first one; asking again
	// would only add noise to the log.
	path := installedBinary(t, "old")
	watch := newRefreshWatch(path)
	watch.version = "0.1.0"
	replaceBinary(t, path, "new")

	if got := watch.decide("0.2.0"); got != refreshRestart {
		t.Fatalf("first decision = %v, want a restart", got)
	}
	// Restart is idempotent at the daemon level (sync.Once); the watch keeps
	// answering the same way, which is what a second in-flight request sees.
	if got := watch.decide("0.2.0"); got != refreshRestart {
		t.Errorf("second decision = %v, want the same answer", got)
	}
}

func TestAnUpgradeIsNoticedWithoutAnEarlierComparison(t *testing.T) {
	// The ordering that happens in real life: the daemon starts, sits idle, and
	// the installer replaces the binary before any request has ever asked
	// whether it changed. Nothing forced the recorded stat to be compared
	// first, which on Windows is what makes its identity readable at all.
	//
	// The neighbouring tests all compare once before replacing, so they cannot
	// catch this — and did not: the daemon shipped unable to notice its own
	// upgrade on Windows.
	path := installedBinary(t, "old")
	watch := newRefreshWatch(path)
	watch.version = "0.1.0"

	replaceBinary(t, path, "new and quite a bit longer")

	if got := watch.decide("0.2.0"); got != refreshRestart {
		t.Errorf("decision = %v, want a restart", got)
	}
}

func TestAnInstallerThatWritesThroughIsStillAnUpgrade(t *testing.T) {
	// `install -m 0755 intenter ~/.local/bin/intenter` — the manual install
	// docs/install.md documents — overwrites in place and keeps the inode, so
	// file identity alone reports nothing happened.
	path := installedBinary(t, "old")
	watch := newRefreshWatch(path)
	watch.version = "0.1.0"

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := os.WriteFile(path, []byte("new content, written through"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// The premise of the test: identity really is unchanged here.
	if !os.SameFile(before, after) {
		t.Skip("this filesystem gave the rewritten file a new identity")
	}
	if !changedFile(before, after) {
		t.Error("a binary rewritten in place must still count as changed")
	}
	if got := watch.decide("0.2.0"); got != refreshRestart {
		t.Errorf("decision = %v, want a restart", got)
	}
}

func TestAnUntouchedBinaryIsNeverAChange(t *testing.T) {
	// The other half of the guard: without this the daemon restarts on every
	// request from a newer client and spends its life starting up.
	path := installedBinary(t, "current")

	first, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	second, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if changedFile(first, second) {
		t.Error("two stats of an untouched file must compare equal")
	}
}

func TestTheUnchangedBinaryWarningIsSaidOnce(t *testing.T) {
	// This is the crash-loop guard, and it is also a log-flood guard: a newer
	// binary installed elsewhere would otherwise warn on every single request.
	path := installedBinary(t, "current")
	watch := newRefreshWatch(path)
	watch.version = "0.1.0"

	if got := watch.decide("0.2.0"); got != refreshReport {
		t.Fatalf("first newer client = %v, want a report", got)
	}
	for i := 0; i < 5; i++ {
		if got := watch.decide("0.2.0"); got != refreshNone {
			t.Errorf("repeat %d = %v, want silence after the first report", i, got)
		}
	}
}

func TestAnUnreadableExecutableNeverRestarts(t *testing.T) {
	// If the daemon cannot see its own binary it cannot know whether restarting
	// would change anything, and a restart into nothing is not an improvement.
	tests := map[string]*refreshWatch{
		"no path": newRefreshWatch(""),
		"a path that is gone": func() *refreshWatch {
			path := installedBinary(t, "old")
			watch := newRefreshWatch(path)
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove: %v", err)
			}
			return watch
		}(),
	}

	for name, watch := range tests {
		t.Run(name, func(t *testing.T) {
			watch.version = "0.1.0"
			if got := watch.decide("0.2.0"); got == refreshRestart {
				t.Error("want no restart when the executable cannot be read")
			}
		})
	}
}

func TestANilWatchIsHarmless(t *testing.T) {
	// The watch is set up at serve time; a request that somehow arrives first
	// must not take the daemon down with it.
	var watch *refreshWatch
	if got := watch.decide("99.0.0"); got != refreshNone {
		t.Errorf("got %v, want no action", got)
	}
}

func TestTheDaemonStopsWithTheRefreshCode(t *testing.T) {
	// The plumbing from "a newer client arrived" to "the process asks to be
	// restarted", on a daemon that is not serving.
	//
	// It deliberately does not drive a running daemon: reaching into one from
	// a test races with the connection goroutines that read the same fields,
	// and a test that has to be raced to pass is testing the test.
	p := testPlatform(t)
	instance, err := newDaemon(p, nil)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}

	if instance.ExitCode() != 0 {
		t.Fatalf("exit code = %d before any refresh, want 0", instance.ExitCode())
	}

	path := installedBinary(t, "old")
	instance.refresh = newRefreshWatch(path)
	instance.refresh.version = "0.1.0"

	// An equal client changes nothing.
	instance.observeClient(&ipc.Request{ClientVersion: "0.1.0"})
	if got := instance.ExitCode(); got != 0 {
		t.Fatalf("exit code = %d after an equal client, want 0", got)
	}

	// Nor does a newer one while the binary on disk is unchanged.
	instance.observeClient(&ipc.Request{ClientVersion: "0.2.0"})
	if got := instance.ExitCode(); got != 0 {
		t.Fatalf("exit code = %d before the binary changed, want 0", got)
	}

	// Once it has been replaced, the daemon asks to be restarted into it.
	replaceBinary(t, path, "new")
	instance.observeClient(&ipc.Request{ClientVersion: "0.2.0"})

	if got := instance.ExitCode(); got != ExitCodeRefresh {
		t.Errorf("exit code = %d, want %d", got, ExitCodeRefresh)
	}
}

func TestAServingDaemonStopsWhenItsBinaryIsReplaced(t *testing.T) {
	// The same thing through the real transport: the refresh watch is
	// installed before serving starts, so nothing is mutated underneath the
	// connection goroutines.
	p := testPlatform(t)
	w := newWorkspace(t, p)

	// Older than the version the real client sends, so an ordinary request
	// looks like one from an upgraded binary.
	path := installedBinary(t, "old")
	client, instance := startDaemonInstanceWith(t, p, func(d *Daemon) {
		d.refresh = newRefreshWatch(path)
		d.refresh.version = "0.0.1"
	})

	// A newer client alone is not enough: the daemon's own binary is unchanged,
	// so restarting would bring back the same code.
	evaluate(t, client, bashRequest(w, "git status", "toolu_1"))
	if got := instance.ExitCode(); got != 0 {
		t.Fatalf("exit code = %d before the binary changed, want 0", got)
	}

	// Now an upgrade replaces it, and the next request notices.
	replaceBinary(t, path, "new")
	evaluate(t, client, bashRequest(w, "git status", "toolu_2"))

	// The request is still answered; the daemon stops afterwards.
	waitFor(t, 5*time.Second, func() bool { return instance.ExitCode() == ExitCodeRefresh })
}

// waitFor polls until a condition holds or the deadline passes.
func waitFor(t *testing.T, within time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the daemon did not ask to be restarted within " + within.String())
}
