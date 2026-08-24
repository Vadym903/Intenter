package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// S7, S8 and S9 (PROTOTYPE_SPEC.md §29) are the safety floor: the commands that
// must never be allowed however they are spelled, wherever they are approved,
// and whatever mode the agent is in.

func TestS7CatastrophicDeletesAreBlocked(t *testing.T) {
	env := NewEnv(t)

	tests := []struct {
		name    string
		command string
		rule    string
	}{
		{"home documents", "rm -rf ~/Documents", "R2"},
		{"the home directory itself", "rm -rf ~", "R2"},
		{"everything in home", "rm -rf ~/*", "R2"},
		{"ssh keys", "rm -rf ~/.ssh", "R2"},
		{"the filesystem root", "rm -rf /", "R1"},
		{"a system directory", "rm -rf " + systemDirectory(), "R1"},
		{"home via a variable", "rm -rf $HOME/Documents", "R2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := env.PreToolUse("session-1", "toolu_"+tt.name, tt.command)
			if got := result.PermissionDecision(); got != "deny" {
				t.Fatalf("permissionDecision = %q, want deny\n%s", got, result.Stdout)
			}
			if !strings.Contains(result.SystemMessage(), "Intenter BLOCK") {
				t.Errorf("systemMessage = %q, want the block notice", result.SystemMessage())
			}

			event := env.FullEvent(env.LatestEventID())
			if event["hard_rule"] != tt.rule {
				t.Errorf("hard_rule = %v, want %s", event["hard_rule"], tt.rule)
			}
			if event["decision"] != "block" {
				t.Errorf("decision = %v, want block", event["decision"])
			}
		})
	}
}

// systemDirectory is an operating-system directory spelled the way Claude's
// shell on this host would spell it: a POSIX path, or on Windows the Git Bash
// form of a path under %SystemRoot% (`/usr/local/bin` has no owner there).
func systemDirectory() string {
	if runtime.GOOS != "windows" {
		return "/usr/local/bin"
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	// C:\Windows\System32 -> /c/Windows/System32
	return "/" + strings.ToLower(root[:1]) + filepath.ToSlash(root[2:]) + "/System32"
}

func TestS7BlocksHoldInBypassMode(t *testing.T) {
	// §11.3: bypassPermissions turns off the prompts, not the safety floor.
	env := NewEnv(t)

	for _, command := range []string{"rm -rf ~/Documents", "rm -rf /", "rm -rf ~"} {
		result := env.PreToolUseMode("session-1", "toolu_"+command, "bypassPermissions", command)
		if got := result.PermissionDecision(); got != "deny" {
			t.Errorf("%q: permissionDecision = %q, want deny even in bypass mode\n%s",
				command, got, result.Stdout)
		}
	}
}

func TestS7BlockedActionsCannotBeApproved(t *testing.T) {
	// §19.3: there is no way to record consent for something the floor stops,
	// so an approval for it can never exist to be matched later.
	env := NewEnv(t)

	env.PreToolUse("session-1", "toolu_1", "rm -rf ~/Documents")
	eventID := env.LatestEventID()

	_, stderr, code := env.CLI("approve", itoa(eventID))
	if code == 0 {
		t.Fatal("approving a blocked action must fail")
	}
	if !strings.Contains(stderr, "safety rule") {
		t.Errorf("stderr = %q, want the refusal reason", stderr)
	}
	if count := env.ApprovalCount(); count != 0 {
		t.Errorf("approvals = %d, want none", count)
	}
}

func TestS7WindowsDialectsAreBlockedToo(t *testing.T) {
	// The same intent expressed in PowerShell or cmd reaches the same rule,
	// because the effect model is shared (§14.4).
	env := NewEnv(t)

	tests := []struct {
		name    string
		tool    string
		command string
	}{
		{"powershell", "PowerShell", `Remove-Item -Recurse -Force $env:USERPROFILE\Documents`},
		{"powershell alias", "PowerShell", `rm -Recurse -Force ~/Documents`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := env.RunHook(map[string]any{
				"hook_event_name": "PreToolUse",
				"session_id":      "session-1",
				"cwd":             env.Workspace,
				"permission_mode": "default",
				"tool_name":       tt.tool,
				"tool_use_id":     "toolu_" + tt.name,
				"tool_input":      map[string]any{"command": tt.command},
			})
			if got := result.PermissionDecision(); got != "deny" {
				t.Fatalf("permissionDecision = %q, want deny\n%s", got, result.Stdout)
			}
		})
	}
}

func TestS7SensitiveFilesAreProtected(t *testing.T) {
	// R5: reading a credential asks; changing one is blocked.
	env := NewEnv(t)
	env.WriteWorkspaceFile(".env", "SECRET=1\n")

	read := env.PreToolUse("session-1", "toolu_1", "cat ./.env")
	if got := read.PermissionDecision(); got != "ask" {
		t.Fatalf("reading a credential file = %q, want ask\n%s", got, read.Stdout)
	}
	if event := env.FullEvent(env.LatestEventID()); event["hard_rule"] != "R5" {
		t.Errorf("hard_rule = %v, want R5", event["hard_rule"])
	}

	write := env.PreToolUse("session-1", "toolu_2", "rm ./.env")
	if got := write.PermissionDecision(); got != "deny" {
		t.Errorf("deleting a credential file = %q, want deny\n%s", got, write.Stdout)
	}
}

func TestS8TraversalOutOfTheWorkspaceIsNeverSilent(t *testing.T) {
	env := NewEnv(t)

	tests := []struct {
		name     string
		command  string
		decision string
	}{
		{"recursive delete outside", "rm -rf ./dist/../../other", "deny"},
		{"read outside", "cat ../../../etc/passwd", "ask"},
		{"copy outside", "cp ./README.md ../../outside/", "ask"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := env.PreToolUse("session-1", "toolu_"+tt.name, tt.command)
			if got := result.PermissionDecision(); got != tt.decision {
				t.Fatalf("permissionDecision = %q, want %q\n%s", got, tt.decision, result.Stdout)
			}
			if got := result.PermissionDecision(); got == "allow" {
				t.Fatal("a path leaving the workspace is never silently allowed")
			}
		})
	}
}

func TestS8TraversalIsVisibleInTheExplanation(t *testing.T) {
	env := NewEnv(t)

	env.PreToolUse("session-1", "toolu_1", "cat ../../../etc/passwd")
	explanation := env.MustCLI("history", "show", itoa(env.LatestEventID()))

	if !strings.Contains(explanation, "traversal") {
		t.Errorf("history show must name the traversal:\n%s", explanation)
	}
}

func TestS9SymlinkEscapeIsClassifiedByWhereItLands(t *testing.T) {
	// INVARIANT I-14: a link inside the workspace pointing at the home
	// directory is a delete in HOME, whatever the path looks like.
	env := NewEnv(t)

	documents := filepath.Join(env.Home, "Documents")
	if err := os.MkdirAll(documents, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(env.Workspace, "build", "link")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(documents, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	for _, command := range []string{
		"rm -rf build/link",
		"rm -rf build/link/",
	} {
		t.Run(command, func(t *testing.T) {
			result := env.PreToolUse("session-1", "toolu_"+command, command)
			if got := result.PermissionDecision(); got == "allow" {
				t.Fatalf("a link out of the workspace must never be allowed\n%s", result.Stdout)
			}

			event := env.FullEvent(env.LatestEventID())
			resolved, _ := event["resolved"].(map[string]any)
			if resolved == nil {
				t.Fatalf("event = %+v, want the resolved action", event)
			}
			encoded := strings.ToUpper(env.MustCLI("history", "show", itoa(env.LatestEventID())))
			if strings.Contains(encoded, "WORKSPACE_GENERATED") {
				t.Error("an escaping link must never be classified as build output (I-14)")
			}
		})
	}
}

func TestS9WildcardOverAnEscapingLinkIsCaught(t *testing.T) {
	// `rm -rf build/*` classifies by the literal prefix, which alone would miss
	// an entry that links out of the workspace (§16.1 step 7).
	env := NewEnv(t)

	documents := filepath.Join(env.Home, "Documents")
	if err := os.MkdirAll(documents, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	buildDir := filepath.Join(env.Workspace, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(documents, filepath.Join(buildDir, "link")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	result := env.PreToolUse("session-1", "toolu_1", "rm -rf build/*")
	if got := result.PermissionDecision(); got == "allow" {
		t.Fatalf("a wildcard covering an escaping link must not be allowed\n%s", result.Stdout)
	}
}

func TestS9OrdinaryBuildOutputIsStillOrdinary(t *testing.T) {
	// The control: without a link, deleting build output is what it looks like.
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)

	result := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if got := result.PermissionDecision(); got == "deny" {
		t.Fatalf("deleting build output is not catastrophic\n%s", result.Stdout)
	}

	explanation := env.MustCLI("history", "show", itoa(env.LatestEventID()))
	if !strings.Contains(explanation, "WORKSPACE_GENERATED") {
		t.Errorf("build output must be classified as generated:\n%s", explanation)
	}
}
