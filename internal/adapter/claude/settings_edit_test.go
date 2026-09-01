package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// INVARIANT I-9: setup and uninstall must not delete or rewrite any Claude
// setting Intenter did not create, and must back the file up first.
//
// This is the invariant a user's trust in `setup claude` rests on: the settings
// file holds their own hooks, permissions and preferences, and Intenter is a
// guest in it.

const testExecutable = "/usr/local/bin/intenter"

// settingsFixture is a settings file with a variety of unrelated content.
func settingsFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return path
}

// readTree parses a settings file for comparison.
func readTree(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var tree map[string]any
	if err := json.Unmarshal(content, &tree); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, content)
	}
	return tree
}

// userSettings is a realistic file: the user's own hooks, permissions,
// preferences and a key Intenter has never heard of.
const userSettings = `{
  "model": "claude-opus-5",
  "env": {"FOO": "bar"},
  "permissions": {
    "allow": ["Bash(npm run test)", "Read(//tmp/**)"],
    "deny": ["Bash(rm -rf /)"]
  },
  "hooks": {
    "PreToolUse": [
      {"matcher": "Write", "hooks": [{"type": "command", "command": "/usr/bin/my-linter", "timeout": 5}]}
    ],
    "SessionStart": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "/usr/bin/greet"}]}
    ]
  },
  "someFutureKey": {"nested": [1, 2, 3]}
}`

func TestInstallHooksPreservesEverythingElse(t *testing.T) {
	path := settingsFixture(t, userSettings)
	before := readTree(t, path)

	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	after := readTree(t, path)

	// Every key the user had is byte-for-byte what it was, except hooks.
	for key, want := range before {
		if key == "hooks" {
			continue
		}
		if !reflect.DeepEqual(after[key], want) {
			t.Errorf("key %q changed:\nbefore: %#v\nafter:  %#v", key, want, after[key])
		}
	}
	if len(after) != len(before) {
		t.Errorf("keys = %d, want %d — nothing may be added or dropped", len(after), len(before))
	}
}

func TestInstallHooksKeepsTheUsersOwnHooks(t *testing.T) {
	path := settingsFixture(t, userSettings)

	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	hooks := readTree(t, path)["hooks"].(map[string]any)

	// The user's unrelated event survives untouched.
	sessionStart, ok := hooks["SessionStart"].([]any)
	if !ok || len(sessionStart) != 1 {
		t.Fatalf("SessionStart = %#v, want the user's own group", hooks["SessionStart"])
	}

	// Their PreToolUse linter survives beside Intenter's entry.
	preToolUse, _ := hooks[EventPreToolUse].([]any)
	if len(preToolUse) != 2 {
		t.Fatalf("PreToolUse groups = %d, want the user's plus Intenter's", len(preToolUse))
	}
	found := false
	for _, raw := range preToolUse {
		group := raw.(map[string]any)
		for _, rawHook := range group["hooks"].([]any) {
			if command, _ := rawHook.(map[string]any)["command"].(string); command == "/usr/bin/my-linter" {
				found = true
			}
		}
	}
	if !found {
		t.Error("the user's own PreToolUse hook must survive")
	}
}

// wantedEvents is the sorted event list a setup should produce, derived from
// what the package declares rather than restated. Adding an event is then a
// one-line change in one place, and a test that still fails after it is telling
// the truth about a real gap.
func wantedEvents(configChange bool) []string {
	events := append([]string(nil), hookEvents...)
	if configChange {
		events = append(events, EventConfigChange)
	}
	sort.Strings(events)
	return events
}

func TestInstallHooksAddsEveryRequiredEvent(t *testing.T) {
	path := settingsFixture(t, `{}`)

	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	installed, err := HooksInstalled(path)
	if err != nil {
		t.Fatalf("read installed hooks: %v", err)
	}

	want := wantedEvents(false)
	if strings.Join(installed, ",") != strings.Join(want, ",") {
		t.Errorf("installed = %v, want %v", installed, want)
	}
}

func TestConfigChangeHookIsOptional(t *testing.T) {
	path := settingsFixture(t, `{}`)

	if err := InstallHooks(path, testExecutable, "darwin", true); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	installed, _ := HooksInstalled(path)

	want := wantedEvents(true)
	if strings.Join(installed, ",") != strings.Join(want, ",") {
		t.Errorf("installed = %v, want the ConfigChange hook too: %v", installed, want)
	}
}

func TestEveryInstalledHookCarriesAMatcherThatCanFire(t *testing.T) {
	// A tool matcher on SessionEnd or ConfigChange installs a hook that never
	// runs — the failure is silent, and looks exactly like the feature not
	// existing. Each event's matcher has to be the one it filters on.
	path := settingsFixture(t, `{}`)
	if err := InstallHooks(path, testExecutable, "darwin", true); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	hooks, _ := readTree(t, path)["hooks"].(map[string]any)
	want := map[string]string{
		EventPreToolUse:        HookMatcher,
		EventPermissionRequest: HookMatcher,
		EventPostToolUse:       HookMatcher,
		EventSessionEnd:        SessionEndMatcher,
		EventConfigChange:      ConfigChangeMatcher,
	}

	for event, wantMatcher := range want {
		groups, ok := hooks[event].([]any)
		if !ok || len(groups) == 0 {
			t.Errorf("%s: no hook group installed", event)
			continue
		}
		group, _ := groups[0].(map[string]any)
		if got, _ := group["matcher"].(string); got != wantMatcher {
			t.Errorf("%s: matcher = %q, want %q", event, got, wantMatcher)
		}
	}
}

func TestInstallHooksIsIdempotent(t *testing.T) {
	// §12.4: running setup again must converge, not accumulate.
	path := settingsFixture(t, userSettings)

	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
			t.Fatalf("install hooks: %v", err)
		}
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("repeated setup changed the file:\n%s\n---\n%s", first, second)
	}
}

func TestMovedBinaryReplacesTheStaleEntry(t *testing.T) {
	// §12.4: a binary that moved must not leave a hook pointing at nothing,
	// and must not produce a second entry either.
	path := settingsFixture(t, `{}`)

	if err := InstallHooks(path, "/old/path/intenter", "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	if err := InstallHooks(path, "/new/path/intenter", "darwin", false); err != nil {
		t.Fatalf("reinstall hooks: %v", err)
	}

	hooks := readTree(t, path)["hooks"].(map[string]any)
	groups := hooks[EventPreToolUse].([]any)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want the stale entry replaced rather than duplicated", len(groups))
	}

	entries := groups[0].(map[string]any)["hooks"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want one", len(entries))
	}
	command := entries[0].(map[string]any)["command"].(string)
	if !strings.Contains(command, "/new/path/intenter") {
		t.Errorf("command = %q, want the current binary path", command)
	}
	if strings.Contains(command, "/old/path") {
		t.Error("the stale path must be gone")
	}
}

func TestHookFormPerPlatform(t *testing.T) {
	// §11.7: Windows needs the exec form, because the same shell-quoted string
	// is read differently by Git Bash and PowerShell.
	t.Run("unix uses the shell form", func(t *testing.T) {
		path := settingsFixture(t, `{}`)
		if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
			t.Fatalf("install hooks: %v", err)
		}

		command, ok := InstalledHookCommand(path, EventPreToolUse)
		if !ok {
			t.Fatal("want an installed hook")
		}
		if len(command.Args) != 0 {
			t.Errorf("args = %v, want the shell form", command.Args)
		}
		if command.Command != `"`+testExecutable+`" hook claude` {
			t.Errorf("command = %q", command.Command)
		}
	})

	t.Run("windows uses the exec form", func(t *testing.T) {
		path := settingsFixture(t, `{}`)
		executable := `C:\Users\u\AppData\Local\Intenter\bin\intenter.exe`
		if err := InstallHooks(path, executable, "windows", false); err != nil {
			t.Fatalf("install hooks: %v", err)
		}

		command, ok := InstalledHookCommand(path, EventPreToolUse)
		if !ok {
			t.Fatal("want an installed hook")
		}
		if command.Command != executable {
			t.Errorf("command = %q, want the bare executable", command.Command)
		}
		if strings.Join(command.Args, " ") != "hook claude" {
			t.Errorf("args = %v, want [hook claude]", command.Args)
		}
	})
}

func TestHookEntryCarriesATimeout(t *testing.T) {
	path := settingsFixture(t, `{}`)
	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	hooks := readTree(t, path)["hooks"].(map[string]any)
	entry := hooks[EventPreToolUse].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)

	if entry["type"] != "command" {
		t.Errorf("type = %v, want command", entry["type"])
	}
	if timeout, _ := entry["timeout"].(float64); int(timeout) != HookTimeoutSeconds {
		t.Errorf("timeout = %v, want %d", entry["timeout"], HookTimeoutSeconds)
	}
	group := hooks[EventPreToolUse].([]any)[0].(map[string]any)
	if group["matcher"] != HookMatcher {
		t.Errorf("matcher = %v, want %q", group["matcher"], HookMatcher)
	}
}

func TestUninstallLeavesTheFileEquivalent(t *testing.T) {
	// The strongest statement of I-9: install then uninstall is a no-op on
	// everything the user owns.
	path := settingsFixture(t, userSettings)
	before := readTree(t, path)

	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	if err := RemoveHooks(path); err != nil {
		t.Fatalf("remove hooks: %v", err)
	}
	after := readTree(t, path)

	if !reflect.DeepEqual(before, after) {
		beforeJSON, _ := json.MarshalIndent(before, "", "  ")
		afterJSON, _ := json.MarshalIndent(after, "", "  ")
		t.Errorf("uninstall did not restore the settings:\nbefore:\n%s\nafter:\n%s", beforeJSON, afterJSON)
	}
}

func TestUninstallRemovesEmptyGroups(t *testing.T) {
	// A group that existed only to hold Intenter's hook goes with it.
	path := settingsFixture(t, `{"model":"claude-opus-5"}`)

	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	if err := RemoveHooks(path); err != nil {
		t.Fatalf("remove hooks: %v", err)
	}

	tree := readTree(t, path)
	if _, present := tree["hooks"]; present {
		t.Errorf("an empty hooks block must be removed, got %#v", tree["hooks"])
	}
	if tree["model"] != "claude-opus-5" {
		t.Error("the user's own settings must survive")
	}
}

func TestUninstallRemovesAHookFromAMovedBinary(t *testing.T) {
	// A user who moved or reinstalled the binary must still be able to remove
	// the hook that names the old path.
	path := settingsFixture(t, `{}`)
	if err := InstallHooks(path, "/old/path/intenter", "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	if err := RemoveHooks(path); err != nil {
		t.Fatalf("remove hooks: %v", err)
	}

	installed, _ := HooksInstalled(path)
	if len(installed) != 0 {
		t.Errorf("installed = %v, want none", installed)
	}
}

func TestUninstallKeepsHooksThatAreNotOurs(t *testing.T) {
	// A hook that merely looks similar is not Intenter's.
	path := settingsFixture(t, `{
	  "hooks": {
	    "PreToolUse": [
	      {"matcher": "Bash", "hooks": [
	        {"type": "command", "command": "/usr/bin/other-guard hook claude"},
	        {"type": "command", "command": "/usr/bin/intenter-helper check"}
	      ]}
	    ]
	  }
	}`)
	before := readTree(t, path)

	if err := RemoveHooks(path); err != nil {
		t.Fatalf("remove hooks: %v", err)
	}
	after := readTree(t, path)

	if !reflect.DeepEqual(before, after) {
		t.Errorf("hooks that are not Intenter's must survive:\n%#v\n%#v", before, after)
	}
}

func TestInvalidJSONIsNeverOverwritten(t *testing.T) {
	// §12.2 step 3: a settings file Intenter cannot parse is one it must not
	// touch — rewriting it would destroy configuration it never understood.
	const broken = `{"model": "claude-opus-5",` // truncated
	path := settingsFixture(t, broken)

	if err := InstallHooks(path, testExecutable, "darwin", false); err == nil {
		t.Fatal("want an error for unparsable settings")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != broken {
		t.Errorf("the file was modified:\n%s", content)
	}
}

func TestMissingSettingsFileIsCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "settings.json")

	if err := EnsureSettingsFile(path); err != nil {
		t.Fatalf("ensure settings: %v", err)
	}
	tree := readTree(t, path)
	if len(tree) != 0 {
		t.Errorf("a created settings file starts empty, got %#v", tree)
	}

	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	if installed, _ := HooksInstalled(path); len(installed) != len(hookEvents) {
		t.Errorf("installed = %v, want %d hooks", installed, len(hookEvents))
	}
}

func TestBackupIsWrittenBeforeAnyEdit(t *testing.T) {
	path := settingsFixture(t, userSettings)
	dataDir := t.TempDir()

	backup, err := BackupSettings(path, dataDir, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if backup == "" {
		t.Fatal("want a backup path")
	}

	content, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(content) != userSettings {
		t.Error("the backup must be the file as it was")
	}
	if !strings.Contains(filepath.Base(backup), "20260816T120000Z") {
		t.Errorf("backup name = %q, want the UTC timestamp", filepath.Base(backup))
	}
}

func TestBackupsArePruned(t *testing.T) {
	path := settingsFixture(t, userSettings)
	dataDir := t.TempDir()

	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < MaxBackups+5; i++ {
		if _, err := BackupSettings(path, dataDir, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if err != nil {
		t.Fatalf("read backups: %v", err)
	}
	if len(entries) != MaxBackups {
		t.Errorf("backups = %d, want the last %d", len(entries), MaxBackups)
	}

	// The ones kept are the most recent.
	newest := base.Add(time.Duration(MaxBackups+4) * time.Minute).Format("20060102T150405Z")
	found := false
	for _, entry := range entries {
		if strings.Contains(entry.Name(), newest) {
			found = true
		}
	}
	if !found {
		t.Error("the newest backup must be kept")
	}
}

func TestBackupOfAMissingFileIsNotAnError(t *testing.T) {
	dataDir := t.TempDir()
	backup, err := BackupSettings(filepath.Join(t.TempDir(), "absent.json"), dataDir, time.Now())
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if backup != "" {
		t.Errorf("backup = %q, want none for a file that does not exist", backup)
	}
}

func TestOwnershipDetection(t *testing.T) {
	tests := []struct {
		name  string
		entry map[string]any
		owned bool
	}{
		{"shell form", map[string]any{"command": `"/usr/local/bin/intenter" hook claude`}, true},
		{"unquoted shell form", map[string]any{"command": `/usr/local/bin/intenter hook claude`}, true},
		{"exec form", map[string]any{
			"command": `C:\bin\intenter.exe`,
			"args":    []any{"hook", "claude"},
		}, true},
		{"another tool with the same arguments", map[string]any{
			"command": "/usr/bin/other hook claude",
		}, false},
		{"a different intenter subcommand", map[string]any{
			"command": "/usr/local/bin/intenter daemon run",
		}, false},
		{"a similarly named binary", map[string]any{
			"command": "/usr/bin/intenter-helper hook claude",
		}, false},
		{"empty", map[string]any{}, false},
		{"empty command", map[string]any{"command": ""}, false},
		// A user's own hook that chains a command before ours, or wraps it, must
		// not be claimed by uninstall — the match is exact, not a suffix test
		// (I-9).
		{"chained before ours", map[string]any{
			"command": "echo hi && /usr/local/bin/intenter hook claude",
		}, false},
		{"wrapped in a subshell", map[string]any{
			"command": "sh -c '/usr/local/bin/intenter hook claude'",
		}, false},
		{"trailing extra argument", map[string]any{
			"command": "/usr/local/bin/intenter hook claude --verbose",
		}, false},
		{"exec form with an extra arg", map[string]any{
			"command": `/bin/intenter`,
			"args":    []any{"hook", "claude", "--verbose"},
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OwnedByAnyIntenter(tt.entry); got != tt.owned {
				t.Errorf("OwnedByAnyIntenter() = %v, want %v", got, tt.owned)
			}
		})
	}
}

// Removing a permission has to reach the rule Claude holds of its own, or the
// command keeps running silently and the removal changed nothing that matters.
// The same guest rules apply as everywhere else in this file: only the named
// rule goes, the backup comes first, and a file that is not this user's to
// change is not changed.

func TestRemoveAllowRuleTakesOnlyTheNamedRule(t *testing.T) {
	path := settingsFixture(t, userSettings)
	dataDir := t.TempDir()
	before := readTree(t, path)

	removal, err := RemoveAllowRule(path, dataDir, "Bash(npm run test)", time.Now())
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !removal.Removed {
		t.Fatal("the rule was in the file and was not removed")
	}

	after := readTree(t, path)
	allow := after["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) != 1 || allow[0] != "Read(//tmp/**)" {
		t.Errorf("allow = %v, want only the untouched rule", allow)
	}

	// Everything else in the file is the user's and must be exactly as it was.
	for _, key := range []string{"model", "env", "hooks"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Errorf("%q changed:\nbefore: %#v\nafter:  %#v", key, before[key], after[key])
		}
	}
	if deny := after["permissions"].(map[string]any)["deny"]; !reflect.DeepEqual(
		deny, before["permissions"].(map[string]any)["deny"]) {
		t.Error("the deny list must not be touched")
	}
}

// FR-021: a removal never deletes history. For a rule there is no database row,
// so the backup is the record — which means it has to actually hold the rule.
func TestRemoveAllowRuleBacksUpTheRuleItRemoves(t *testing.T) {
	path := settingsFixture(t, userSettings)
	dataDir := t.TempDir()

	removal, err := RemoveAllowRule(path, dataDir, "Bash(npm run test)", time.Now())
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removal.BackupPath == "" {
		t.Fatal("INVARIANT I-9: the file was modified without a backup")
	}
	saved, err := os.ReadFile(removal.BackupPath)
	if err != nil {
		t.Fatalf("read the backup: %v", err)
	}
	if !strings.Contains(string(saved), "Bash(npm run test)") {
		t.Errorf("the backup does not hold the removed rule, so nothing does:\n%s", saved)
	}
}

func TestRemoveAllowRuleLeavesAStaleTargetAlone(t *testing.T) {
	path := settingsFixture(t, userSettings)
	dataDir := t.TempDir()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	removal, err := RemoveAllowRule(path, dataDir, "Bash(npm run something-else)", time.Now())
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removal.Removed {
		t.Error("a rule that is not in the file must not report as removed")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a stale target must change nothing — removing something else because " +
			"the text moved is worse than doing nothing")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "backups")); err == nil {
		t.Error("nothing was changed, so nothing should have been backed up")
	}
}

func TestRemoveAllowRuleRefusesAManagedPolicyFile(t *testing.T) {
	managed := managedSettingsPath(runtime.GOOS)
	dataDir := t.TempDir()

	_, err := RemoveAllowRule(managed, dataDir, "Bash(npm run test)", time.Now())
	if err == nil {
		t.Fatal("a managed policy file belongs to an administrator and is never edited")
	}
	if !strings.Contains(err.Error(), "managed") {
		t.Errorf("the refusal must say why: %v", err)
	}
}

func TestRemoveAllowRuleReportsAnUnwritableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not work this way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(userSettings), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The write is atomic through a temporary file, so it is the directory that
	// has to be closed to make it fail.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := RemoveAllowRule(path, t.TempDir(), "Bash(npm run test)", time.Now()); err == nil {
		t.Fatal("an unwritable settings file must be reported, not silently skipped")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a failed removal must leave the file exactly as it was")
	}
}
