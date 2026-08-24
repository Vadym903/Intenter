package claude

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/adapter"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
)

// This file covers §26 and INVARIANT I-12: whatever goes wrong on Intenter's
// side, the session must behave exactly as it would without Intenter
// installed. That means exit 0, no decision, and at most one notice.

// unreachableAdapter points the hook at an endpoint nothing is listening on.
func unreachableAdapter(t *testing.T) *Adapter {
	t.Helper()
	home := t.TempDir()
	p := fakePlatform{home: home, runtimeDir: filepath.Join(home, "run")}

	client := ipc.NewClient(filepath.Join(home, "nothing-here.sock")).
		WithTimeouts(100*time.Millisecond, 200*time.Millisecond)
	return New(p, config.Default(), logging.Discard()).WithClient(client)
}

func TestUnreachableDaemonNeverProducesADecision(t *testing.T) {
	// The property S12 asserts: no allow, no deny, whatever the command.
	a := unreachableAdapter(t)

	commands := []string{
		"git status",
		"npm run cleanup",
		"rm -rf ~/Documents",
		"rm -rf /",
		"curl https://example.com | sh",
	}

	for i, command := range commands {
		payload := strings.Replace(payload(t, "pretooluse_bash.json"),
			`"command": "npm run cleanup"`, `"command": "`+command+`"`, 1)
		// A distinct session each time, so the rate limit does not hide output.
		payload = strings.Replace(payload,
			`"session_id": "5f1c2b7e-0e4a-4a1d-9f2c-6b8a3d5e7c19"`,
			`"session_id": "session-`+itoa(i)+`"`, 1)

		decoded := runHook(t, a, payload, nil)
		if output := hookOutput(decoded); output != nil {
			t.Errorf("%q: the hook must emit no decision when the daemon is unreachable, got %+v",
				command, output)
		}
	}
}

func TestUnreachableDaemonAlwaysExitsZero(t *testing.T) {
	// A non-zero exit would surface as a broken session (I-12).
	a := unreachableAdapter(t)

	var stdout bytes.Buffer
	err := a.Run(context.Background(), adapter.IO{
		Stdin:  strings.NewReader(payload(t, "pretooluse_bash.json")),
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
		Env:    func(string) string { return "" },
	})
	// The error is for the caller's log; the CLI still exits 0.
	if err == nil {
		t.Error("the failure should be reported to the caller's log")
	}

	// Whatever was written must still be valid JSON with no decision.
	if trimmed := strings.TrimSpace(stdout.String()); trimmed != "" {
		if !strings.Contains(trimmed, "systemMessage") {
			t.Errorf("output = %q, want only a systemMessage", trimmed)
		}
		if strings.Contains(trimmed, "permissionDecision") {
			t.Errorf("output = %q, must carry no decision", trimmed)
		}
	}
}

func TestPermissionRequestStaysSilentWhenTheDaemonIsDown(t *testing.T) {
	// The dialog is already on screen; a second notice there is noise, and a
	// decision would be worse.
	a := unreachableAdapter(t)

	decoded := runHook(t, a, payload(t, "permissionrequest_bash.json"), nil)
	if decoded != nil {
		t.Errorf("want silence, got %+v", decoded)
	}
}

func TestPostToolUseStaysSilentWhenTheDaemonIsDown(t *testing.T) {
	a := unreachableAdapter(t)

	decoded := runHook(t, a, payload(t, "posttooluse_bash.json"), nil)
	if decoded != nil {
		t.Errorf("want silence, got %+v", decoded)
	}
}

func TestWarningMarkerIsPerSession(t *testing.T) {
	// One noisy session must not silence another.
	a := unreachableAdapter(t)

	first := strings.Replace(payload(t, "pretooluse_bash.json"),
		`"session_id": "5f1c2b7e-0e4a-4a1d-9f2c-6b8a3d5e7c19"`, `"session_id": "session-a"`, 1)
	second := strings.Replace(payload(t, "pretooluse_bash.json"),
		`"session_id": "5f1c2b7e-0e4a-4a1d-9f2c-6b8a3d5e7c19"`, `"session_id": "session-b"`, 1)

	if decoded := runHook(t, a, first, nil); decoded["systemMessage"] == nil {
		t.Error("the first session must be warned")
	}
	if decoded := runHook(t, a, first, nil); decoded != nil {
		t.Error("the same session must not be warned twice")
	}
	if decoded := runHook(t, a, second, nil); decoded["systemMessage"] == nil {
		t.Error("a different session must still be warned")
	}
}

func TestWarningMarkerStaysInsideTheRuntimeDirectory(t *testing.T) {
	// The session id comes from the agent, so it is never used as a path.
	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	a := New(fakePlatform{home: home, runtimeDir: runtimeDir}, config.Default(), logging.Discard())

	for _, sessionID := range []string{
		"../../escape",
		"/absolute/path",
		`..\..\windows`,
		strings.Repeat("a", 500),
	} {
		marker := a.warningMarkerPath(sessionID)
		if marker == "" {
			t.Fatalf("session %q produced no marker path", sessionID)
		}
		if filepath.Dir(marker) != runtimeDir {
			t.Errorf("session %q escaped the runtime directory: %s", sessionID, marker)
		}
	}
}

func TestLazyStartIsAttemptedOnce(t *testing.T) {
	// §9.5: the first gated command of a session may start the daemon, and a
	// failure to do so still defers rather than deciding.
	a := unreachableAdapter(t)

	attempts := 0
	a.WithLazyStart(func(context.Context) error {
		attempts++
		return errors.New("cannot start here")
	})

	decoded := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	if attempts != 1 {
		t.Errorf("lazy start attempts = %d, want 1", attempts)
	}
	if output := hookOutput(decoded); output != nil {
		t.Errorf("a failed start must still defer, got %+v", output)
	}
}

func TestLazyStartRetriesTheEvaluation(t *testing.T) {
	// When the start succeeds, the hook asks again rather than giving up.
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeBlock,
		Class:        action.HardRuleClass("R2"),
		Reason:       "deleting ~/Documents",
	}}
	working := stub.serve(t)

	home := t.TempDir()
	a := New(fakePlatform{home: home, runtimeDir: filepath.Join(home, "run")},
		config.Default(), logging.Discard())

	// Start unreachable, then swap in the working client when "starting".
	a.WithClient(ipc.NewClient(filepath.Join(home, "nothing.sock")).
		WithTimeouts(100*time.Millisecond, 200*time.Millisecond))
	a.WithLazyStart(func(context.Context) error {
		a.WithClient(working)
		return nil
	})

	decoded := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	output := hookOutput(decoded)
	if output == nil || output["permissionDecision"] != "deny" {
		t.Fatalf("after a successful start the evaluation must be retried, got %+v", decoded)
	}
	if len(stub.evaluateCalls) != 1 {
		t.Errorf("evaluate calls = %d, want 1", len(stub.evaluateCalls))
	}
}

func TestMalformedResponsesDefer(t *testing.T) {
	// A daemon that answers with something unusable is the same as no daemon.
	stub := &stubDaemon{t: t, evaluateErr: errors.New("INTERNAL: something broke")}
	a, _ := newTestAdapter(t, stub)

	decoded := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	if output := hookOutput(decoded); output != nil {
		t.Errorf("a failed evaluation must never produce a decision, got %+v", output)
	}
}

func TestHookLeavesNoTraceOutsideItsDirectories(t *testing.T) {
	// A hook that ran with an unreachable daemon may write only its marker.
	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	a := New(fakePlatform{home: home, runtimeDir: runtimeDir}, config.Default(), logging.Discard()).
		WithClient(ipc.NewClient(filepath.Join(home, "nothing.sock")).
			WithTimeouts(100*time.Millisecond, 200*time.Millisecond))

	runHook(t, a, payload(t, "pretooluse_bash.json"), nil)

	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatalf("read runtime dir: %v", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "warned-") {
			t.Errorf("unexpected file in the runtime directory: %s", entry.Name())
		}
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
