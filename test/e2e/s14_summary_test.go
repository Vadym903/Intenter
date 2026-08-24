package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// S14: what Intenter did is countable — per session at the moment a session
// ends, and on demand through `intenter summary`. The numbers come from the
// same audit rows every other view is built from, so they cannot disagree with
// `intenter history`.

func TestS14SessionEndReportsTheSession(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	// One approved command, run twice: the second is the allow that an approval
	// answered for the user.
	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approve", itoa(env.LatestEventID()))
	env.PreToolUse("session-1", "toolu_2", "npm run cleanup")
	// A baseline read, and something refused outright.
	env.PreToolUse("session-1", "toolu_3", "git status")
	env.PreToolUse("session-1", "toolu_4", "rm -rf ~/Documents")

	result := env.SessionEnd("session-1", "prompt_input_exit")
	message := result.SystemMessage()
	if message == "" {
		t.Fatalf("want a session summary, got %s", result.Stdout)
	}

	for _, want := range []string{
		"Intenter this session",
		"4 commands checked",
		"1 blocked",
		"1 prompt you did not have to answer",
		"intenter summary",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("summary must mention %q:\n%s", want, message)
		}
	}

	// SessionEnd cannot decide anything; emitting a decision there would be a
	// protocol error rather than an ignored field.
	if !result.Deferred() {
		t.Errorf("SessionEnd must carry no permission decision: %s", result.Stdout)
	}
}

func TestS14SessionEndCountsOnlyItsOwnSession(t *testing.T) {
	env := NewEnv(t)

	env.PreToolUse("session-1", "toolu_1", "git status")
	env.PreToolUse("session-2", "toolu_2", "git status")
	env.PreToolUse("session-2", "toolu_3", "git diff")

	message := env.SessionEnd("session-2", "clear").SystemMessage()
	if !strings.Contains(message, "2 commands checked") {
		t.Errorf("want only session-2's two commands:\n%s", message)
	}
}

func TestS14ASessionThatDecidedNothingIsSilent(t *testing.T) {
	env := NewEnv(t)

	result := env.SessionEnd("session-never-used", "logout")
	if !result.Silent() {
		t.Errorf("want no output for a session with no decisions, got %s", result.Stdout)
	}
}

func TestS14SummaryCountsWhatTheHistoryShows(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approve", itoa(env.LatestEventID()))
	env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	env.PreToolUse("session-2", "toolu_3", "git status")
	env.PreToolUse("session-2", "toolu_4", "rm -rf ~/Documents")

	out := env.MustCLI("summary")
	for _, want := range []string{"commands", "allowed", "asked", "blocked", "prompt"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary must report %q:\n%s", want, out)
		}
	}

	// The one number the command exists to report.
	if !strings.Contains(out, "1 prompt you did not have to answer") {
		t.Errorf("summary must name the prompts an approval answered:\n%s", out)
	}
	if !strings.Contains(out, "by approval") {
		t.Errorf("summary must split the allows by what decided them:\n%s", out)
	}
}

func TestS14SummaryScopesToASession(t *testing.T) {
	env := NewEnv(t)

	env.PreToolUse("session-1", "toolu_1", "git status")
	env.PreToolUse("session-2", "toolu_2", "git status")

	out := env.MustCLI("summary", "--session", "session-2", "--json")
	if !strings.Contains(out, `"total": 1`) && !strings.Contains(out, `"total":1`) {
		t.Errorf("want one event for session-2:\n%s", out)
	}
}

func TestS14SummaryOfAnEmptyPeriodSaysSo(t *testing.T) {
	env := NewEnv(t)

	out := env.MustCLI("summary")
	if !strings.Contains(out, "Nothing decided") {
		t.Errorf("an empty period must say so rather than print zeros:\n%s", out)
	}
}

func TestS14DoctorReportsAHookAnUpgradeDidNotAdd(t *testing.T) {
	// An installation from before an event was added keeps gating commands
	// perfectly, so nothing else reports the gap — whatever depends on that
	// event simply never happens. Only doctor can catch it.
	env := NewEnv(t)
	fakeClaudeShim(t, env)
	env.MustCLI("setup", "claude", "--no-service")

	// What an older setup left behind: every hook but the newest.
	path := claudeSettingsPath(env)
	tree := readSettings(t, path)
	hooks, ok := tree["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("setup wrote no hooks: %#v", tree)
	}
	if _, present := hooks["SessionEnd"]; !present {
		t.Fatal("setup must install the SessionEnd hook")
	}
	delete(hooks, "SessionEnd")
	writeJSON(t, path, tree)

	out, errOut, code := env.CLI("doctor")
	combined := out + errOut
	if code == 0 {
		t.Errorf("doctor must fail while a required hook is missing:\n%s", combined)
	}
	if !strings.Contains(combined, "SessionEnd") {
		t.Errorf("doctor must name the missing hook:\n%s", combined)
	}
	if !strings.Contains(combined, "intenter setup claude") {
		t.Errorf("doctor must print the fix:\n%s", combined)
	}
}

// writeJSON replaces a settings file with a modified tree.
func writeJSON(t *testing.T, path string, tree map[string]any) {
	t.Helper()
	content, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
