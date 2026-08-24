package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/ipc"
)

// The limits of §15.1 exist so a pathological command cannot stall the agent or
// exhaust the daemon. Every one of them fails towards ASK: a limit that let an
// action through unexamined would be worse than a slow one.

func TestOversizedRequestIsAskedNotRejected(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// An over-long command is examined in part and forced to a prompt (R13),
	// never rejected: a bad request would defer to the agent's native flow with
	// the safety floor skipped, which an agent could exploit by padding a
	// command past the limit.
	var result action.EvaluationResult
	err := client.Call(ctx, ipc.MethodEvaluate, ipc.EvaluateParams{
		Request: bashRequest(w, "echo "+strings.Repeat("a", action.MaxRawCommandBytes), "toolu_1"),
	}, &result)
	if err != nil {
		t.Fatalf("an over-long command must be evaluated, not rejected: %v", err)
	}
	if result.Decision != action.OutcomeAsk || result.HardRule != "R13" {
		t.Fatalf("decision = %s rule = %q, want ASK / R13", result.Decision, result.HardRule)
	}
}

func TestTooManyCommandsIsNotApprovable(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	command := strings.TrimSuffix(strings.Repeat("git status; ", 40), "; ")
	result := evaluate(t, client, bashRequest(w, command, "toolu_1"))

	if result.Decision == action.OutcomeAllow {
		t.Fatalf("a command line past the limit must not be allowed (%s)", result.Reason)
	}
	if result.ResolutionStatus.Approvable() {
		t.Errorf("resolution status = %s, want a non-approvable status", result.ResolutionStatus)
	}
}

func TestDeeplyNestedScriptsAreNotApprovable(t *testing.T) {
	// §15.1: wrapper resolution stops at four hops rather than following a
	// chain of unknown depth.
	p := testPlatform(t)
	w := newWorkspace(t, p)
	w.write(t, "package.json", `{"name":"demo","scripts":{
		"a":"npm run b","b":"npm run c","c":"npm run d",
		"d":"npm run e","e":"npm run f","f":"rm -rf ./dist"
	}}`)
	client := startDaemon(t, p)

	result := evaluate(t, client, bashRequest(w, "npm run a", "toolu_1"))
	if result.Decision != action.OutcomeAsk {
		t.Fatalf("decision = %s (%s), want ASK", result.Decision, result.Reason)
	}
	if result.Class != action.ClassUnresolvedCommand {
		t.Errorf("class = %s, want UNRESOLVED_COMMAND", result.Class)
	}
	if !strings.Contains(result.Reason, "deeper") {
		t.Errorf("reason = %q, want it to name the depth limit", result.Reason)
	}
}

func TestScriptCyclesTerminate(t *testing.T) {
	// A pair of scripts calling each other must not spin.
	p := testPlatform(t)
	w := newWorkspace(t, p)
	w.write(t, "package.json", `{"name":"demo","scripts":{"a":"npm run b","b":"npm run a"}}`)
	client := startDaemon(t, p)

	done := make(chan action.EvaluationResult, 1)
	go func() { done <- evaluate(t, client, bashRequest(w, "npm run a", "toolu_1")) }()

	select {
	case result := <-done:
		if result.Decision == action.OutcomeAllow {
			t.Errorf("a script loop must not be allowed (%s)", result.Reason)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("resolving a script loop did not terminate")
	}
}

func TestUnknownMethodDoesNotDisturbTheDaemon(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var ignored map[string]any
	if err := client.Call(ctx, "not_a_method", nil, &ignored); err == nil {
		t.Error("want an error for an unknown method")
	}

	// The daemon keeps working afterwards.
	result := evaluate(t, client, bashRequest(w, "git status", "toolu_1"))
	if result.Decision != action.OutcomeAllow {
		t.Errorf("decision = %s, want the daemon to carry on", result.Decision)
	}
}

func TestMalformedParametersAreRejectedNotCrashed(t *testing.T) {
	p := testPlatform(t)
	w := newWorkspace(t, p)
	client := startDaemon(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A parameter of the wrong shape entirely.
	var ignored map[string]any
	if err := client.Call(ctx, ipc.MethodEvaluate, "not an object", &ignored); err == nil {
		t.Error("want a BAD_REQUEST for malformed parameters")
	}

	result := evaluate(t, client, bashRequest(w, "git status", "toolu_2"))
	if result.Decision != action.OutcomeAllow {
		t.Errorf("decision = %s, want the daemon to carry on", result.Decision)
	}
}

func TestMissingEventsAreNotFound(t *testing.T) {
	p := testPlatform(t)
	client := startDaemon(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var event action.AuditEvent
	err := client.Call(ctx, ipc.MethodGetHistoryEvent, ipc.GetHistoryEventParams{ID: 9999}, &event)
	if err == nil {
		t.Fatal("want an error for an unknown event")
	}
	if !strings.Contains(err.Error(), ipc.CodeNotFound) {
		t.Errorf("error = %v, want %s", err, ipc.CodeNotFound)
	}

	var detail ipc.ApprovalDetail
	err = client.Call(ctx, ipc.MethodGetApproval, ipc.GetApprovalParams{ID: 9999}, &detail)
	if err == nil || !strings.Contains(err.Error(), ipc.CodeNotFound) {
		t.Errorf("error = %v, want %s for an unknown approval", err, ipc.CodeNotFound)
	}
}

func TestConcurrentEvaluationsAreIndependent(t *testing.T) {
	// The daemon serves one goroutine per connection; a slow or failing
	// request must not affect another.
	p := testPlatform(t)
	w := newWorkspace(t, p)
	w.write(t, "package.json", `{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}`)
	client := startDaemon(t, p)

	const workers = 8
	results := make(chan action.EvaluationResult, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			command := "git status"
			if i%2 == 0 {
				command = "npm run cleanup"
			}
			results <- evaluate(t, client, bashRequest(w, command, "toolu_"+strings.Repeat("x", i+1)))
		}(i)
	}

	for i := 0; i < workers; i++ {
		select {
		case result := <-results:
			if result.AuditEventID == nil {
				t.Error("every evaluation must be recorded")
			}
		case <-time.After(20 * time.Second):
			t.Fatal("concurrent evaluations did not finish")
		}
	}

	// Every one of them produced its own audit row.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var events []action.AuditEventSummary
	if err := client.Call(ctx, ipc.MethodListHistory, ipc.ListHistoryParams{Limit: 100}, &events); err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(events) != workers {
		t.Errorf("events = %d, want %d", len(events), workers)
	}
}
