package claude

import (
	"errors"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// sessionEndPayload is one SessionEnd hook invocation, in the shape Claude
// sends it (input schema in the hooks documentation).
const sessionEndPayload = `{
  "hook_event_name": "SessionEnd",
  "session_id": "session-1",
  "cwd": "/w/demo",
  "session_end_reason": "prompt_input_exit",
  "last_assistant_message": "done"
}`

func TestSessionEndReportsWhatTheSessionDid(t *testing.T) {
	stub := &stubDaemon{t: t, summarizeResult: action.ActivitySummary{
		Total: 46, Allowed: 42, AllowedByApproval: 2, AllowedBaseline: 40,
		Asked: 3, Blocked: 1,
	}}
	a, _ := newTestAdapter(t, stub)

	decoded := runHook(t, a, sessionEndPayload, nil)
	message, _ := decoded["systemMessage"].(string)

	for _, want := range []string{
		"Intenter this session",
		"46 commands",
		"42 allowed",
		"3 asked",
		"1 blocked",
		"2 prompts you did not have to answer",
		"intenter summary",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("systemMessage must mention %q:\n%s", want, message)
		}
	}

	// Counted for this session only: another session's numbers are not this
	// user's report on what just happened.
	if len(stub.summarizeCalls) != 1 {
		t.Fatalf("summarize calls = %d, want 1", len(stub.summarizeCalls))
	}
	if id := stub.summarizeCalls[0].SessionID; id == nil || *id != "session-1" {
		t.Errorf("summarize was not scoped to the session: %+v", stub.summarizeCalls[0])
	}
}

func TestSessionEndCarriesNoPermissionDecision(t *testing.T) {
	// SessionEnd cannot decide anything, and a decision on it would be a
	// protocol error rather than a no-op.
	stub := &stubDaemon{t: t, summarizeResult: action.ActivitySummary{Total: 1, Allowed: 1}}
	a, _ := newTestAdapter(t, stub)

	decoded := runHook(t, a, sessionEndPayload, nil)
	if output := hookOutput(decoded); output != nil {
		t.Errorf("SessionEnd must carry no decision, got %+v", output)
	}
}

func TestASessionThatDecidedNothingSaysNothing(t *testing.T) {
	// Closing a terminal is the worst moment for a notice that reports zero of
	// everything.
	stub := &stubDaemon{t: t, summarizeResult: action.ActivitySummary{}}
	a, _ := newTestAdapter(t, stub)

	if decoded := runHook(t, a, sessionEndPayload, nil); decoded != nil {
		t.Errorf("want silence for an empty session, got %+v", decoded)
	}
}

func TestSessionEndIsSilentWhenTheDaemonCannotAnswer(t *testing.T) {
	// The summary is a courtesy. A session is ending; there is nothing the user
	// could do about a daemon problem reported now, and nothing was gated by
	// this event.
	stub := &stubDaemon{t: t, summarizeErr: errors.New("INTERNAL: broken")}
	a, _ := newTestAdapter(t, stub)

	if decoded := runHook(t, a, sessionEndPayload, nil); decoded != nil {
		t.Errorf("want silence on a failed summary, got %+v", decoded)
	}

	unreachable := unreachableAdapter(t)
	if decoded := runHook(t, unreachable, sessionEndPayload, nil); decoded != nil {
		t.Errorf("want silence when the daemon is unreachable, got %+v", decoded)
	}
}

func TestSessionEndWithoutASessionIDIsIgnored(t *testing.T) {
	// The summary is per session; without an id there is nothing to scope it to,
	// and counting everything would report other sessions as this one.
	stub := &stubDaemon{t: t, summarizeResult: action.ActivitySummary{Total: 9, Allowed: 9}}
	a, _ := newTestAdapter(t, stub)

	const noSession = `{"hook_event_name":"SessionEnd","cwd":"/w/demo","session_end_reason":"other"}`
	if decoded := runHook(t, a, noSession, nil); decoded != nil {
		t.Errorf("want silence without a session id, got %+v", decoded)
	}
	if len(stub.summarizeCalls) != 0 {
		t.Errorf("the daemon must not be asked at all: %+v", stub.summarizeCalls)
	}
}

func TestSessionSummaryMessageReadsForOneOfEach(t *testing.T) {
	// The plural forms are the kind of thing nobody notices until a user sees
	// "1 commands" in their terminal.
	message := SessionSummaryMessage(action.ActivitySummary{
		Total: 1, Allowed: 1, AllowedByApproval: 1,
	})
	for _, want := range []string{"1 command checked", "1 command allowed by approvals", "1 prompt you did not"} {
		if !strings.Contains(message, want) {
			t.Errorf("message must read %q:\n%s", want, message)
		}
	}
	if strings.Contains(message, "commands checked") {
		t.Errorf("a single command must not be pluralized:\n%s", message)
	}
}

func TestSessionSummaryOmitsWhatDidNotHappen(t *testing.T) {
	// A quiet session should read as quiet, not as a table of zeros.
	message := SessionSummaryMessage(action.ActivitySummary{
		Total: 12, Allowed: 12, AllowedBaseline: 12,
	})
	for _, unwanted := range []string{"asked", "blocked", "did not have to answer"} {
		if strings.Contains(message, unwanted) {
			t.Errorf("message must not mention %q when it did not happen:\n%s", unwanted, message)
		}
	}
	if !strings.Contains(message, "12 commands checked — 12 allowed.") {
		t.Errorf("message = %q", message)
	}
}
