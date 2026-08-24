package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// S13 (PROTOTYPE_SPEC.md §29): the whole integration, through the real binary
// and the exact hook command line setup writes. A hook that was installed but
// cannot execute is the failure a user would otherwise meet mid-session.

// fakeClaudeShim writes a `claude` executable that reports a version, so setup
// has something to detect, and puts it first on PATH so a real Claude Code
// installed on the machine running the tests cannot be detected instead. The
// rest of PATH is preserved, so the setup self-test's shell still resolves.
func fakeClaudeShim(t *testing.T, env *Env) {
	t.Helper()

	dir := filepath.Join(env.Home, ".local", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Windows cannot run a shebang script; a batch file named claude.cmd is
	// what PATH lookup through PATHEXT finds there.
	name, script := "claude", "#!/bin/sh\necho '2.1.233 (Claude Code)'\n"
	if runtime.GOOS == "windows" {
		name, script = "claude.cmd", "@echo 2.1.233 (Claude Code)\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}

	if env.ExtraEnv == nil {
		env.ExtraEnv = map[string]string{}
	}
	env.ExtraEnv["PATH"] = dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// claudeSettingsPath is the file setup installs hooks into.
func claudeSettingsPath(env *Env) string {
	return filepath.Join(env.Home, ".claude", "settings.json")
}

// readSettings parses the Claude settings file.
func readSettings(t *testing.T, path string) map[string]any {
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

func TestS13SetupInstallsAWorkingIntegration(t *testing.T) {
	env := NewEnv(t)
	fakeClaudeShim(t, env)
	// The daemon the harness started already occupies the endpoint, so setup
	// finds it running rather than starting a second one.

	started := time.Now()
	out, errOut, code := env.CLI("setup", "claude", "--no-service")
	elapsed := time.Since(started)

	if code != 0 {
		t.Fatalf("setup exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, want := range []string{
		"Intenter setup",
		"✓ Claude Code detected (2.1.233)",
		"✓ Settings backed up",
		"✓ Permission hooks installed",
		"✓ Database initialized",
		"✓ Daemon running",
		"✓ Integration test passed",
		"Restart any running Claude Code sessions",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("setup report is missing %q:\n%s", want, out)
		}
	}

	// SC-004: setup is a single command a user waits through.
	if elapsed > 60*time.Second {
		t.Errorf("setup took %s, want under a minute", elapsed)
	}
}

func TestS13InstalledHookCommandLineActuallyRuns(t *testing.T) {
	// The step that matters most: whatever setup wrote into the settings file
	// has to be executable and answer with valid JSON.
	env := NewEnv(t)
	fakeClaudeShim(t, env)
	env.MustCLI("setup", "claude", "--no-service")

	command, args := installedHookInvocation(t, env)

	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "s13",
		"cwd":             env.Workspace,
		"permission_mode": "default",
		"tool_name":       "Bash",
		"tool_use_id":     "toolu_s13",
		"tool_input":      map[string]any{"command": "rm -rf ~/Documents"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	run := exec.Command(command, args...)
	run.Env = append(env.environ(), "INTENTER_SELFTEST=1")
	run.Dir = env.Workspace
	run.Stdin = strings.NewReader(string(payload))

	output, err := run.Output()
	if err != nil {
		t.Fatalf("the installed hook command failed: %v", err)
	}

	var response struct {
		SystemMessage      string `json:"systemMessage"`
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("the hook did not answer with JSON: %v\n%s", err, output)
	}
	if response.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want deny\n%s",
			response.HookSpecificOutput.PermissionDecision, output)
	}
	if response.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q", response.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(response.HookSpecificOutput.PermissionDecisionReason, "Intenter") {
		t.Errorf("reason = %q", response.HookSpecificOutput.PermissionDecisionReason)
	}
}

func TestS13SelfTestLeavesNoHistory(t *testing.T) {
	// Proving the integration works must not put a decision in the user's
	// history: INTENTER_SELFTEST forces a dry run (§12.2 step 7).
	env := NewEnv(t)
	fakeClaudeShim(t, env)
	env.MustCLI("setup", "claude", "--no-service")

	out := env.MustCLI("history", "--limit", "50", "--json")
	var events []map[string]any
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("parse history: %v\n%s", err, out)
	}
	if len(events) != 0 {
		t.Errorf("setup left %d events in the history:\n%s", len(events), out)
	}
}

// installedHookInvocation reads the command line setup wrote and turns it back
// into something executable.
func installedHookInvocation(t *testing.T, env *Env) (string, []string) {
	t.Helper()

	tree := readSettings(t, claudeSettingsPath(env))
	hooks, ok := tree["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("no hooks were installed: %#v", tree)
	}
	groups, ok := hooks["PreToolUse"].([]any)
	if !ok || len(groups) == 0 {
		t.Fatalf("no PreToolUse hook: %#v", hooks)
	}

	entry := groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	command, _ := entry["command"].(string)

	if raw, ok := entry["args"].([]any); ok && len(raw) > 0 {
		args := make([]string, 0, len(raw))
		for _, item := range raw {
			args = append(args, item.(string))
		}
		return command, args
	}

	text := strings.TrimSpace(command)
	executable := strings.Trim(strings.TrimSpace(strings.TrimSuffix(text, "hook claude")), `"'`)
	return executable, []string{"hook", "claude"}
}

func TestS13SetupTwiceDoesNotDuplicateHooks(t *testing.T) {
	env := NewEnv(t)
	fakeClaudeShim(t, env)

	env.MustCLI("setup", "claude", "--no-service")
	first := readSettings(t, claudeSettingsPath(env))

	env.MustCLI("setup", "claude", "--no-service")
	second := readSettings(t, claudeSettingsPath(env))

	if !reflect.DeepEqual(first, second) {
		firstJSON, _ := json.MarshalIndent(first, "", "  ")
		secondJSON, _ := json.MarshalIndent(second, "", "  ")
		t.Errorf("running setup twice changed the settings:\n%s\n---\n%s", firstJSON, secondJSON)
	}
}

func TestS13UninstallLeavesUnrelatedSettingsIdentical(t *testing.T) {
	// INVARIANT I-9, end to end: everything the user owns comes back exactly
	// as it was.
	env := NewEnv(t)
	fakeClaudeShim(t, env)

	settingsPath := claudeSettingsPath(env)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := `{
  "model": "claude-opus-5",
  "permissions": {"allow": ["Bash(npm run test)"]},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Write", "hooks": [{"type": "command", "command": "/usr/bin/my-linter"}]}
    ]
  },
  "someFutureKey": {"nested": [1, 2, 3]}
}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	before := readSettings(t, settingsPath)

	env.MustCLI("setup", "claude", "--no-service")
	env.MustCLI("uninstall", "claude", "--keep-daemon")

	after := readSettings(t, settingsPath)
	if !reflect.DeepEqual(before, after) {
		beforeJSON, _ := json.MarshalIndent(before, "", "  ")
		afterJSON, _ := json.MarshalIndent(after, "", "  ")
		t.Errorf("uninstall did not restore the settings:\nbefore:\n%s\nafter:\n%s", beforeJSON, afterJSON)
	}
}

func TestS13UninstallKeepDaemonLeavesItRunning(t *testing.T) {
	env := NewEnv(t)
	fakeClaudeShim(t, env)
	env.MustCLI("setup", "claude", "--no-service")

	env.MustCLI("uninstall", "claude", "--keep-daemon")

	if _, _, code := env.CLI("daemon", "status"); code != 0 {
		t.Error("--keep-daemon must leave the daemon running")
	}
}

func TestS13UninstallStopsTheDaemonByDefault(t *testing.T) {
	env := NewEnv(t)
	fakeClaudeShim(t, env)
	env.MustCLI("setup", "claude", "--no-service")

	out, errOut, code := env.CLI("uninstall", "claude")
	if code != 0 {
		t.Fatalf("uninstall exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "✓ Daemon stopped") {
		t.Errorf("report is missing the daemon step:\n%s", out)
	}

	// The harness no longer owns a running daemon.
	env.ForgetDaemon()

	if _, _, code := env.CLI("daemon", "status"); code == 0 {
		t.Error("the daemon must be stopped")
	}
}

func TestS13UninstallKeepsTheHistoryByDefault(t *testing.T) {
	env := NewEnv(t)
	fakeClaudeShim(t, env)
	env.PreToolUse("session-1", "toolu_1", "rm -rf ~/Documents")
	env.MustCLI("setup", "claude", "--no-service")

	env.MustCLI("uninstall", "claude", "--keep-daemon")

	out := env.MustCLI("history", "--limit", "10", "--json")
	var events []map[string]any
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("parse history: %v\n%s", err, out)
	}
	if len(events) == 0 {
		t.Error("an ordinary uninstall must keep the record of what was decided")
	}
}

func TestS13UninstallPurgeRemovesTheDataButNotTheSettings(t *testing.T) {
	env := NewEnv(t)
	fakeClaudeShim(t, env)
	env.MustCLI("setup", "claude", "--no-service")

	out, errOut, code := env.CLI("uninstall", "claude", "--purge")
	if code != 0 {
		t.Fatalf("uninstall exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	env.ForgetDaemon()

	if _, err := os.Stat(filepath.Join(env.DataDir, "intenter.db")); !os.IsNotExist(err) {
		t.Errorf("--purge must remove the database, got %v", err)
	}
	if _, err := os.Stat(claudeSettingsPath(env)); err != nil {
		t.Errorf("the Claude settings must survive --purge: %v", err)
	}
}

func TestS13SetupDryRunChangesNothing(t *testing.T) {
	env := NewEnv(t)
	fakeClaudeShim(t, env)

	settingsPath := claudeSettingsPath(env)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := `{"model":"claude-opus-5"}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	out, errOut, code := env.CLI("setup", "claude", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "Nothing was changed") {
		t.Errorf("a dry run must say so:\n%s", out)
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != original {
		t.Errorf("the settings were modified during a dry run:\n%s", content)
	}
}

func TestS13SetupFailsWithoutClaude(t *testing.T) {
	// Exit 3 marks a setup step failing (contracts/cli.md).
	env := NewEnv(t)
	env.HideRealClaude()

	out, errOut, code := env.CLI("setup", "claude", "--no-service")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "✗ Claude Code detected") {
		t.Errorf("the failing step must be marked:\n%s", out)
	}
	if !strings.Contains(errOut, "not found") {
		t.Errorf("stderr = %q, want it to say what is missing", errOut)
	}
}
