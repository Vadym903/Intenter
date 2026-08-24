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
	if !strings.Contains(out, "Nothing is approved in this project yet") {
		t.Errorf("output = %q, want the empty-state message", out)
	}
	if !strings.Contains(out, "intenter approve") {
		t.Error("the empty state must say how to approve something")
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

	var summaries []ipc.ApprovalSummary
	if err := json.Unmarshal([]byte(out), &summaries); err != nil {
		t.Fatalf("output is not a JSON array of approvals: %v\n%s", err, out)
	}
	if len(summaries) != 1 {
		t.Fatalf("approvals = %d, want 1", len(summaries))
	}
	if summaries[0].Kind != action.ApprovalExact {
		t.Errorf("kind = %s", summaries[0].Kind)
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

	out, _, code = f.inWorkspace(t, "approval", "revoke", "1")
	if code != ExitOK || !strings.Contains(out, "revoked") {
		t.Fatalf("revoke = %q (exit %d)", out, code)
	}
	if !strings.Contains(out, "permanent") {
		t.Error("revoke must say it is permanent")
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
