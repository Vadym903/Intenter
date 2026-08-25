package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startupCheckFor builds a check against a fake home, on a chosen platform.
func startupCheckFor(t *testing.T, goos string) (*StartupCheck, *Store, string) {
	t.Helper()
	// The fish paths honor XDG_CONFIG_HOME; a value inherited from the
	// machine running the tests would point outside the fake home.
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	store := NewStore(t.TempDir())
	return &StartupCheck{
		Home:       home,
		Executable: filepath.Join(home, ".local", "bin", "intenter"),
		Store:      store,
		GOOS:       goos,
		Now:        func() time.Time { return at(t, "2026-08-16T12:00:00Z") },
	}, store, home
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestTheBlockIsWrittenForEveryShell(t *testing.T) {
	tests := map[string]struct {
		goos  string
		shell string
		file  []string
		// guards are the things that must be in the block, because each one is
		// a case where running would be wrong.
		guards []string
	}{
		"zsh": {
			goos: "linux", shell: ShellZsh, file: []string{".zshrc"},
			guards: []string{"case $- in *i*)", "[ -t 0 ]", "[ -t 1 ]",
				"INTENTER_NO_UPDATE_CHECK", "INTENTER_STARTUP_CHECKED", "update --startup"},
		},
		"bash": {
			goos: "linux", shell: ShellBash, file: []string{".bashrc"},
			guards: []string{"case $- in *i*)", "[ -t 0 ]", "update --startup"},
		},
		"fish": {
			goos: "linux", shell: ShellFish,
			file: []string{".config", "fish", "conf.d", fishBlockFile},
			guards: []string{"status is-interactive", "isatty stdin", "isatty stdout",
				"set -q INTENTER_NO_UPDATE_CHECK", "set -gx INTENTER_STARTUP_CHECKED 1", "update --startup"},
		},
		"powershell": {
			goos: "windows", shell: ShellPowerShell,
			file: []string{"Documents", "WindowsPowerShell", "profile.ps1"},
			guards: []string{"[Environment]::UserInteractive", "$env:INTENTER_NO_UPDATE_CHECK",
				"$env:INTENTER_STARTUP_CHECKED", "Test-Path", "update --startup"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			check, _, home := startupCheckFor(t, test.goos)
			if test.goos != "windows" {
				// A POSIX shell is given a POSIX-spelled path, as it would be
				// on its own OS; a backslash is an escape in fish's quoting and
				// would not survive into the block verbatim.
				check.Executable = filepath.ToSlash(check.Executable)
			}
			if _, err := check.Install([]string{test.shell}); err != nil {
				t.Fatalf("Install: %v", err)
			}

			path := filepath.Join(append([]string{home}, test.file...)...)
			content := readFile(t, path)

			if !strings.HasPrefix(strings.TrimSpace(content), MarkerBegin) {
				t.Errorf("the block must start with its marker:\n%s", content)
			}
			if !strings.Contains(content, MarkerEnd) {
				t.Errorf("the block must end with its marker:\n%s", content)
			}
			for _, guard := range test.guards {
				if !strings.Contains(content, guard) {
					t.Errorf("the block is missing the guard %q:\n%s", guard, content)
				}
			}
			if !strings.Contains(content, check.Executable) {
				t.Errorf("the block must call the stable path %q:\n%s", check.Executable, content)
			}
		})
	}
}

func TestTheBlockDoesNothingButRunTheCheck(t *testing.T) {
	// The installer's own block manages PATH. If this one did too, removing
	// either would break the other.
	check, _, home := startupCheckFor(t, "linux")
	if _, err := check.Install([]string{ShellZsh}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	content := readFile(t, filepath.Join(home, ".zshrc"))
	for _, forbidden := range []string{"PATH=", "export PATH", "alias "} {
		if strings.Contains(content, forbidden) {
			t.Errorf("the block does more than run the check (%q):\n%s", forbidden, content)
		}
	}
}

func TestInstallingTwiceLeavesOneBlock(t *testing.T) {
	check, _, home := startupCheckFor(t, "linux")
	for i := 0; i < 3; i++ {
		if _, err := check.Install([]string{ShellZsh}); err != nil {
			t.Fatalf("Install %d: %v", i, err)
		}
	}

	content := readFile(t, filepath.Join(home, ".zshrc"))
	if got := strings.Count(content, MarkerBegin); got != 1 {
		t.Errorf("the file has %d blocks, want 1:\n%s", got, content)
	}
}

func TestRemovalLeavesTheRestOfTheFileByteIdentical(t *testing.T) {
	// SC-007. A start-up file is the user's; everything that is not ours has to
	// come back exactly as it was, down to the whitespace.
	check, _, home := startupCheckFor(t, "linux")
	path := filepath.Join(home, ".zshrc")

	original := "# my prompt\nexport PS1='> '\n\n\n" +
		"alias ll='ls -la'   \n" + // trailing spaces on purpose
		"# no newline at the end of this file"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Install([]string{ShellZsh}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !strings.Contains(readFile(t, path), MarkerBegin) {
		t.Fatal("the block was not written")
	}

	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := readFile(t, path); got != original {
		t.Errorf("the file did not come back:\n--- want ---\n%q\n--- got ---\n%q", original, got)
	}
}

func TestRemovalIsByteIdenticalForEveryShell(t *testing.T) {
	// SC-007 is stated for all four shells, so it is asserted for all four —
	// the block differs per shell, and so does the file it goes into.
	tests := map[string]struct {
		goos     string
		shell    string
		file     []string
		original string
	}{
		"zsh": {"linux", ShellZsh, []string{".zshrc"}, "export PS1='> '\n"},
		"bash": {"linux", ShellBash, []string{".bashrc"},
			"# bash\nHISTSIZE=10000\n"},
		"fish": {"linux", ShellFish,
			[]string{".config", "fish", "conf.d", fishBlockFile},
			"set -gx MY_THING 1\n"},
		"powershell": {"windows", ShellPowerShell,
			[]string{"Documents", "WindowsPowerShell", "profile.ps1"},
			"Set-Alias ll Get-ChildItem\r\n"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			check, _, home := startupCheckFor(t, test.goos)
			path := filepath.Join(append([]string{home}, test.file...)...)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(path, []byte(test.original), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}

			if _, err := check.Install([]string{test.shell}); err != nil {
				t.Fatalf("Install: %v", err)
			}
			if !strings.Contains(readFile(t, path), MarkerBegin) {
				t.Fatal("the block was not written")
			}
			if _, err := check.Remove(); err != nil {
				t.Fatalf("Remove: %v", err)
			}

			if got := readFile(t, path); got != test.original {
				t.Errorf("the file did not come back:\nwant %q\ngot  %q", test.original, got)
			}
		})
	}
}

func TestRemovalIsIdempotentAndDoesNotAccumulateBlankLines(t *testing.T) {
	check, _, home := startupCheckFor(t, "linux")
	path := filepath.Join(home, ".zshrc")
	original := "export PS1='> '\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := check.Install([]string{ShellZsh}); err != nil {
			t.Fatalf("Install: %v", err)
		}
		if _, err := check.Remove(); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if got := readFile(t, path); got != original {
			t.Fatalf("round trip %d changed the file:\nwant %q\ngot  %q", i, original, got)
		}
	}
}

func TestTheFishDropInIsDeletedRatherThanLeftEmpty(t *testing.T) {
	// It exists only to hold our block, so an empty one is litter.
	check, _, home := startupCheckFor(t, "linux")
	path := filepath.Join(home, ".config", "fish", "conf.d", fishBlockFile)

	if _, err := check.Install([]string{ShellFish}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the drop-in was not created: %v", err)
	}

	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the drop-in must be removed, got %v", err)
	}
}

func TestAFishFileTheUserAlsoEditedIsKept(t *testing.T) {
	check, _, home := startupCheckFor(t, "linux")
	path := filepath.Join(home, ".config", "fish", "conf.d", fishBlockFile)

	if _, err := check.Install([]string{ShellFish}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	content := readFile(t, path)
	if err := os.WriteFile(path, []byte("set -gx MY_THING 1\n"+content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "MY_THING") {
		t.Errorf("the user's own line was lost: %q", got)
	}
	if strings.Contains(got, MarkerBegin) {
		t.Errorf("the block was not removed: %q", got)
	}
}

func TestBothPowerShellProfilesAreWritten(t *testing.T) {
	// 5.1 and 7 read different files and are commonly both installed; a user
	// opening either expects the same behavior.
	check, _, home := startupCheckFor(t, "windows")
	if _, err := check.Install([]string{ShellPowerShell}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for _, relative := range [][]string{
		{"Documents", "WindowsPowerShell", "profile.ps1"},
		{"Documents", "PowerShell", "profile.ps1"},
	} {
		path := filepath.Join(append([]string{home}, relative...)...)
		if !strings.Contains(readFile(t, path), MarkerBegin) {
			t.Errorf("%s has no block", path)
		}
	}
}

func TestMacOSAlsoWritesBashProfileWhenItDoesNotSourceBashrc(t *testing.T) {
	// macOS Terminal starts login shells, which read .bash_profile instead of
	// .bashrc — so on that platform the block would otherwise never run.
	check, _, home := startupCheckFor(t, "darwin")
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Install([]string{ShellBash}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, name := range []string{".bashrc", ".bash_profile"} {
		if !strings.Contains(readFile(t, filepath.Join(home, name)), MarkerBegin) {
			t.Errorf("%s has no block", name)
		}
	}
}

func TestMacOSLeavesBashProfileAloneWhenItAlreadySourcesBashrc(t *testing.T) {
	check, _, home := startupCheckFor(t, "darwin")
	profile := filepath.Join(home, ".bash_profile")
	if err := os.WriteFile(profile, []byte("[ -f ~/.bashrc ] && . ~/.bashrc\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := check.Install([]string{ShellBash}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if strings.Contains(readFile(t, profile), MarkerBegin) {
		t.Error("one block is enough when the profile already sources .bashrc")
	}
}

func TestStatusReportsWhereTheBlockIs(t *testing.T) {
	check, _, home := startupCheckFor(t, "linux")

	if status := check.Status([]string{ShellZsh}); len(status.Installed) != 0 {
		t.Errorf("nothing is installed yet: %+v", status)
	}
	if _, err := check.Install([]string{ShellZsh}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	status := check.Status([]string{ShellZsh})
	if len(status.Installed) != 1 || status.Installed[0] != filepath.Join(home, ".zshrc") {
		t.Errorf("installed = %v", status.Installed)
	}
	if len(status.Candidates) == 0 {
		t.Error("status must say where the block would go")
	}
}

func TestTheStateAndHistoryRecordEveryChange(t *testing.T) {
	check, store, _ := startupCheckFor(t, "linux")

	if _, err := check.Install([]string{ShellZsh}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	state := store.LoadOrZero()
	if len(state.StartupHook.InstalledFiles) != 1 || state.StartupHook.InstalledAt == nil {
		t.Errorf("startup_hook = %+v", state.StartupHook)
	}
	if !contains(eventNames(store.Tail(10)), EventHookInstalled) {
		t.Error("installing must be recorded in the history")
	}

	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	state = store.LoadOrZero()
	if len(state.StartupHook.InstalledFiles) != 0 || state.StartupHook.InstalledAt != nil {
		t.Errorf("startup_hook after removal = %+v", state.StartupHook)
	}
	if !contains(eventNames(store.Tail(10)), EventHookRemoved) {
		t.Error("removing must be recorded in the history")
	}
}

func TestABlockedExecutionPolicyIsReportedWithItsFix(t *testing.T) {
	// The block is still written — it is harmless while blocked — but a user
	// whose prompt never appears needs to be told why.
	check, store, _ := startupCheckFor(t, "windows")
	check.Policy = func() (bool, string) { return true, PolicyFix }

	status, err := check.Install([]string{ShellPowerShell})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !status.BlockedByPolicy || status.PolicyFix != PolicyFix {
		t.Errorf("status = %+v, want it to name the fix", status)
	}
	if !store.LoadOrZero().StartupHook.BlockedByPolicy {
		t.Error("the state must record that the policy blocks it")
	}
	if !contains(eventNames(store.Tail(10)), EventHookBlocked) {
		t.Error("a blocked policy must be in the history")
	}
}

func TestShellDetectionUsesWhatIsOnTheMachine(t *testing.T) {
	check, _, home := startupCheckFor(t, "linux")
	t.Setenv("SHELL", "/usr/bin/zsh")

	if got := check.detectShells(); len(got) != 1 || got[0] != ShellZsh {
		t.Errorf("shells = %v, want just zsh", got)
	}

	if err := os.MkdirAll(filepath.Join(home, ".config", "fish"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# bash\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := check.detectShells()
	for _, want := range []string{ShellZsh, ShellBash, ShellFish} {
		if !contains(got, want) {
			t.Errorf("shells = %v, missing %s", got, want)
		}
	}
}

func TestFishFileFollowsXDGConfigHome(t *testing.T) {
	// fish reads $XDG_CONFIG_HOME/fish when the variable is set — CI runners
	// export it — so that is where the drop-in must be written, or fish will
	// never see it.
	check, _, _ := startupCheckFor(t, "linux")
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	if _, err := check.Install([]string{ShellFish}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	dropIn := filepath.Join(config, "fish", "conf.d", fishBlockFile)
	if _, err := os.Stat(dropIn); err != nil {
		t.Fatalf("drop-in is not where fish looks: %v", err)
	}

	if _, err := check.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(dropIn); !os.IsNotExist(err) {
		t.Errorf("drop-in still present after Remove: %v", err)
	}
}

func TestAMachineWithNoEvidenceStillGetsSomewhereToPromptFrom(t *testing.T) {
	check, _, _ := startupCheckFor(t, "linux")
	t.Setenv("SHELL", "")

	if got := check.detectShells(); len(got) == 0 {
		t.Error("detection must not come back empty; there would be no prompt at all")
	}
}

func TestAPathWithAwkwardCharactersIsQuoted(t *testing.T) {
	// Home directories contain spaces, apostrophes and — on Windows —
	// backslashes. An unquoted path here is a broken shell start-up.
	for _, executable := range []string{
		"/home/o'brien/my tools/intenter",
		`C:\Users\O'Brien\App Data\intenter.exe`,
	} {
		check, _, home := startupCheckFor(t, "linux")
		check.Executable = executable
		if _, err := check.Install([]string{ShellZsh, ShellFish}); err != nil {
			t.Fatalf("Install: %v", err)
		}

		posix := readFile(t, filepath.Join(home, ".zshrc"))
		if strings.Contains(posix, "'"+executable+"'") == strings.Contains(executable, "'") {
			t.Errorf("the apostrophe was not escaped for POSIX:\n%s", posix)
		}
		fish := readFile(t, filepath.Join(home, ".config", "fish", "conf.d", fishBlockFile))
		if strings.Contains(executable, `\`) && !strings.Contains(fish, `\\`) {
			t.Errorf("the backslash was not escaped for fish:\n%s", fish)
		}
	}
}
