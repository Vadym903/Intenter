package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/ipc"
)

// These are end-to-end regressions for the area-3 review (T048, approval
// matcher): each runs the real resolver and the real matcher together through
// the daemon, the same path a hook takes, so a gap in either layer alone would
// show up as an unwanted ALLOW here.

// approveKind is approveOnce with an explicit kind, for the SEMANTIC cases
// below (approveOnce in handlers_test.go always asks for EXACT).
func approveKind(t *testing.T, client *ipc.Client, eventID int64, kind action.ApprovalKind) ipc.ApprovalDetail {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var detail ipc.ApprovalDetail
	err := client.Call(ctx, ipc.MethodCreateApproval, ipc.CreateApprovalParams{
		AuditEventID: eventID,
		Kind:         kind,
	}, &detail)
	if err != nil {
		t.Fatalf("create %s approval: %v", kind, err)
	}
	return detail
}

// AG-161 (see internal/approval/review_regression_test.go): a SEMANTIC
// approval for a plain GET must not grow to cover the same request plus a
// cookie-jar write, even though the network target is unchanged — the write
// is a new envelope entry the approval never permitted.
func TestApprovalSemanticCurlDoesNotGrowToCoverAnAddedCookieJarWrite(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	first := evaluate(t, client, bashRequest(w, "curl https://api.example.com/data", "toolu_1"))
	if first.Decision != action.OutcomeAsk {
		t.Fatalf("first run = %s (%s), want ASK", first.Decision, first.Reason)
	}
	approveKind(t, client, *first.AuditEventID, action.ApprovalSemantic)

	second := evaluate(t, client, bashRequest(w, "curl https://api.example.com/data", "toolu_2"))
	if second.Decision != action.OutcomeAllow {
		t.Fatalf("the unchanged request must now be allowed: %s (%s)", second.Decision, second.Reason)
	}

	grown := evaluate(t, client,
		bashRequest(w, "curl https://api.example.com/data -c ./cookies.txt", "toolu_3"))
	if grown.Decision == action.OutcomeAllow {
		t.Errorf("a curl approval for a GET must not also cover a cookie-jar write it never granted (%s)",
			grown.Class)
	}
}

// AG-161: a SEMANTIC approval for an ordinary HOME write must not extend to a
// write inside a package manager's cache directory — nothing about the
// envelope (CREATE/WRITE, HOME scope) changes when the target moves from a
// plain file to ~/.npm, but a write there can poison a later `npm install`.
func TestApprovalSemanticHomeWriteDoesNotGrowToCoverAToolCacheWrite(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	w.write(t, "notes.txt", "hello")
	client := startDaemon(t, p)

	first := evaluate(t, client, bashRequest(w, "cp ./notes.txt ~/notes.txt", "toolu_1"))
	if first.Decision != action.OutcomeAsk {
		t.Fatalf("first run = %s (%s), want ASK", first.Decision, first.Reason)
	}
	approveKind(t, client, *first.AuditEventID, action.ApprovalSemantic)

	second := evaluate(t, client, bashRequest(w, "cp ./notes.txt ~/notes.txt", "toolu_2"))
	if second.Decision != action.OutcomeAllow {
		t.Fatalf("the unchanged copy must now be allowed: %s (%s)", second.Decision, second.Reason)
	}

	toolCache := evaluate(t, client,
		bashRequest(w, "cp ./notes.txt ~/.npm/_cacache/evil", "toolu_3"))
	if toolCache.Decision == action.OutcomeAllow {
		t.Errorf("a plain HOME write approval must not also cover a write into the npm cache (%s)",
			toolCache.Class)
	}
}

// R5 (sensitive write -> BLOCK) runs before approval matching (I-4), so it
// stops a broad HOME-write approval from ever reaching a credential file —
// independently of, and in addition to, the AG-161 envelope fix above.
func TestApprovalNeverExtendsToASensitiveFileRegardlessOfMatching(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	w.write(t, "notes.txt", "hello")
	client := startDaemon(t, p)

	first := evaluate(t, client, bashRequest(w, "cp ./notes.txt ~/notes.txt", "toolu_1"))
	if first.Decision != action.OutcomeAsk {
		t.Fatalf("first run = %s (%s), want ASK", first.Decision, first.Reason)
	}
	approveKind(t, client, *first.AuditEventID, action.ApprovalSemantic)

	if err := os.MkdirAll(filepath.Join(p.HomeDir(), ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}

	blocked := evaluate(t, client,
		bashRequest(w, "cp ./notes.txt ~/.ssh/authorized_keys", "toolu_2"))
	if blocked.Decision != action.OutcomeBlock {
		t.Fatalf("decision = %s (%s), want BLOCK", blocked.Decision, blocked.Reason)
	}
	if blocked.HardRule != "R5" {
		t.Errorf("hard rule = %q, want R5", blocked.HardRule)
	}
}
