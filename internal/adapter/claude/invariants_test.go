package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/logging"
)

// The invariant index for the Claude adapter: I-8, I-9, I-12.
// See internal/approval/invariants_test.go for what this index is for.

func TestInvariant_I8_TheAdapterNeverTurnsARuleIntoAnApproval(t *testing.T) {
	// I-8: agent string rules MUST NOT become Intenter approvals without full
	// resolution and policy validation in the daemon.
	//
	// A Claude rule is a string, and a string cannot say what a command will do
	// today. The adapter's job is to report that the user once said yes to that
	// string; deciding what it means is the daemon's, after resolving it.
	stub := &stubDaemon{t: t}
	home := t.TempDir()
	project := filepath.Join(home, "projects", "demo")

	for _, dir := range []string{filepath.Join(project, ".git"), filepath.Join(project, ".claude")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	// A rule broad enough to cover anything, which is exactly why the adapter
	// must not act on it.
	settings := `{"permissions":{"allow":["Bash(npm run cleanup)","Bash(rm:*)"]}}`
	if err := os.WriteFile(filepath.Join(project, ".claude", "settings.local.json"),
		[]byte(settings), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	a := newAdapterWithHome(t, stub, home)
	runHook(t, a, payload(t, "posttooluse_bash.json"), map[string]string{EnvProjectDir: project})

	if len(stub.executeCalls) != 1 {
		t.Fatalf("report_execution calls = %d, want 1", len(stub.executeCalls))
	}
	consent := stub.executeCalls[0].AgentConsent
	if consent == nil {
		t.Fatal("the matching rule must be reported as a consent signal")
	}

	// What crosses the boundary is a claim about consent, not a permission.
	if consent.Kind != action.ConsentKindPersistentRule {
		t.Errorf("kind = %q, want a consent signal", consent.Kind)
	}
	if len(consent.RuleKeys) == 0 {
		t.Error("consent must name the rules it came from, so the daemon can record them")
	}

	// And the adapter creates nothing on its own: no approval call exists in
	// the IPC surface it uses.
	for _, call := range stub.evaluateCalls {
		if call.AgentConsent != nil && call.AgentConsent.Kind != action.ConsentKindPersistentRule {
			t.Errorf("consent kind = %q; the adapter may only report persistent rules", call.AgentConsent.Kind)
		}
	}

	// A rule that only partly covers the command is not consent at all: a
	// partial match is a guess, and a guess must not become permission.
	partial := `{"permissions":{"allow":["Bash(npm run build)"]}}`
	if err := os.WriteFile(filepath.Join(project, ".claude", "settings.local.json"),
		[]byte(partial), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	second := &stubDaemon{t: t}
	b := newAdapterWithHome(t, second, t.TempDir())
	runHook(t, b, payload(t, "posttooluse_bash.json"), map[string]string{EnvProjectDir: project})

	if len(second.executeCalls) != 1 {
		t.Fatalf("report_execution calls = %d, want 1", len(second.executeCalls))
	}
	if got := second.executeCalls[0].AgentConsent; got != nil {
		t.Errorf("consent = %+v, want none when no rule covers the command", got)
	}
}

func TestInvariant_I9_SetupNeverDestroysWhatItDidNotWrite(t *testing.T) {
	// I-9: setup/uninstall MUST NOT delete or rewrite settings Intenter did
	// not create, and MUST back up before modifying.
	//
	// The settings file is the user's, and it holds things Intenter has never
	// heard of. Editing it is a privilege that lasts exactly as long as the
	// file comes back intact.
	//
	// See also TestInstallHooksPreservesEverythingElse, TestUninstallLeavesTheFileEquivalent.
	path := settingsFixture(t, userSettings)
	before := readTree(t, path)

	if err := InstallHooks(path, testExecutable, "darwin", true); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	after := readTree(t, path)

	// Everything that is not the hooks block survives byte for byte.
	for key, want := range before {
		if key == "hooks" {
			continue
		}
		if !reflect.DeepEqual(after[key], want) {
			t.Errorf("setup changed %q:\n got %#v\nwant %#v", key, after[key], want)
		}
	}
	if _, present := after["someFutureKey"]; !present {
		t.Error("a key Intenter does not model must survive an edit")
	}

	// The user's own hooks are still there, alongside Intenter's.
	foreign := findHookCommand(t, path, "PreToolUse", "/usr/bin/my-linter")
	if !foreign {
		t.Error("the user's own PreToolUse hook was removed")
	}
	if session := findHookCommand(t, path, "SessionStart", "/usr/bin/greet"); !session {
		t.Error("a hook on an event Intenter does not use was removed")
	}

	// And uninstall returns the file to something equivalent, keeping the rest.
	if err := RemoveHooks(path); err != nil {
		t.Fatalf("remove hooks: %v", err)
	}
	restored := readTree(t, path)
	for key, want := range before {
		if !reflect.DeepEqual(restored[key], want) {
			t.Errorf("uninstall did not restore %q:\n got %#v\nwant %#v", key, restored[key], want)
		}
	}
}

func TestInvariant_I9_ModifyingIsAlwaysPrecededByABackup(t *testing.T) {
	// The second half of I-9. A backup taken after the edit is not a backup.
	dataDir := t.TempDir()
	path := settingsFixture(t, userSettings)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	backupPath, err := BackupSettings(path, dataDir, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := InstallHooks(path, testExecutable, "darwin", false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	saved, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(saved) != string(original) {
		t.Error("the backup must hold the file as it was before the edit")
	}
}

func TestInvariant_I9_UnreadableSettingsAreLeftAlone(t *testing.T) {
	// The file may be hand-edited and briefly invalid. Overwriting it then
	// would destroy work Intenter cannot even read, which is the worst
	// possible moment to be confident.
	//
	// See also TestInvalidJSONIsNeverOverwritten.
	broken := `{"model": "claude-opus-5", "hooks": {`
	path := settingsFixture(t, broken)

	if err := InstallHooks(path, testExecutable, "darwin", false); err == nil {
		t.Error("install must refuse a file it cannot parse")
	}
	if err := RemoveHooks(path); err == nil {
		t.Error("uninstall must refuse a file it cannot parse")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != broken {
		t.Errorf("the file was modified:\n got %q\nwant %q", after, broken)
	}
}

func TestInvariant_I12_NoFailurePathProducesAllow(t *testing.T) {
	// I-12: no failure path in adapter or daemon may produce `allow` output.
	//
	// The failure mode this rules out is the quiet one: Intenter breaks, the
	// hook says "allow", and the session runs wide open while looking guarded.
	// Deferring instead leaves the agent exactly as safe as it was without
	// Intenter installed.
	//
	// See also hook_failure_test.go, which covers §26 in full.
	events := []string{"pretooluse_bash.json", "permissionrequest_bash.json", "posttooluse_bash.json"}

	// Every way the hook's own work can fail.
	adapters := map[string]func(*testing.T) *Adapter{
		"the daemon is unreachable": unreachableAdapter,
		"the daemon errors": func(t *testing.T) *Adapter {
			a, _ := newTestAdapter(t, &stubDaemon{t: t, evaluateErr: errors.New("INTERNAL: broken")})
			return a
		},
		"the daemon answers with nothing": func(t *testing.T) *Adapter {
			a, _ := newTestAdapter(t, &stubDaemon{t: t})
			return a
		},
	}

	for name, build := range adapters {
		for _, event := range events {
			t.Run(name+"/"+event, func(t *testing.T) {
				decoded := runHook(t, build(t), payload(t, event), nil)
				if decoded == nil {
					return
				}
				rendered, err := json.Marshal(decoded)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				if strings.Contains(string(rendered), `"allow"`) {
					t.Errorf("a failure produced an allow: %s", rendered)
				}
				if output := hookOutput(decoded); output != nil {
					if _, present := output["permissionDecision"]; present {
						t.Errorf("a failure produced a decision: %s", rendered)
					}
					if _, present := output["decision"]; present {
						t.Errorf("a failure produced a decision: %s", rendered)
					}
				}
			})
		}
	}
}

func TestInvariant_I12_UnusableInputProducesNoDecision(t *testing.T) {
	// The other class of failure: the payload itself is not something the
	// adapter can read. Guessing at it would be deciding on a command nobody
	// established.
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeAllow,
		Class:        action.ClassApprovalMatch,
		Reason:       "approved",
	}}

	payloads := map[string]string{
		"empty":            "",
		"not JSON":         "this is not json",
		"an array":         `["PreToolUse"]`,
		"no event name":    `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`,
		"an unknown event": `{"hook_event_name":"SomethingNew","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`,
		"no command":       `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{}}`,
	}

	for name, raw := range payloads {
		t.Run(name, func(t *testing.T) {
			a, _ := newTestAdapter(t, stub)
			decoded := runHook(t, a, raw, nil)
			if output := hookOutput(decoded); output != nil {
				t.Errorf("unusable input produced %+v", output)
			}
		})
	}
}

// newAdapterWithHome builds an adapter rooted at a specific home directory.
func newAdapterWithHome(t *testing.T, stub *stubDaemon, home string) *Adapter {
	t.Helper()
	p := fakePlatform{home: home, runtimeDir: filepath.Join(home, "run")}
	return New(p, config.Default(), logging.Discard()).WithClient(stub.serve(t))
}

// findHookCommand reports whether a hook with the given command is still
// installed for an event.
func findHookCommand(t *testing.T, settingsPath, event, command string) bool {
	t.Helper()
	tree := readTree(t, settingsPath)

	hooks, _ := tree["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		inner, _ := entry["hooks"].([]any)
		for _, rawHook := range inner {
			hook, _ := rawHook.(map[string]any)
			if text, _ := hook["command"].(string); strings.Contains(text, command) {
				return true
			}
		}
	}
	return false
}
