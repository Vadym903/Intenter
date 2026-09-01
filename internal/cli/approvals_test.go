package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/daemon"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
)

// cliFixture is an isolated environment with a running daemon and a workspace.
type cliFixture struct {
	base      string
	home      string
	workspace string
	client    *ipc.Client

	stop    func()
	stopped bool
	done    chan error
}

// stopFixtureDaemon shuts the daemon down so a test can exercise the paths that
// still have to work without it.
func stopFixtureDaemon(t *testing.T, f *cliFixture) {
	t.Helper()
	if f.stopped {
		return
	}
	f.stopped = true
	f.stop()
	select {
	case err := <-f.done:
		if err != nil {
			t.Fatalf("daemon.Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

// writeFile replaces a file in the fixture workspace.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// startFixture isolates the CLI, creates a project and runs a daemon for it.
func startFixture(t *testing.T) *cliFixture {
	t.Helper()
	base := isolate(t)

	p, err := platform.New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}
	if err := os.MkdirAll(p.HomeDir(), 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}

	workspace := filepath.Join(p.HomeDir(), "projects", "demo")
	for _, dir := range []string{filepath.Join(workspace, ".git"), filepath.Join(workspace, "dist")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	manifest := `{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}`
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	ready := make(chan struct{})
	instance, err := daemon.New(daemon.Options{
		Platform: p,
		Config:   config.Default(),
		Logger:   logging.Discard(),
		Ready:    ready,
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.Run(ctx) }()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited during startup: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not become ready")
	}
	fixture := &cliFixture{
		base:      base,
		home:      p.HomeDir(),
		workspace: resolved,
		client:    ipc.NewClient(instance.Endpoint()),
		stop:      cancel,
		done:      done,
	}

	t.Cleanup(func() {
		if fixture.stopped {
			return
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon.Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not stop")
		}
	})

	return fixture
}

// evaluate submits one command and returns the recorded audit event id.
func (f *cliFixture) evaluate(t *testing.T, command, toolUseID string) action.EvaluationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var result action.EvaluationResult
	err := f.client.Call(ctx, ipc.MethodEvaluate, ipc.EvaluateParams{
		Request: action.ActionRequest{
			Agent:      "claude",
			SessionID:  "session-1",
			ToolUseID:  toolUseID,
			Tool:       "Bash",
			Dialect:    action.DialectPosix,
			RawCommand: command,
			Cwd:        f.workspace,
		},
	}, &result)
	if err != nil {
		t.Fatalf("evaluate %q: %v", command, err)
	}
	return result
}

// inWorkspace runs a CLI command with the workspace as the working directory,
// which is how `approvals` and `history --project` scope themselves.
func (f *cliFixture) inWorkspace(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(f.workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("chdir back: %v", err)
		}
	}()
	return runCLI(t, args...)
}

func TestApprovalsListingIsEmptyAtFirst(t *testing.T) {
	f := startFixture(t)

	out, _, code := f.inWorkspace(t, "approvals")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	// "Trusted" rather than "approved": the listing now covers the rules Claude
	// holds of its own as well, and those were never approved by anyone here.
	if !strings.Contains(out, "Nothing is trusted in this project yet") {
		t.Errorf("output = %q, want the empty-state message", out)
	}
	if !strings.Contains(out, "intenter approve") {
		t.Error("the empty state must say how a permission comes to exist")
	}
	if !strings.Contains(out, "don't ask again") {
		t.Error("the empty state must name the way most permissions are actually created")
	}
}

func TestApproveThenListAndShow(t *testing.T) {
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	if evaluated.AuditEventID == nil {
		t.Fatal("want an audit event")
	}
	eventID := *evaluated.AuditEventID

	out, _, code := f.inWorkspace(t, "approve", itoa(eventID))
	if code != ExitOK {
		t.Fatalf("approve exit code = %d\n%s", code, out)
	}
	for _, want := range []string{"Approved as exact approval", "trusted:", "valid while these stay unchanged", "npm-script:package.json#scripts.cleanup"} {
		if !strings.Contains(out, want) {
			t.Errorf("approve output missing %q:\n%s", want, out)
		}
	}

	listed, _, code := f.inWorkspace(t, "approvals")
	if code != ExitOK {
		t.Fatalf("approvals exit code = %d", code)
	}
	for _, want := range []string{"ID", "KIND", "ACTION", "TRUSTED", "USES", "STATE", "ORIGIN", "RUN_SCRIPT", "EXACT", "./dist"} {
		if !strings.Contains(listed, want) {
			t.Errorf("approvals output missing %q:\n%s", want, listed)
		}
	}
	// Scoped to one project, the path is a footer rather than a repeated column.
	if !strings.Contains(listed, "Project: "+f.workspace) {
		t.Errorf("approvals output must name the project:\n%s", listed)
	}

	acrossProjects, _, code := f.inWorkspace(t, "approvals", "--all")
	if code != ExitOK {
		t.Fatalf("approvals --all exit code = %d", code)
	}
	if !strings.Contains(acrossProjects, "PROJECT") {
		t.Errorf("listing across projects needs the project column:\n%s", acrossProjects)
	}

	detail, _, code := f.inWorkspace(t, "approval", "show", "1")
	if code != ExitOK {
		t.Fatalf("approval show exit code = %d\n%s", code, detail)
	}
	for _, want := range []string{
		"Approval 1", "EXACT", "ACTIVE", "action:", "effects:", "targets:",
		"./dist", "valid while unchanged", "origin:", "created by: npm run cleanup",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("approval show output missing %q:\n%s", want, detail)
		}
	}
}

func TestApprovalsJSONShape(t *testing.T) {
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	out, _, code := f.inWorkspace(t, "approvals", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	// The default array now carries both sources, so every element says which
	// one it is. This is the documented breaking change.
	var entries []permissionEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("output is not a JSON array of permissions: %v\n%s", err, out)
	}
	if len(entries) != 1 {
		t.Fatalf("permissions = %d, want 1", len(entries))
	}
	if entries[0].Source != sourceApproval || entries[0].Approval == nil {
		t.Fatalf("entry = %+v, want an approval", entries[0])
	}
	if entries[0].Approval.Kind != action.ApprovalExact {
		t.Errorf("kind = %s", entries[0].Approval.Kind)
	}
	if entries[0].Approval.ProjectRoot != f.workspace {
		t.Errorf("project root = %q, want %q", entries[0].Approval.ProjectRoot, f.workspace)
	}
}

// TestApprovalsJSONCompatibilityFlag pins the escape hatch the changelog
// promises: anything that parsed the old array keeps working with one flag.
func TestApprovalsJSONCompatibilityFlag(t *testing.T) {
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	out, _, code := f.inWorkspace(t, "approvals", "--source", "approval", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	var summaries []ipc.ApprovalSummary
	if err := json.Unmarshal([]byte(out), &summaries); err != nil {
		t.Fatalf("--source approval must reproduce the previous array: %v\n%s", err, out)
	}
	if len(summaries) != 1 || summaries[0].Kind != action.ApprovalExact {
		t.Fatalf("summaries = %+v", summaries)
	}
	if summaries[0].ProjectRoot != f.workspace {
		t.Errorf("project root = %q, want %q", summaries[0].ProjectRoot, f.workspace)
	}
}

func TestApproveRejectsWhatCannotBeApproved(t *testing.T) {
	f := startFixture(t)

	blocked := f.evaluate(t, "rm -rf ~/Documents", "toolu_1")
	out, errOut, code := f.inWorkspace(t, "approve", itoa(*blocked.AuditEventID))
	if code == ExitOK {
		t.Fatalf("a blocked action must not be approvable\n%s", out)
	}
	if !strings.Contains(errOut, "safety rule") {
		t.Errorf("stderr = %q, want the refusal reason", errOut)
	}

	unresolved := f.evaluate(t, "some-unknown-tool", "toolu_2")
	_, errOut, code = f.inWorkspace(t, "approve", itoa(*unresolved.AuditEventID))
	if code == ExitOK {
		t.Fatal("an unresolved action must not be approvable")
	}
	if !strings.Contains(errOut, "status") {
		t.Errorf("stderr = %q, want the status in the reason", errOut)
	}
}

func TestApprovalStateTransitions(t *testing.T) {
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	out, _, code := f.inWorkspace(t, "approval", "disable", "1")
	if code != ExitOK || !strings.Contains(out, "disabled") {
		t.Fatalf("disable = %q (exit %d)", out, code)
	}

	out, _, code = f.inWorkspace(t, "approval", "enable", "1")
	if code != ExitOK || !strings.Contains(out, "enabled") {
		t.Fatalf("enable = %q (exit %d)", out, code)
	}

	// Revoke changes a permission, so it shows the plan and asks first. With
	// nothing to read an answer from, it must refuse rather than assume yes.
	_, errOut, code := f.inWorkspace(t, "approval", "revoke", "1")
	if code == ExitOK {
		t.Error("revoke must not proceed without a confirmation it could not ask for")
	}
	if !strings.Contains(errOut, "--yes") {
		t.Errorf("the refusal must say how to proceed: %q", errOut)
	}

	out, _, code = f.inWorkspace(t, "approval", "revoke", "1", "--yes")
	if code != ExitOK || !strings.Contains(out, "revoked") {
		t.Fatalf("revoke = %q (exit %d)", out, code)
	}
	if !strings.Contains(out, "This will stop being trusted") {
		t.Error("revoke must show what it is about to change, even with --yes")
	}
	if !strings.Contains(out, "kept for the history") {
		t.Error("revoke must say the record survives")
	}
	if !strings.Contains(out, "will ask again") {
		t.Error("revoke must say what happens next")
	}

	// Revocation is terminal.
	_, _, code = f.inWorkspace(t, "approval", "enable", "1")
	if code == ExitOK {
		t.Error("a revoked approval must not be re-enabled (I-15)")
	}

	// The record survives, which is what makes the history readable.
	listed, _, _ := f.inWorkspace(t, "approvals", "--inactive")
	if !strings.Contains(listed, "REVOKED") {
		t.Errorf("a revoked approval is still listed with --inactive:\n%s", listed)
	}
}

func TestSemanticApprovalIsOptIn(t *testing.T) {
	f := startFixture(t)
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")

	out, _, code := f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID), "--semantic", "--note", "build output only")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if !strings.Contains(out, "semantic approval") {
		t.Errorf("output = %q, want a semantic approval", out)
	}

	detail, _, _ := f.inWorkspace(t, "approval", "show", "1", "--json")
	var decoded ipc.ApprovalDetail
	if err := json.Unmarshal([]byte(detail), &decoded); err != nil {
		t.Fatalf("approval show --json: %v", err)
	}
	if decoded.Approval.Kind != action.ApprovalSemantic {
		t.Errorf("kind = %s, want SEMANTIC", decoded.Approval.Kind)
	}
	if len(decoded.Approval.Targets) != 0 {
		t.Errorf("a semantic approval pins no targets, got %v", decoded.Approval.Targets)
	}
	if decoded.Approval.Note != "build output only" {
		t.Errorf("note = %q", decoded.Approval.Note)
	}
}

func TestApprovalCommandsRejectBadIDs(t *testing.T) {
	startFixture(t)

	for _, args := range [][]string{
		{"approve", "zero"},
		{"approve", "0"},
		{"approval", "show", "1x"},
		{"history", "show", "abc"},
	} {
		_, errOut, code := runCLI(t, args...)
		if code == ExitOK {
			t.Errorf("%v: want a failure", args)
		}
		if !strings.Contains(errOut, "not a valid id") {
			t.Errorf("%v: stderr = %q", args, errOut)
		}
	}
}

func TestCommandsReportAnUnreachableDaemon(t *testing.T) {
	// Exit code 2 is reserved for "the daemon is not running" (contracts/cli.md).
	isolate(t)

	for _, args := range [][]string{
		{"approvals"},
		{"approval", "show", "1"},
		{"approve", "1"},
	} {
		_, errOut, code := runCLI(t, args...)
		if code != ExitDaemonUnreached {
			t.Errorf("%v: exit code = %d, want %d", args, code, ExitDaemonUnreached)
		}
		if !strings.Contains(errOut, "daemon") {
			t.Errorf("%v: stderr = %q", args, errOut)
		}
	}
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

// approveCleanup approves `npm run cleanup` and returns the approval id.
func approveCleanup(t *testing.T, f *cliFixture) string {
	t.Helper()
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	if evaluated.AuditEventID == nil {
		t.Fatal("want an audit event")
	}
	out, _, code := f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))
	if code != ExitOK {
		t.Fatalf("approve failed (%d):\n%s", code, out)
	}
	return "1"
}

// This is the point of the whole feature. Revoking Intenter's own record while
// a rule in Claude's settings still grants the command leaves it running
// silently — the gate hands a command with no matching approval back to Claude,
// which then allows it. A revoke that stops at the database is not a revoke.
func TestRevokeRemovesTheAgentRuleThatGrantsTheSameCommand(t *testing.T) {
	f := startFixture(t)
	settings := filepath.Join(f.home, ".claude", "settings.json")
	writeAllowRules(t, settings, "Bash(npm run cleanup)", "Bash(git status)")
	id := approveCleanup(t, f)

	out, _, code := f.inWorkspace(t, "approval", "revoke", id, "--yes")
	if code != ExitOK {
		t.Fatalf("revoke exit = %d\n%s", code, out)
	}
	for _, want := range []string{
		"This will stop being trusted",
		"Bash(npm run cleanup)",
		"Removed Bash(npm run cleanup)",
		"will ask again",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("revoke output missing %q:\n%s", want, out)
		}
	}

	content, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(content), "npm run cleanup") {
		t.Errorf("the rule that grants the command is still there:\n%s", content)
	}
	if !strings.Contains(string(content), "Bash(git status)") {
		t.Errorf("an unrelated rule was removed:\n%s", content)
	}
}

func TestRevokeKeepAgentRulesLeavesThemAlone(t *testing.T) {
	f := startFixture(t)
	settings := filepath.Join(f.home, ".claude", "settings.json")
	writeAllowRules(t, settings, "Bash(npm run cleanup)")
	id := approveCleanup(t, f)

	out, _, code := f.inWorkspace(t, "approval", "revoke", id, "--keep-agent-rules", "--yes")
	if code != ExitOK {
		t.Fatalf("revoke exit = %d\n%s", code, out)
	}
	content, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !strings.Contains(string(content), "npm run cleanup") {
		t.Error("--keep-agent-rules must leave Claude's own rules in place")
	}
}

// A rule shared through the repository is not this user's to remove. Saying the
// permission is gone when it is not would be the worst possible outcome, so the
// outcome has to say the command still runs.
func TestRevokeNamesARuleItCannotChangeAndSaysTheCommandStillRuns(t *testing.T) {
	f := startFixture(t)
	shared := filepath.Join(f.workspace, ".claude", "settings.json")
	writeAllowRules(t, shared, "Bash(npm run cleanup)")
	id := approveCleanup(t, f)

	out, _, code := f.inWorkspace(t, "approval", "revoke", id, "--yes")
	if code != ExitOK {
		t.Fatalf("revoke exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "can still run without a prompt") {
		t.Errorf("the outcome must not claim success it did not achieve:\n%s", out)
	}
	if !strings.Contains(out, shared) {
		t.Errorf("the outcome must name the file that still grants it:\n%s", out)
	}

	content, err := os.ReadFile(shared)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !strings.Contains(string(content), "npm run cleanup") {
		t.Error("a repository-shared settings file must not be edited by default")
	}
}

// Announcing "this will stop being trusted" over an empty list would be the one
// thing this command must never do: claim a change it is not going to make.
// Found by running the walkthrough by hand.
func TestRevokeSaysSoWhenThereIsNothingItCanRemove(t *testing.T) {
	f := startFixture(t)
	shared := filepath.Join(f.workspace, ".claude", "settings.json")
	writeAllowRules(t, shared, "Bash(npm run build)")

	out, _, code := f.inWorkspace(t, "approval", "revoke", "project:Bash(npm run build)", "--yes")
	if code != ExitOK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "Nothing here can be removed") {
		t.Errorf("a plan that removes nothing must say so:\n%s", out)
	}
	if strings.Contains(out, "This will stop being trusted") {
		t.Errorf("nothing stops being trusted, so the output must not say it does:\n%s", out)
	}
	if strings.Contains(out, "backed up first") {
		t.Errorf("no file changes, so promising a backup is noise:\n%s", out)
	}
	if !strings.Contains(out, "can still run without a prompt") {
		t.Errorf("the outcome must be honest about what still allows it:\n%s", out)
	}
}

// `Bash(npm run *)` grants far more than the one command being revoked, and
// removing it takes all of that away. The user asked about one command; if the
// plan showed only the rule text they could agree to much more than they meant.
func TestRevokeWarnsWhenTheRuleIsWiderThanTheCommand(t *testing.T) {
	f := startFixture(t)
	writeAllowRules(t, filepath.Join(f.home, ".claude", "settings.json"), "Bash(npm run *)")
	id := approveCleanup(t, f)

	out, _, code := f.inWorkspace(t, "approval", "revoke", id, "--yes")
	if code != ExitOK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "grants more than the command you named") {
		t.Errorf("a wildcard rule must be flagged as wider than the request:\n%s", out)
	}
	if !strings.Contains(out, "Bash(npm run *)") {
		t.Errorf("the plan must name the rule whose reach is wider:\n%s", out)
	}
}

// A rule that matches the command exactly is not wider, and saying so would be
// noise on the common path.
func TestRevokeDoesNotCryWolfOnAnExactRule(t *testing.T) {
	f := startFixture(t)
	writeAllowRules(t, filepath.Join(f.home, ".claude", "settings.json"), "Bash(npm run cleanup)")
	id := approveCleanup(t, f)

	out, _, code := f.inWorkspace(t, "approval", "revoke", id, "--yes")
	if code != ExitOK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "grants more than the command you named") {
		t.Errorf("an exact rule must not be flagged as wider:\n%s", out)
	}
}

// A short label means only what the listing that printed it meant. Unattended,
// nobody sees the plan that would show the substitution, so it is refused.
func TestRevokeRefusesAShortLabelWithYes(t *testing.T) {
	f := startFixture(t)
	writeAllowRules(t, filepath.Join(f.home, ".claude", "settings.json"), "Bash(git status)")

	_, errOut, code := f.inWorkspace(t, "approval", "revoke", "r1", "--yes")
	if code == ExitOK {
		t.Error("a listing label must not be accepted unattended")
	}
	if !strings.Contains(errOut, "not a stable name") {
		t.Errorf("the refusal must say why: %q", errOut)
	}
	if !strings.Contains(errOut, "user:Bash(git status)") {
		t.Errorf("the refusal must hand over the stable name: %q", errOut)
	}
}

// `--all` spans every project Intenter knows; rules are read from one project's
// settings files. Reporting "none" for something never looked at would be a
// claim the command cannot support.
func TestApprovalsSaysAgentRulesAreProjectScoped(t *testing.T) {
	f := startFixture(t)

	out, _, code := f.inWorkspace(t, "approvals", "--all", "--source", "agent-rule")
	if code != ExitOK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "holds no shell permission rules") {
		t.Errorf("this claims an absence it never checked:\n%s", out)
	}
	if !strings.Contains(out, "does not cover them") {
		t.Errorf("the output must say why there is nothing here:\n%s", out)
	}
}

// `revoke --json` worked before this feature, through the shared state-change
// command. Replacing that with a bespoke flow silently dropped it: the flag was
// accepted and ignored, and a script got prose. Restored, with the shape the
// removal actually needs.
func TestRevokeJSONReportsWhatHappened(t *testing.T) {
	f := startFixture(t)
	writeAllowRules(t, filepath.Join(f.home, ".claude", "settings.json"), "Bash(npm run cleanup)")
	id := approveCleanup(t, f)

	out, _, code := f.inWorkspace(t, "approval", "revoke", id, "--yes", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d\n%s", code, out)
	}

	var result revokeResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("revoke --json must emit JSON, got:\n%s", out)
	}
	if !result.Revoked || result.ApprovalID == nil {
		t.Errorf("result = %+v, want the approval reported as revoked", result)
	}
	if len(result.RulesRemoved) != 1 || result.RulesRemoved[0].Text != "Bash(npm run cleanup)" {
		t.Errorf("rules_removed = %+v", result.RulesRemoved)
	}
	if result.StillAllowed {
		t.Error("nothing was left granting the command, so still_allowed must be false")
	}
}

// With no terminal to ask from and no `--yes`, a JSON removal must refuse
// rather than take the machine-readable output as licence to proceed.
func TestRevokeJSONRefusesWithoutYes(t *testing.T) {
	f := startFixture(t)
	id := approveCleanup(t, f)

	_, errOut, code := f.inWorkspace(t, "approval", "revoke", id, "--json")
	if code == ExitOK {
		t.Error("--json without --yes must not change anything")
	}
	if !strings.Contains(errOut, "--yes") {
		t.Errorf("the refusal must say how to proceed: %q", errOut)
	}
}

func TestRevokeAcceptsARuleKey(t *testing.T) {
	f := startFixture(t)
	settings := filepath.Join(f.home, ".claude", "settings.json")
	writeAllowRules(t, settings, "Bash(npm run cleanup)")

	out, _, code := f.inWorkspace(t, "approval", "revoke", "user:Bash(npm run cleanup)", "--yes")
	if code != ExitOK {
		t.Fatalf("revoke exit = %d\n%s", code, out)
	}
	content, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(content), "npm run cleanup") {
		t.Errorf("a rule addressed by its key must be removed:\n%s", content)
	}
}

func TestRevokeRefusesATargetItCannotFind(t *testing.T) {
	f := startFixture(t)

	_, errOut, code := f.inWorkspace(t, "approval", "revoke", "local:Bash(never added)", "--yes")
	if code == ExitOK {
		t.Error("an unknown rule key must be refused")
	}
	if !strings.Contains(errOut, "intenter approvals") {
		t.Errorf("the refusal must say what to do instead: %q", errOut)
	}
}

func TestRevokeOfAnAlreadyRevokedApprovalChangesNothing(t *testing.T) {
	f := startFixture(t)
	id := approveCleanup(t, f)

	if _, _, code := f.inWorkspace(t, "approval", "revoke", id, "--yes"); code != ExitOK {
		t.Fatalf("first revoke exit = %d", code)
	}
	out, _, code := f.inWorkspace(t, "approval", "revoke", id, "--yes")
	if code != ExitOK {
		t.Fatalf("second revoke exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "already revoked") {
		t.Errorf("a second revoke must say so plainly:\n%s", out)
	}
}

// writeAllowRules puts a Claude settings file with permission rules in place.
func writeAllowRules(t *testing.T, path string, allow ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content, err := json.Marshal(map[string]any{"permissions": map[string]any{"allow": allow}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeFile(t, path, string(content))
}

// A rule in Claude's own settings lets a command run without a prompt whether
// or not Intenter ever imported it. A listing that showed only Intenter's
// approvals would tell the user this project trusts less than it does.
func TestApprovalsListsTheRulesClaudeHoldsOfItsOwn(t *testing.T) {
	f := startFixture(t)
	writeAllowRules(t, filepath.Join(f.home, ".claude", "settings.json"), "Bash(git status)")
	writeAllowRules(t, filepath.Join(f.workspace, ".claude", "settings.local.json"),
		"Bash(npm run cleanup)")

	out, _, code := f.inWorkspace(t, "approvals")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{
		"Rules Claude holds of its own",
		"Bash(git status)",
		"Bash(npm run cleanup)",
		"r1", "r2",
		"intenter approval revoke",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing is missing %q:\n%s", want, out)
		}
	}
}

func TestApprovalsSourceFilterSeparatesTheTwo(t *testing.T) {
	f := startFixture(t)
	writeAllowRules(t, filepath.Join(f.home, ".claude", "settings.json"), "Bash(git status)")
	evaluated := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*evaluated.AuditEventID))

	onlyApprovals, _, code := f.inWorkspace(t, "approvals", "--source", "approval")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, onlyApprovals)
	}
	if strings.Contains(onlyApprovals, "Bash(git status)") {
		t.Errorf("--source approval must not list agent rules:\n%s", onlyApprovals)
	}

	onlyRules, _, code := f.inWorkspace(t, "approvals", "--source", "agent-rule")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, onlyRules)
	}
	if !strings.Contains(onlyRules, "Bash(git status)") {
		t.Errorf("--source agent-rule must list the rule:\n%s", onlyRules)
	}
	if strings.Contains(onlyRules, "RUN_SCRIPT") {
		t.Errorf("--source agent-rule must not list approvals:\n%s", onlyRules)
	}

	_, errOut, code := f.inWorkspace(t, "approvals", "--source", "nonsense")
	if code == ExitOK {
		t.Error("an unknown --source must be refused")
	}
	if !strings.Contains(errOut, "--source") {
		t.Errorf("stderr = %q, want the valid values", errOut)
	}
}

// A settings file that cannot be parsed makes the list incomplete. Saying
// nothing would present it as complete, which is the one thing a permission
// listing must never do.
func TestApprovalsReportsUnreadableSettings(t *testing.T) {
	f := startFixture(t)
	broken := filepath.Join(f.workspace, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, broken, "{ not json")

	out, _, code := f.inWorkspace(t, "approvals")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if !strings.Contains(out, "could not be read") || !strings.Contains(out, broken) {
		t.Errorf("the listing must name the unreadable file:\n%s", out)
	}
}

// The most common reason to want a permission back is that it was granted a
// moment ago, hastily. Sorting by recency is what makes that one findable.
func TestApprovalsRecentPutsTheNewestFirst(t *testing.T) {
	f := startFixture(t)

	first := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*first.AuditEventID))
	second := f.evaluate(t, "git status", "toolu_2")
	f.inWorkspace(t, "approve", itoa(*second.AuditEventID))

	out, _, code := f.inWorkspace(t, "approvals", "--recent", "--source", "approval", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	var summaries []ipc.ApprovalSummary
	if err := json.Unmarshal([]byte(out), &summaries); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(summaries) != 2 {
		t.Fatalf("approvals = %d, want 2", len(summaries))
	}
	if summaries[0].CreatedAt.Before(summaries[1].CreatedAt) {
		t.Errorf("--recent must put the newest first, got %s then %s",
			summaries[0].CreatedAt, summaries[1].CreatedAt)
	}
}
