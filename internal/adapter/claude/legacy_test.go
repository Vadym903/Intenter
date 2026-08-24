package claude

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// These tests cover the legacy AgentGuard → Intenter cleanup rules of
// specs/005-make-product-usable/contracts/identity-and-rename.md §2.3–2.5:
// setup replaces a legacy hook entry with Intenter's own, uninstall removes
// either identity, and the match stays exact (AG-11) so a similarly named
// hook is never claimed.

func TestOwnedByLegacyExactMatch(t *testing.T) {
	tests := []struct {
		name  string
		entry map[string]any
		owned bool
	}{
		{"shell form", map[string]any{"command": `"/usr/local/bin/agentguard" hook claude`}, true},
		{"unquoted shell form", map[string]any{"command": `/usr/local/bin/agentguard hook claude`}, true},
		{"windows exec form", map[string]any{
			"command": "C:/Users/u/AppData/Local/AgentGuard/bin/agentguard.exe",
			"args":    []any{"hook", "claude"},
		}, true},
		{"a similarly named binary", map[string]any{
			"command": "/usr/bin/agentguard-helper hook claude",
		}, false},
		{"a different agentguard subcommand", map[string]any{
			"command": "/usr/local/bin/agentguard daemon run",
		}, false},
		{"wrapped in a subshell", map[string]any{
			"command": `sh -c "agentguard hook claude"`,
		}, false},
		{"trailing extra argument", map[string]any{
			"command": "/usr/local/bin/agentguard hook claude --extra",
		}, false},
		{"chained before another command", map[string]any{
			"command": "/usr/bin/agentguard && true",
		}, false},
		{"chained after another command", map[string]any{
			"command": "echo hi && /usr/local/bin/agentguard hook claude",
		}, false},
		{"exec form with an extra arg", map[string]any{
			"command": "/bin/agentguard",
			"args":    []any{"hook", "claude", "--verbose"},
		}, false},
		{"empty", map[string]any{}, false},
		{"empty command", map[string]any{"command": ""}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ownedByLegacy(tt.entry); got != tt.owned {
				t.Errorf("ownedByLegacy() = %v, want %v", got, tt.owned)
			}
		})
	}
}

// legacySettings has a legacy AgentGuard hook installed in every event
// Intenter installs one for, plus an unrelated hook Intenter must not touch.
const legacySettings = `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash|PowerShell", "hooks": [{"type": "command", "command": "/usr/local/bin/agentguard hook claude", "timeout": 10}]}
    ],
    "PermissionRequest": [
      {"matcher": "Bash|PowerShell", "hooks": [{"type": "command", "command": "/usr/local/bin/agentguard hook claude", "timeout": 10}]}
    ],
    "PostToolUse": [
      {"matcher": "Bash|PowerShell", "hooks": [{"type": "command", "command": "/usr/local/bin/agentguard hook claude", "timeout": 10}]}
    ],
    "ConfigChange": [
      {"matcher": "user_settings|project_settings|local_settings", "hooks": [{"type": "command", "command": "/usr/local/bin/agentguard hook claude", "timeout": 10}]}
    ],
    "SessionStart": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "/usr/bin/greet"}]}
    ]
  }
}`

func TestInstallHooksReplacesLegacyEntriesInEveryEventGroup(t *testing.T) {
	path := settingsFixture(t, legacySettings)

	if err := InstallHooks(path, testExecutable, "darwin", true); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	installed, err := HooksInstalled(path)
	if err != nil {
		t.Fatalf("read installed hooks: %v", err)
	}
	want := wantedEvents(true)
	if strings.Join(installed, ",") != strings.Join(want, ",") {
		t.Fatalf("installed = %v, want %v", installed, want)
	}

	hooks := readTree(t, path)["hooks"].(map[string]any)
	for _, event := range want {
		groups := hooks[event].([]any)
		if len(groups) != 1 {
			t.Fatalf("%s groups = %d, want the legacy entry replaced rather than duplicated", event, len(groups))
		}
		entries := groups[0].(map[string]any)["hooks"].([]any)
		if len(entries) != 1 {
			t.Fatalf("%s entries = %d, want one", event, len(entries))
		}
		command := entries[0].(map[string]any)["command"].(string)
		if !strings.Contains(command, testExecutable) {
			t.Errorf("%s command = %q, want the Intenter binary", event, command)
		}
	}

	// The unrelated hook must survive untouched.
	sessionStart := hooks["SessionStart"].([]any)
	if len(sessionStart) != 1 {
		t.Fatalf("SessionStart = %#v, want the user's own group", hooks["SessionStart"])
	}
	greet := sessionStart[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
	if greet != "/usr/bin/greet" {
		t.Errorf("SessionStart command = %q, want it untouched", greet)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(content), "agentguard") {
		t.Error("no legacy AgentGuard entry may remain after install")
	}

	// §12.4 / idempotent: running setup again must converge, not duplicate.
	first := content
	for i := 0; i < 3; i++ {
		if err := InstallHooks(path, testExecutable, "darwin", true); err != nil {
			t.Fatalf("reinstall hooks: %v", err)
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

func TestInstallHooksReplacesTheWindowsExecFormLegacyEntry(t *testing.T) {
	path := settingsFixture(t, `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash|PowerShell", "hooks": [
        {"type": "command", "command": "C:/Users/u/AppData/Local/AgentGuard/bin/agentguard.exe", "args": ["hook", "claude"], "timeout": 10}
      ]}
    ]
  }
}`)
	executable := `C:\Users\u\AppData\Local\Intenter\bin\intenter.exe`

	if err := InstallHooks(path, executable, "windows", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	hooks := readTree(t, path)["hooks"].(map[string]any)
	groups := hooks[EventPreToolUse].([]any)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want the legacy entry replaced rather than duplicated", len(groups))
	}
	entries := groups[0].(map[string]any)["hooks"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want one", len(entries))
	}
	entry := entries[0].(map[string]any)
	if entry["command"] != executable {
		t.Errorf("command = %v, want the current Intenter binary", entry["command"])
	}
	if args, _ := entry["args"].([]any); len(args) != 2 || args[0] != "hook" || args[1] != "claude" {
		t.Errorf("args = %v, want [hook claude]", entry["args"])
	}
}

func TestUninstallRemovesBothTheLegacyAndCurrentIdentity(t *testing.T) {
	path := settingsFixture(t, `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash|PowerShell", "hooks": [
        {"type": "command", "command": "/usr/local/bin/agentguard hook claude", "timeout": 10},
        {"type": "command", "command": "/usr/local/bin/intenter hook claude", "timeout": 10}
      ]}
    ],
    "SessionStart": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "/usr/bin/greet"}]}
    ]
  }
}`)

	if err := RemoveHooks(path); err != nil {
		t.Fatalf("remove hooks: %v", err)
	}

	tree := readTree(t, path)
	hooks, _ := tree["hooks"].(map[string]any)
	if _, present := hooks[EventPreToolUse]; present {
		t.Errorf("PreToolUse must be gone once both identities are removed, got %#v", hooks[EventPreToolUse])
	}
	sessionStart, ok := hooks["SessionStart"].([]any)
	if !ok || len(sessionStart) != 1 {
		t.Fatalf("SessionStart = %#v, want the user's own group preserved", hooks["SessionStart"])
	}
}

func TestLegacyLookalikesAreNeverClaimed(t *testing.T) {
	// A hook that merely resembles the legacy identity — a different binary,
	// a wrapper, or an extra argument — is not the legacy AgentGuard hook and
	// must survive both install and uninstall (I-9, AG-11 exactness).
	const lookalikes = `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [
        {"type": "command", "command": "/usr/bin/agentguard-helper hook claude"},
        {"type": "command", "command": "sh -c \"agentguard hook claude\""},
        {"type": "command", "command": "/usr/local/bin/agentguard hook claude --extra"},
        {"type": "command", "command": "echo hi && /usr/local/bin/agentguard hook claude"}
      ]}
    ]
  }
}`

	t.Run("uninstall leaves them alone", func(t *testing.T) {
		path := settingsFixture(t, lookalikes)
		before := readTree(t, path)

		if err := RemoveHooks(path); err != nil {
			t.Fatalf("remove hooks: %v", err)
		}
		after := readTree(t, path)

		if !reflect.DeepEqual(before, after) {
			t.Errorf("look-alike hooks must survive uninstall:\n%#v\n%#v", before, after)
		}
	})

	t.Run("install adds beside them rather than replacing", func(t *testing.T) {
		path := settingsFixture(t, lookalikes)

		if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
			t.Fatalf("install hooks: %v", err)
		}

		hooks := readTree(t, path)["hooks"].(map[string]any)
		groups := hooks[EventPreToolUse].([]any)
		if len(groups) != 2 {
			t.Fatalf("groups = %d, want the look-alikes' group plus Intenter's own", len(groups))
		}
		lookalikeGroup := groups[0].(map[string]any)["hooks"].([]any)
		if len(lookalikeGroup) != 4 {
			t.Fatalf("look-alike hooks = %d, want all four preserved", len(lookalikeGroup))
		}
	})
}
