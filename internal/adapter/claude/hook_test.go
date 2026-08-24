package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/adapter"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
)

// stubDaemon answers the IPC methods the adapter calls, recording what it saw.
type stubDaemon struct {
	t *testing.T

	evaluateResult action.EvaluationResult
	evaluateErr    error

	summarizeResult action.ActivitySummary
	summarizeErr    error

	evaluateCalls      []action.ActionRequest
	promptCalls        []ipc.RecordPromptParams
	executeCalls       []ipc.ReportExecutionParams
	configCalls        []ipc.AgentConfigChangedParams
	adapterActionCalls []ipc.RecordAdapterActionParams
	summarizeCalls     []ipc.SummarizeParams

	// adapterActionErr makes the annotation fail, which must never be visible
	// to the agent.
	adapterActionErr error
	// onAdapterAction observes when the annotation arrives, relative to the
	// response the agent is waiting on.
	onAdapterAction func()
}

// serve starts a real IPC server on a temp endpoint and returns a client.
func (s *stubDaemon) serve(t *testing.T) *ipc.Client {
	t.Helper()

	endpoint := testEndpoint(t)
	listener, err := ipc.Listen(endpoint)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := ipc.NewServer(listener, logging.Discard(), 5*time.Second)
	server.Handle(ipc.MethodEvaluate, func(_ context.Context, req *ipc.Request) (any, error) {
		var params ipc.EvaluateParams
		if err := req.DecodeParams(&params); err != nil {
			return nil, err
		}
		s.evaluateCalls = append(s.evaluateCalls, params.Request)
		if s.evaluateErr != nil {
			return nil, s.evaluateErr
		}
		return s.evaluateResult, nil
	})
	server.Handle(ipc.MethodRecordPrompt, func(_ context.Context, req *ipc.Request) (any, error) {
		var params ipc.RecordPromptParams
		if err := req.DecodeParams(&params); err != nil {
			return nil, err
		}
		s.promptCalls = append(s.promptCalls, params)
		return ipc.RecordPromptResult{AuditEventID: action.Ref(42)}, nil
	})
	server.Handle(ipc.MethodRecordAdapterAction, func(_ context.Context, req *ipc.Request) (any, error) {
		var params ipc.RecordAdapterActionParams
		if err := req.DecodeParams(&params); err != nil {
			return nil, err
		}
		s.adapterActionCalls = append(s.adapterActionCalls, params)
		if s.onAdapterAction != nil {
			s.onAdapterAction()
		}
		if s.adapterActionErr != nil {
			return nil, s.adapterActionErr
		}
		return struct{}{}, nil
	})
	server.Handle(ipc.MethodReportExecution, func(_ context.Context, req *ipc.Request) (any, error) {
		var params ipc.ReportExecutionParams
		if err := req.DecodeParams(&params); err != nil {
			return nil, err
		}
		s.executeCalls = append(s.executeCalls, params)
		return ipc.ReportExecutionResult{}, nil
	})
	server.Handle(ipc.MethodAgentConfigChanged, func(_ context.Context, req *ipc.Request) (any, error) {
		var params ipc.AgentConfigChangedParams
		if err := req.DecodeParams(&params); err != nil {
			return nil, err
		}
		s.configCalls = append(s.configCalls, params)
		return struct{}{}, nil
	})
	server.Handle(ipc.MethodSummarize, func(_ context.Context, req *ipc.Request) (any, error) {
		var params ipc.SummarizeParams
		if err := req.DecodeParams(&params); err != nil {
			return nil, err
		}
		s.summarizeCalls = append(s.summarizeCalls, params)
		if s.summarizeErr != nil {
			return nil, s.summarizeErr
		}
		return s.summarizeResult, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		<-done
	})

	return ipc.NewClient(endpoint)
}

// testEndpoint returns a per-test endpoint for the platform's transport: a
// named pipe on Windows, where a filesystem path cannot be listened on, and a
// socket in a short directory everywhere else.
func testEndpoint(t *testing.T) string {
	t.Helper()

	// Unique per call, not per test. A test that starts two stub daemons — the
	// I-8 invariant does, to check that a partial rule match is not consent —
	// would otherwise ask Windows for a second pipe under a name the first one
	// still holds, and get "Access is denied". The unix path is already unique
	// per call because shortRuntimeDir makes a fresh directory each time, which
	// is why this only ever failed on Windows.
	nth := testEndpointSeq.Add(1)

	if runtime.GOOS == "windows" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("claude/%s/%d", t.Name(), nth)))
		return `\\.\pipe\intenter-test-` + hex.EncodeToString(sum[:])[:16]
	}
	return filepath.Join(shortRuntimeDir(t), "stub.sock")
}

// testEndpointSeq makes every endpoint name distinct across the whole package
// run, including tests that run in parallel.
var testEndpointSeq atomic.Uint64

// shortRuntimeDir keeps the socket path within the unix domain limit.
func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "agh")
	if err != nil {
		// Windows has no such limit.
		return t.TempDir()
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// runHook feeds one payload through the adapter and returns what it printed.
func runHook(t *testing.T, a *Adapter, payload string, env map[string]string) map[string]any {
	t.Helper()

	var stdout bytes.Buffer
	err := a.Run(context.Background(), adapter.IO{
		Stdin:  strings.NewReader(payload),
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
		Env:    func(name string) string { return env[name] },
	})
	if err != nil {
		t.Logf("hook reported: %v", err)
	}

	if strings.TrimSpace(stdout.String()) == "" {
		return nil
	}
	var decoded map[string]any
	if unmarshalErr := json.Unmarshal(stdout.Bytes(), &decoded); unmarshalErr != nil {
		t.Fatalf("hook wrote invalid JSON %q: %v", stdout.String(), unmarshalErr)
	}
	return decoded
}

// newTestAdapter builds an adapter wired to a stub daemon.
func newTestAdapter(t *testing.T, stub *stubDaemon) (*Adapter, string) {
	t.Helper()
	home := t.TempDir()
	p := fakePlatform{home: home, runtimeDir: filepath.Join(home, "run")}
	return New(p, config.Default(), logging.Discard()).WithClient(stub.serve(t)), home
}

func payload(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}

func TestHookEmitsAllowForAnApprovedCommand(t *testing.T) {
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeAllow,
		Class:        action.ClassApprovalMatch,
		Reason:       "approval 3 covers this action",
	}}
	a, _ := newTestAdapter(t, stub)

	decoded := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	output := hookOutput(decoded)
	if output == nil {
		t.Fatalf("want a decision, got %+v", decoded)
	}
	if output["permissionDecision"] != "allow" {
		t.Errorf("permissionDecision = %v, want allow", output["permissionDecision"])
	}

	if len(stub.evaluateCalls) != 1 {
		t.Fatalf("evaluate calls = %d, want 1", len(stub.evaluateCalls))
	}
	request := stub.evaluateCalls[0]
	if request.RawCommand != "npm run cleanup" || request.Tool != ToolBash {
		t.Errorf("request = %+v", request)
	}
	if request.Dialect != action.DialectPosix {
		t.Errorf("dialect = %s", request.Dialect)
	}
}

func TestHookEmitsDenyForABlockedCommand(t *testing.T) {
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeBlock,
		Class:        action.HardRuleClass("R2"),
		Reason:       "deleting ~/Documents, which is in your home directory",
	}}
	a, _ := newTestAdapter(t, stub)

	decoded := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	output := hookOutput(decoded)
	if output == nil || output["permissionDecision"] != "deny" {
		t.Fatalf("want a deny decision, got %+v", decoded)
	}
	if message, _ := decoded["systemMessage"].(string); !strings.Contains(message, "Intenter BLOCK") {
		t.Errorf("systemMessage = %q", message)
	}
}

func TestHookIsSilentForUngatedTools(t *testing.T) {
	stub := &stubDaemon{t: t}
	a, _ := newTestAdapter(t, stub)

	if decoded := runHook(t, a, payload(t, "pretooluse_read.json"), nil); decoded != nil {
		t.Errorf("want no output for a Read tool, got %+v", decoded)
	}
	if len(stub.evaluateCalls) != 0 {
		t.Error("an ungated tool must not reach the daemon")
	}
}

func TestHookIsSilentOnUnusableInput(t *testing.T) {
	stub := &stubDaemon{t: t}
	a, _ := newTestAdapter(t, stub)

	for _, input := range []string{"", "not json", `{"hook_event_name":`} {
		if decoded := runHook(t, a, input, nil); decoded != nil {
			t.Errorf("input %q: want silence, got %+v", input, decoded)
		}
	}
}

func TestHookDefersAndWarnsWhenTheDaemonFails(t *testing.T) {
	stub := &stubDaemon{t: t, evaluateErr: errors.New("daemon exploded")}
	a, _ := newTestAdapter(t, stub)

	decoded := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	if hookOutput(decoded) != nil {
		t.Fatalf("a failed evaluation must never produce a decision, got %+v", decoded)
	}
	message, _ := decoded["systemMessage"].(string)
	if !strings.Contains(message, "daemon unavailable") {
		t.Errorf("systemMessage = %q, want the daemon notice", message)
	}
}

func TestDaemonWarningIsRateLimitedPerSession(t *testing.T) {
	stub := &stubDaemon{t: t, evaluateErr: errors.New("daemon exploded")}
	a, _ := newTestAdapter(t, stub)

	first := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	if _, ok := first["systemMessage"]; !ok {
		t.Fatal("the first failure must warn")
	}

	second := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	if second != nil {
		t.Errorf("the warning must not repeat within the window, got %+v", second)
	}

	// Past the window it may warn again.
	a.now = func() time.Time { return time.Now().Add(2 * DaemonWarningInterval) }
	third := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	if _, ok := third["systemMessage"]; !ok {
		t.Error("after the interval the warning may repeat")
	}
}

func TestHookNeverPanicsOutward(t *testing.T) {
	// A crash in the gate must not become a crash in the session.
	stub := &stubDaemon{t: t}
	a, _ := newTestAdapter(t, stub)
	a.now = func() time.Time { panic("clock exploded") }

	var stdout bytes.Buffer
	err := a.Run(context.Background(), adapter.IO{
		Stdin:  strings.NewReader(payload(t, "pretooluse_bash.json")),
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
		Env:    func(string) string { return "" },
	})
	if err == nil {
		t.Error("the recovered panic should be reported to the caller's log")
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Errorf("a panicking hook must write nothing, got %q", stdout.String())
	}
}

func TestPermissionRequestRecordsTheDialogAndSuggestions(t *testing.T) {
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeAsk,
		Class:        action.ClassNoMatchingApproval,
		Reason:       "not approved yet",
	}}
	a, _ := newTestAdapter(t, stub)

	decoded := runHook(t, a, payload(t, "permissionrequest_bash.json"), nil)
	if decoded != nil {
		t.Errorf("an ASK must let the dialog proceed untouched, got %+v", decoded)
	}

	if len(stub.promptCalls) != 1 {
		t.Fatalf("record_prompt calls = %d, want 1", len(stub.promptCalls))
	}
	prompt := stub.promptCalls[0]
	if prompt.RawCommand != "npm run cleanup" {
		t.Errorf("prompt = %+v", prompt)
	}
	if len(prompt.Suggestions) != 1 {
		t.Errorf("suggestions = %+v, want them forwarded verbatim (§11.4)", prompt.Suggestions)
	}
}

func TestPermissionRequestEmitsAllowAndDeny(t *testing.T) {
	for _, tt := range []struct {
		name     string
		decision action.DecisionOutcome
		behavior string
	}{
		{"allow", action.OutcomeAllow, "allow"},
		{"block", action.OutcomeBlock, "deny"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
				AuditEventID: action.Ref(7),
				Decision:     tt.decision,
				Class:        action.ClassApprovalMatch,
				Reason:       "reason",
			}}
			a, _ := newTestAdapter(t, stub)

			decoded := runHook(t, a, payload(t, "permissionrequest_bash.json"), nil)
			output := hookOutput(decoded)
			if output == nil {
				t.Fatalf("want a decision, got %+v", decoded)
			}
			decision, _ := output["decision"].(map[string]any)
			if decision == nil || decision["behavior"] != tt.behavior {
				t.Errorf("decision = %+v, want behavior %q", decision, tt.behavior)
			}
		})
	}
}

func TestPostToolUseReportsTheExecution(t *testing.T) {
	stub := &stubDaemon{t: t}
	a, _ := newTestAdapter(t, stub)

	if decoded := runHook(t, a, payload(t, "posttooluse_bash.json"), nil); decoded != nil {
		t.Errorf("PostToolUse produces no output, got %+v", decoded)
	}

	if len(stub.executeCalls) != 1 {
		t.Fatalf("report_execution calls = %d, want 1", len(stub.executeCalls))
	}
	report := stub.executeCalls[0]
	if report.ToolUseID != "toolu_01A2b3C4d5E6f7G8h9I0" {
		t.Errorf("tool use id = %q", report.ToolUseID)
	}
	if report.Status != action.ExecutionCompleted {
		t.Errorf("status = %s", report.Status)
	}
	if !strings.Contains(report.ResponseSummary, "removed 42 files") {
		t.Errorf("summary = %q", report.ResponseSummary)
	}
}

func TestPostToolUseCarriesConsentFromClaudesOwnSettings(t *testing.T) {
	// §19.5 path (b): "yes, and don't ask again" lands in settings.local.json,
	// and the next PostToolUse reports it as consent.
	stub := &stubDaemon{t: t}
	home := t.TempDir()
	project := filepath.Join(home, "projects", "demo")

	for _, dir := range []string{filepath.Join(project, ".git"), filepath.Join(project, ".claude")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	settings := `{"permissions":{"allow":["Bash(npm run cleanup)"]}}`
	if err := os.WriteFile(filepath.Join(project, ".claude", "settings.local.json"), []byte(settings), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	p := fakePlatform{home: home, runtimeDir: filepath.Join(home, "run")}
	a := New(p, config.Default(), logging.Discard()).WithClient(stub.serve(t))

	runHook(t, a, payload(t, "posttooluse_bash.json"), map[string]string{EnvProjectDir: project})

	if len(stub.executeCalls) != 1 {
		t.Fatalf("report_execution calls = %d", len(stub.executeCalls))
	}
	consent := stub.executeCalls[0].AgentConsent
	if consent == nil {
		t.Fatal("want the consent signal computed from Claude's own settings")
	}
	if consent.Kind != action.ConsentKindPersistentRule {
		t.Errorf("kind = %q", consent.Kind)
	}
	if len(consent.RuleKeys) != 1 || !strings.Contains(consent.RuleKeys[0], "npm run cleanup") {
		t.Errorf("rule keys = %v", consent.RuleKeys)
	}
	if !consent.Exact {
		t.Error("an exact rule yields exact consent")
	}
}

func TestNoConsentWithoutAMatchingRule(t *testing.T) {
	stub := &stubDaemon{t: t}
	a, _ := newTestAdapter(t, stub)

	runHook(t, a, payload(t, "posttooluse_bash.json"), nil)

	if len(stub.executeCalls) != 1 {
		t.Fatalf("report_execution calls = %d", len(stub.executeCalls))
	}
	if consent := stub.executeCalls[0].AgentConsent; consent != nil {
		t.Errorf("consent = %+v, want none without a rule (I-8)", consent)
	}
}

func TestConfigChangeNotifiesTheDaemon(t *testing.T) {
	stub := &stubDaemon{t: t}
	a, _ := newTestAdapter(t, stub)

	event := `{"hook_event_name":"ConfigChange","source":"local_settings","file_path":"/x/.claude/settings.local.json"}`
	if decoded := runHook(t, a, event, nil); decoded != nil {
		t.Errorf("ConfigChange produces no output, got %+v", decoded)
	}
	if len(stub.configCalls) != 1 {
		t.Fatalf("agent_config_changed calls = %d, want 1", len(stub.configCalls))
	}
	if stub.configCalls[0].Source != "local_settings" {
		t.Errorf("source = %q", stub.configCalls[0].Source)
	}
}

func TestBypassModeDefersEverythingButBlock(t *testing.T) {
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeAsk,
		Class:        action.ClassApprovalMismatch,
		Reason:       "approval 3 no longer covers this action",
	}}
	a, _ := newTestAdapter(t, stub)

	event := strings.Replace(payload(t, "pretooluse_bash.json"),
		`"permission_mode": "default"`, `"permission_mode": "bypassPermissions"`, 1)

	if decoded := runHook(t, a, event, nil); decoded != nil {
		t.Errorf("want silence in bypass mode, got %+v", decoded)
	}
}

func TestHookOutputIsASingleJSONObjectPerInvocation(t *testing.T) {
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeBlock,
		Class:        action.HardRuleClass("R2"),
		Reason:       "deleting ~/Documents",
	}}
	a, _ := newTestAdapter(t, stub)

	var stdout bytes.Buffer
	_ = a.Run(context.Background(), adapter.IO{
		Stdin:  strings.NewReader(payload(t, "pretooluse_bash.json")),
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
		Env:    func(string) string { return "" },
	})

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("hook wrote %d lines, want exactly one JSON object:\n%s", len(lines), stdout.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

// One tool call reaches Intenter as up to three separate hook invocations,
// each in its own process, and they only add up to one story if the daemon can
// tell they belong together. PreToolUse and PostToolUse carry a tool_use_id;
// PermissionRequest does not, and is matched by session and command instead
// (§11.4, §11.5). These tests pin the keys that make that work.

func TestTheThreeHooksOfOneToolCallCarryMatchingKeys(t *testing.T) {
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeAsk,
		Class:        action.ClassNoMatchingApproval,
		Reason:       "not approved yet",
	}}
	a, _ := newTestAdapter(t, stub)

	// The PermissionRequest carries a tool_use_id it has no business sending.
	// Claude does not send one today, but the adapter must withhold it either
	// way: forwarded, it would make the daemon treat the dialog as a separate
	// invocation, and the prompt would be recorded against the wrong row.
	golden := payload(t, "permissionrequest_bash.json")
	permissionPayload := strings.Replace(golden,
		`"tool_name": "Bash",`, `"tool_name": "Bash",
		"tool_use_id": "toolu_01A2b3C4d5E6f7G8h9I0",`, 1)
	if permissionPayload == golden {
		t.Fatal("the golden payload changed shape; this test is only meaningful with a tool_use_id in it")
	}

	runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	runHook(t, a, permissionPayload, nil)
	runHook(t, a, payload(t, "posttooluse_bash.json"), nil)

	if len(stub.evaluateCalls) != 2 {
		t.Fatalf("evaluate calls = %d, want one per gating hook", len(stub.evaluateCalls))
	}
	pre, permission := stub.evaluateCalls[0], stub.evaluateCalls[1]

	if pre.ToolUseID == "" {
		t.Error("PreToolUse must carry its tool_use_id: it is what PostToolUse correlates against")
	}
	if permission.ToolUseID != "" {
		t.Errorf("PermissionRequest tool use id = %q, want none — Claude does not send one, and "+
			"inventing one would correlate to the wrong call (§11.4)", permission.ToolUseID)
	}
	if permission.SessionID != pre.SessionID || permission.RawCommand != pre.RawCommand {
		t.Errorf("PermissionRequest correlates by (session, command), got (%q, %q), want (%q, %q)",
			permission.SessionID, permission.RawCommand, pre.SessionID, pre.RawCommand)
	}
	if event, _ := permission.AdapterContext["hook_event"].(string); event != EventPermissionRequest {
		t.Errorf("hook_event = %q, want %q — the daemon reads it to pick the cache key",
			event, EventPermissionRequest)
	}

	if len(stub.promptCalls) != 1 {
		t.Fatalf("record_prompt calls = %d, want one for the dialog", len(stub.promptCalls))
	}
	if prompt := stub.promptCalls[0]; prompt.SessionID != pre.SessionID || prompt.RawCommand != pre.RawCommand {
		t.Errorf("prompt = (%q, %q), want the same session and command", prompt.SessionID, prompt.RawCommand)
	}

	if len(stub.executeCalls) != 1 {
		t.Fatalf("report_execution calls = %d, want one", len(stub.executeCalls))
	}
	report := stub.executeCalls[0]
	if report.ToolUseID != pre.ToolUseID {
		t.Errorf("execution reported for %q, want the evaluated call %q", report.ToolUseID, pre.ToolUseID)
	}
	if report.SessionID != pre.SessionID {
		t.Errorf("execution session = %q, want %q", report.SessionID, pre.SessionID)
	}
}

func TestPermissionSuggestionsAreForwardedVerbatim(t *testing.T) {
	// §11.4 step 2 keeps Claude's own suggestion objects because they are the
	// record of what the user was offered. Reshaping them here would make the
	// audit log a paraphrase of the dialog rather than a copy of it.
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeAsk,
		Class:        action.ClassNoMatchingApproval,
		Reason:       "not approved yet",
	}}
	a, _ := newTestAdapter(t, stub)

	raw := payload(t, "permissionrequest_bash.json")
	runHook(t, a, raw, nil)

	if len(stub.promptCalls) != 1 {
		t.Fatalf("record_prompt calls = %d, want 1", len(stub.promptCalls))
	}

	var sent struct {
		PermissionSuggestions []any `json:"permission_suggestions"`
	}
	if err := json.Unmarshal([]byte(raw), &sent); err != nil {
		t.Fatalf("read the golden payload: %v", err)
	}
	if want, got := canonicalJSON(t, sent.PermissionSuggestions), canonicalJSON(t, stub.promptCalls[0].Suggestions); want != got {
		t.Errorf("suggestions were rewritten in transit:\n got %s\nwant %s", got, want)
	}
}

func TestUnknownSuggestionFieldsSurviveTheAdapter(t *testing.T) {
	// Claude may add fields to a suggestion at any time. Dropping the ones this
	// build does not know about would silently shrink the audit record of a
	// future dialog, so the adapter must not model the shape at all.
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeAsk,
		Class:        action.ClassNoMatchingApproval,
		Reason:       "not approved yet",
	}}
	a, _ := newTestAdapter(t, stub)

	event := `{"hook_event_name":"PermissionRequest","session_id":"s1","cwd":"/w",
		"permission_mode":"default","tool_name":"Bash","tool_input":{"command":"npm run cleanup"},
		"permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash",
		"ruleContent":"npm run cleanup","behavior":"allow","destination":"localSettings",
		"futureField":{"nested":[1,2,3]}}],"anotherFutureField":true}]}`
	runHook(t, a, event, nil)

	if len(stub.promptCalls) != 1 {
		t.Fatalf("record_prompt calls = %d, want 1", len(stub.promptCalls))
	}
	forwarded := canonicalJSON(t, stub.promptCalls[0].Suggestions)
	for _, field := range []string{"futureField", "anotherFutureField", "nested"} {
		if !strings.Contains(forwarded, field) {
			t.Errorf("%q was dropped in transit: %s", field, forwarded)
		}
	}
}

func TestPermissionRequestNeverOffersToWriteClaudeRules(t *testing.T) {
	// §11.4 step 3: Intenter answers the dialog, it never asks Claude to
	// persist a string rule. A rule written here would outlive the resolution
	// that justified it — the exact bypass the product exists to close (I-8).
	outcomes := []action.DecisionOutcome{action.OutcomeAllow, action.OutcomeAsk, action.OutcomeBlock}
	for _, outcome := range outcomes {
		t.Run(string(outcome), func(t *testing.T) {
			stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
				AuditEventID: action.Ref(7),
				Decision:     outcome,
				Class:        action.ClassNoMatchingApproval,
				Reason:       "reason",
			}}
			a, _ := newTestAdapter(t, stub)

			decoded := runHook(t, a, payload(t, "permissionrequest_bash.json"), nil)
			if decoded == nil {
				return
			}
			rendered, err := json.Marshal(decoded)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(rendered), "updatedPermissions") {
				t.Errorf("response carries updatedPermissions: %s", rendered)
			}
		})
	}
}

func TestExecutionStatusIsReportedAsClaudeSawIt(t *testing.T) {
	// The recorded status is what makes history worth reading: "this ran and
	// failed" is a different fact from "this ran".
	tests := map[string]struct {
		response string
		want     action.ExecutionStatus
	}{
		"completed":   {`{"stdout":"ok\n","interrupted":false,"is_error":false}`, action.ExecutionCompleted},
		"failed":      {`{"stdout":"","stderr":"boom","is_error":true}`, action.ExecutionFailed},
		"interrupted": {`{"stdout":"","interrupted":true}`, action.ExecutionFailed},
		"unknown":     {`"a shape this build does not model"`, action.ExecutionUnknown},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stub := &stubDaemon{t: t}
			a, _ := newTestAdapter(t, stub)

			event := `{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/w",
				"tool_name":"Bash","tool_use_id":"toolu_9","tool_input":{"command":"npm run cleanup"},
				"tool_response":` + tc.response + `}`
			runHook(t, a, event, nil)

			if len(stub.executeCalls) != 1 {
				t.Fatalf("report_execution calls = %d, want 1", len(stub.executeCalls))
			}
			if got := stub.executeCalls[0].Status; got != tc.want {
				t.Errorf("status = %s, want %s", got, tc.want)
			}
			if got := stub.executeCalls[0].ToolUseID; got != "toolu_9" {
				t.Errorf("tool use id = %q, want toolu_9", got)
			}
		})
	}
}

// A decision and its delivery are separate facts, and §11.3 makes them come
// apart on purpose: a never-approved understood action is ASK and gets
// *deferred* to Claude's own dialog, while an approval mismatch is also ASK and
// gets the prompt *forced*. Recording only the decision leaves a user asking
// why one "ask" produced a prompt and the other produced nothing.

func TestTheHookRecordsWhatItActuallyEmitted(t *testing.T) {
	tests := map[string]struct {
		result     action.EvaluationResult
		bypassing  bool
		wantAction action.AdapterAction
	}{
		"an approved command is allowed": {
			result: action.EvaluationResult{
				AuditEventID: action.Ref(7), Decision: action.OutcomeAllow,
				Class: action.ClassApprovalMatch, Reason: "approval 3 covers this",
			},
			wantAction: action.AdapterAllow,
		},
		"a blocked command is denied": {
			result: action.EvaluationResult{
				AuditEventID: action.Ref(7), Decision: action.OutcomeBlock,
				Class: action.HardRuleClass("R2"), Reason: "deleting ~/Documents",
			},
			wantAction: action.AdapterDeny,
		},
		"a mismatch forces the prompt": {
			result: action.EvaluationResult{
				AuditEventID: action.Ref(7), Decision: action.OutcomeAsk,
				Class: action.ClassApprovalMismatch, Reason: "approval 3 no longer covers this",
			},
			wantAction: action.AdapterPrompt,
		},
		"a never-approved action is deferred": {
			result: action.EvaluationResult{
				AuditEventID: action.Ref(7), Decision: action.OutcomeAsk,
				Class: action.ClassNoMatchingApproval, Reason: "not approved yet",
			},
			wantAction: action.AdapterDefer,
		},
		"bypass mode defers an ask": {
			result: action.EvaluationResult{
				AuditEventID: action.Ref(7), Decision: action.OutcomeAsk,
				Class: action.ClassApprovalMismatch, Reason: "approval 3 no longer covers this",
			},
			bypassing:  true,
			wantAction: action.AdapterDefer,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stub := &stubDaemon{t: t, evaluateResult: tc.result}
			a, _ := newTestAdapter(t, stub)

			event := payload(t, "pretooluse_bash.json")
			if tc.bypassing {
				event = strings.Replace(event,
					`"permission_mode": "default"`, `"permission_mode": "bypassPermissions"`, 1)
			}
			runHook(t, a, event, nil)

			if len(stub.adapterActionCalls) != 1 {
				t.Fatalf("record_adapter_action calls = %d, want 1", len(stub.adapterActionCalls))
			}
			recorded := stub.adapterActionCalls[0]
			if recorded.Action != string(tc.wantAction) {
				t.Errorf("recorded %q, want %q", recorded.Action, tc.wantAction)
			}
			if recorded.AuditEventID != *tc.result.AuditEventID {
				t.Errorf("recorded against event %d, want %d",
					recorded.AuditEventID, *tc.result.AuditEventID)
			}
			if recorded.Agent != Agent {
				t.Errorf("agent = %q, want %q", recorded.Agent, Agent)
			}
		})
	}
}

func TestPermissionRequestRecordsItsDeliveryToo(t *testing.T) {
	// The dialog hook makes its own decision, and it is enforced a second time
	// there — so what it emitted is worth the same record.
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeBlock,
		Class:        action.HardRuleClass("R2"),
		Reason:       "deleting ~/Documents",
	}}
	a, _ := newTestAdapter(t, stub)

	runHook(t, a, payload(t, "permissionrequest_bash.json"), nil)

	if len(stub.adapterActionCalls) != 1 {
		t.Fatalf("record_adapter_action calls = %d, want 1", len(stub.adapterActionCalls))
	}
	if got := stub.adapterActionCalls[0].Action; got != string(action.AdapterDeny) {
		t.Errorf("recorded %q, want deny", got)
	}
}

func TestTheAnnotationIsWrittenAfterTheAnswer(t *testing.T) {
	// The hook exists to add no friction, so nothing the agent is waiting on
	// may sit behind an audit write.
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeBlock,
		Class:        action.HardRuleClass("R2"),
		Reason:       "deleting ~/Documents",
	}}
	a, _ := newTestAdapter(t, stub)

	// A stdout that records when it was written, against the daemon's own log.
	var order []string
	stdout := &recordingWriter{onWrite: func() { order = append(order, "response") }}
	stub.onAdapterAction = func() { order = append(order, "annotation") }

	err := a.Run(context.Background(), adapter.IO{
		Stdin:  strings.NewReader(payload(t, "pretooluse_bash.json")),
		Stdout: stdout,
		Stderr: &bytes.Buffer{},
		Env:    func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(order) != 2 || order[0] != "response" || order[1] != "annotation" {
		t.Errorf("order = %v, want the response written before the annotation", order)
	}
}

func TestAFailedAnnotationIsInvisibleToTheAgent(t *testing.T) {
	// The annotation is bookkeeping. Losing it must cost the audit log a row's
	// worth of detail and cost the session nothing (I-12).
	stub := &stubDaemon{
		t: t,
		evaluateResult: action.EvaluationResult{
			AuditEventID: action.Ref(7),
			Decision:     action.OutcomeBlock,
			Class:        action.HardRuleClass("R2"),
			Reason:       "deleting ~/Documents",
		},
		adapterActionErr: errors.New("INTERNAL: database is locked"),
	}
	a, _ := newTestAdapter(t, stub)

	decoded := runHook(t, a, payload(t, "pretooluse_bash.json"), nil)
	output := hookOutput(decoded)
	if output == nil {
		t.Fatal("the decision must still be delivered")
	}
	if output["permissionDecision"] != "deny" {
		t.Errorf("decision = %v, want deny despite the failed annotation", output["permissionDecision"])
	}
}

func TestTheAnnotationCannotHoldTheHookOpen(t *testing.T) {
	// The daemon can die between answering the evaluation and receiving the
	// annotation. Waiting out the default IPC timeouts for a write nobody is
	// waiting on would stall the session for seconds over bookkeeping.
	if AdapterActionTimeout >= ipc.ConnectTimeout+ipc.RequestTimeout {
		t.Errorf("the annotation may take %s, as long as a call the user is waiting on (%s + %s)",
			AdapterActionTimeout, ipc.ConnectTimeout, ipc.RequestTimeout)
	}
	if AdapterActionTimeout <= 0 {
		t.Error("an unbounded annotation is the thing this bound exists to prevent")
	}
}

func TestADeferredActionIsStillRecorded(t *testing.T) {
	// A deferral produces no output at all, which is exactly the case a user
	// asks about later: "Intenter said ask — why did I see nothing?"
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		AuditEventID: action.Ref(7),
		Decision:     action.OutcomeAsk,
		Class:        action.ClassUnresolvedCommand,
		Reason:       "Intenter cannot tell what this command would do",
	}}
	a, _ := newTestAdapter(t, stub)

	if decoded := runHook(t, a, payload(t, "pretooluse_bash.json"), nil); decoded != nil {
		t.Fatalf("want silence for a deferral, got %+v", decoded)
	}
	if len(stub.adapterActionCalls) != 1 {
		t.Fatalf("record_adapter_action calls = %d, want 1 even with no output",
			len(stub.adapterActionCalls))
	}
	if got := stub.adapterActionCalls[0].Action; got != string(action.AdapterDefer) {
		t.Errorf("recorded %q, want defer", got)
	}
}

func TestADryRunIsNeverAnnotated(t *testing.T) {
	// Setup's self-test writes no audit row, so there is nothing to annotate
	// and nothing to invent (§12.2).
	stub := &stubDaemon{t: t, evaluateResult: action.EvaluationResult{
		Decision: action.OutcomeBlock,
		Class:    action.HardRuleClass("R2"),
		Reason:   "deleting ~/Documents",
	}}
	a, _ := newTestAdapter(t, stub)

	runHook(t, a, payload(t, "pretooluse_bash.json"), map[string]string{EnvSelfTest: "1"})

	if len(stub.adapterActionCalls) != 0 {
		t.Errorf("record_adapter_action calls = %+v, want none for a dry run", stub.adapterActionCalls)
	}
}

// recordingWriter notes when it was first written to.
type recordingWriter struct {
	bytes.Buffer
	onWrite func()
	written bool
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	if !w.written {
		w.written = true
		if w.onWrite != nil {
			w.onWrite()
		}
	}
	return w.Buffer.Write(p)
}

// canonicalJSON renders a value as JSON with map keys sorted, so two structures
// can be compared as text.
func canonicalJSON(t *testing.T, value any) string {
	t.Helper()
	rendered, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(rendered)
}
