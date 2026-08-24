package install

import "testing"

// Pinning a version is how someone bisects a regression, and how support says
// "please try 0.1.0". It has to work in both directions and say honestly which
// one happened: a summary line reading "upgraded" after a deliberate rollback
// would leave the user believing they are on the newest release.

func TestPinnedInstallThenUpgradeReportsBothSteps(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeOlder))

	first := env.run("--version", fakeOlder)
	if first.ExitCode != 0 {
		t.Fatalf("pinned install: %d\n%s", first.ExitCode, first.Output())
	}
	if version, err := env.installedVersion(); err != nil || version != fakeOlder {
		t.Fatalf("installed %q (%v), want %s", version, err, fakeOlder)
	}
	refuteLine(t, first.Output(), "upgraded from")

	// A second release, newer, at the same server.
	env.release = newRelease(t, fakeLatest)
	second := env.run()
	if second.ExitCode != 0 {
		t.Fatalf("upgrade: %d\n%s", second.ExitCode, second.Output())
	}
	requireLine(t, second.Output(), "upgraded from "+fakeOlder)
	if version, err := env.installedVersion(); err != nil || version != fakeLatest {
		t.Fatalf("installed %q (%v), want %s", version, err, fakeLatest)
	}
}

func TestDowngradeIsAllowedAndNamedHonestly(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	if got := env.run(); got.ExitCode != 0 {
		t.Fatalf("install: %d\n%s", got.ExitCode, got.Output())
	}

	env.release = newRelease(t, fakeOlder)
	got := env.run("--version", fakeOlder)
	if got.ExitCode != 0 {
		t.Fatalf("downgrade: %d\n%s", got.ExitCode, got.Output())
	}

	requireLine(t, got.Output(), "downgraded from "+fakeLatest)
	refuteLine(t, got.Output(), "upgraded from")
	if version, err := env.installedVersion(); err != nil || version != fakeOlder {
		t.Errorf("installed %q (%v), want the pinned %s", version, err, fakeOlder)
	}
}

func TestAVersionThatDoesNotExistFailsCleanly(t *testing.T) {
	// A typo in a pinned version must not leave a half-installed machine.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	for _, version := range []string{"0.0.0", "not-a-version", "1.2"} {
		got := env.run("--version", version)
		if got.ExitCode != 2 {
			t.Errorf("--version %q: exit code = %d, want 2\n%s", version, got.ExitCode, got.Output())
		}
		if exists(env.installedBinary()) {
			t.Errorf("--version %q installed something", version)
		}
	}
}

func TestChangeVerbHandlesEveryOrdering(t *testing.T) {
	// The comparison the summary line depends on, exercised directly through
	// the script so the shell arithmetic is what is tested.
	skipOnWindows(t)

	tests := []struct {
		from, to, want string
	}{
		{"0.1.0", "0.2.0", "upgraded"},
		{"0.2.0", "0.1.0", "downgraded"},
		{"0.1.0", "0.1.1", "upgraded"},
		{"0.1.1", "0.1.0", "downgraded"},
		{"0.9.0", "1.0.0", "upgraded"},
		{"1.0.0", "0.9.0", "downgraded"},
		{"1.2.3", "1.2.3", "upgraded"},
		{"0.2.0-rc.1", "0.2.0", "upgraded"},
		{"0.10.0", "0.9.0", "downgraded"},
		{"0.9.0", "0.10.0", "upgraded"},
	}

	for _, tc := range tests {
		got := runShellFunction(t, "change_verb", tc.from, tc.to)
		if got != tc.want {
			t.Errorf("change_verb %s → %s = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
}
