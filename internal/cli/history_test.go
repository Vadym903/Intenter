package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

func TestHistoryIsEmptyAtFirst(t *testing.T) {
	f := startFixture(t)

	out, _, code := f.inWorkspace(t, "history")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(out, "No decisions recorded yet") {
		t.Errorf("output = %q, want the empty-state message", out)
	}
}

func TestHistoryListsDecisions(t *testing.T) {
	f := startFixture(t)
	f.evaluate(t, "git status", "toolu_1")
	f.evaluate(t, "rm -rf ~/Documents", "toolu_2")
	f.evaluate(t, "some-unknown-tool", "toolu_3")

	out, _, code := f.inWorkspace(t, "history")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{
		"ID", "TIME", "DECISION", "CLASS", "COMMAND", "REASON", "APPROVAL",
		"ALLOW", "BLOCK", "ASK",
		"git status", "rm -rf ~/Documents", "some-unknown-tool",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("history output missing %q:\n%s", want, out)
		}
	}
}

func TestHistoryDecisionFilters(t *testing.T) {
	f := startFixture(t)
	f.evaluate(t, "git status", "toolu_1")
	f.evaluate(t, "rm -rf ~/Documents", "toolu_2")
	f.evaluate(t, "some-unknown-tool", "toolu_3")

	tests := []struct {
		flag    string
		present string
		absent  string
	}{
		{"--blocked", "rm -rf ~/Documents", "git status"},
		{"--allowed", "git status", "rm -rf ~/Documents"},
		{"--asked", "some-unknown-tool", "rm -rf ~/Documents"},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			out, _, code := f.inWorkspace(t, "history", tt.flag)
			if code != ExitOK {
				t.Fatalf("exit code = %d", code)
			}
			if !strings.Contains(out, tt.present) {
				t.Errorf("want %q in:\n%s", tt.present, out)
			}
			if strings.Contains(out, tt.absent) {
				t.Errorf("did not want %q in:\n%s", tt.absent, out)
			}
		})
	}
}

func TestHistoryRefusesConflictingFilters(t *testing.T) {
	f := startFixture(t)

	_, errOut, code := f.inWorkspace(t, "history", "--blocked", "--allowed")
	if code == ExitOK {
		t.Fatal("want a failure for conflicting filters")
	}
	if !strings.Contains(errOut, "at most one") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestHistoryJSONShape(t *testing.T) {
	f := startFixture(t)
	f.evaluate(t, "git status", "toolu_1")

	out, _, code := f.inWorkspace(t, "history", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	var events []action.AuditEventSummary
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("output is not a JSON array of events: %v\n%s", err, out)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].RawCommand != "git status" {
		t.Errorf("event = %+v", events[0])
	}
}

func TestHistoryShowExplainsABlock(t *testing.T) {
	// The user-facing half of INVARIANT I-17: everything needed to answer
	// "why was this blocked?" comes from the stored row.
	f := startFixture(t)
	blocked := f.evaluate(t, "rm -rf ~/Documents", "toolu_1")

	out, _, code := f.inWorkspace(t, "history", "show", itoa(*blocked.AuditEventID))
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{
		"BLOCK", "HARD_RULE_R2", "targets:", "~/Documents", "HOME",
		"effects:", "DELETE", "explanation:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("history show output missing %q:\n%s", want, out)
		}
	}
	assertField(t, out, "command", "rm -rf ~/Documents")
	assertField(t, out, "resolved", "RESOLVED")
	assertField(t, out, "rule", "R2")
	assertField(t, out, "reason", "home directory")
}

func TestHistoryShowExplainsAnAutoApproval(t *testing.T) {
	// The other half: "why did Intenter auto-approve this?"
	f := startFixture(t)
	first := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*first.AuditEventID))
	allowed := f.evaluate(t, "npm run cleanup", "toolu_2")

	out, _, code := f.inWorkspace(t, "history", "show", itoa(*allowed.AuditEventID))
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{
		"ALLOW", "APPROVAL_MATCH",
		"depends on:", "npm-script:package.json#scripts.cleanup",
		"effects:", "./dist",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("history show output missing %q:\n%s", want, out)
		}
	}
	assertField(t, out, "approval", "1")
}

func TestHistoryShowExplainsAMismatch(t *testing.T) {
	f := startFixture(t)
	first := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*first.AuditEventID))

	// Rewrite the script so the approval stops covering it.
	manifest := `{"name":"demo","scripts":{"cleanup":"rm -rf ./src"}}`
	writeFile(t, f.workspace+"/package.json", manifest)

	mismatched := f.evaluate(t, "npm run cleanup", "toolu_2")
	out, _, code := f.inWorkspace(t, "history", "show", itoa(*mismatched.AuditEventID))
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{
		"APPROVAL_MISMATCH",
		"approval 1 no longer matches",
		"npm-script:package.json#scripts.cleanup changed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("history show output missing %q:\n%s", want, out)
		}
	}
}

func TestHistoryReadsTheDatabaseWhenTheDaemonIsDown(t *testing.T) {
	// The history matters most exactly when something is wrong, which is also
	// when the daemon may not be running (§25).
	f := startFixture(t)
	blocked := f.evaluate(t, "rm -rf ~/Documents", "toolu_1")
	eventID := *blocked.AuditEventID

	stopFixtureDaemon(t, f)

	out, errOut, code := f.inWorkspace(t, "history")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "daemon is not running") {
		t.Errorf("stderr = %q, want the read-only warning", errOut)
	}
	if !strings.Contains(out, "rm -rf ~/Documents") {
		t.Errorf("history is still readable:\n%s", out)
	}

	detail, errOut, code := f.inWorkspace(t, "history", "show", itoa(eventID))
	if code != ExitOK {
		t.Fatalf("history show exit code = %d\n%s", code, detail)
	}
	if !strings.Contains(errOut, "daemon is not running") {
		t.Errorf("stderr = %q, want the read-only warning", errOut)
	}
	if !strings.Contains(detail, "HARD_RULE_R2") {
		t.Errorf("the stored explanation is still readable:\n%s", detail)
	}
}

func TestApprovalsStillNeedTheDaemon(t *testing.T) {
	// Only the history has a read-only fallback; anything that changes trust
	// goes through the daemon.
	f := startFixture(t)
	stopFixtureDaemon(t, f)

	_, _, code := f.inWorkspace(t, "approvals")
	if code != ExitDaemonUnreached {
		t.Errorf("exit code = %d, want %d", code, ExitDaemonUnreached)
	}
}
