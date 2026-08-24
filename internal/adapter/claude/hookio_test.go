package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

func loadEvent(t *testing.T, name string) *Event {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	event, err := DecodeEvent(strings.NewReader(string(content)))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return event
}

func TestDecodeGoldenPreToolUse(t *testing.T) {
	event := loadEvent(t, "pretooluse_bash.json")

	if event.HookEventName != EventPreToolUse {
		t.Errorf("hook event = %q", event.HookEventName)
	}
	if event.ToolName != ToolBash {
		t.Errorf("tool = %q", event.ToolName)
	}
	if event.ToolInput.Command != "npm run cleanup" {
		t.Errorf("command = %q", event.ToolInput.Command)
	}
	if event.ToolUseID != "toolu_01A2b3C4d5E6f7G8h9I0" {
		t.Errorf("tool use id = %q", event.ToolUseID)
	}
	if event.Cwd != "/Users/u/projects/demo" {
		t.Errorf("cwd = %q", event.Cwd)
	}
	if !event.Gated() {
		t.Error("a Bash PreToolUse must be gated")
	}
	if event.Bypassing() {
		t.Error("default mode is not bypassing")
	}
}

func TestDecodeGoldenPermissionRequest(t *testing.T) {
	event := loadEvent(t, "permissionrequest_bash.json")

	if !event.Gated() {
		t.Fatal("a Bash PermissionRequest must be gated")
	}
	if len(event.PermissionSuggestions) != 1 {
		t.Fatalf("suggestions = %+v, want one, stored verbatim", event.PermissionSuggestions)
	}
	// Claude does not send a tool_use_id on this event, so the request must not
	// invent one: correlation happens by session and command (§11.4).
	request := event.ActionRequest("", nil, time.Now())
	if request.ToolUseID != "" {
		t.Errorf("tool use id = %q, want none on a PermissionRequest", request.ToolUseID)
	}
}

func TestDecodeGoldenPostToolUse(t *testing.T) {
	event := loadEvent(t, "posttooluse_bash.json")

	if !event.Gated() {
		t.Fatal("a Bash PostToolUse must be gated")
	}
	if got := event.ExecutionStatus(); got != action.ExecutionCompleted {
		t.Errorf("execution status = %s, want completed", got)
	}
	if summary := event.ResponseSummary(); !strings.Contains(summary, "removed 42 files") {
		t.Errorf("summary = %q, want the command output", summary)
	}
}

func TestUngatedToolsAreLeftAlone(t *testing.T) {
	event := loadEvent(t, "pretooluse_read.json")
	if event.Gated() {
		t.Error("only Bash and PowerShell are gated (contracts/claude-hooks.md)")
	}
}

func TestGatedRejectsUnknownEventsAndEmptyCommands(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  bool
	}{
		{"unknown event", Event{HookEventName: "SessionStart", ToolName: ToolBash}, false},
		{"unknown tool", Event{HookEventName: EventPreToolUse, ToolName: "Write"}, false},
		{"no command", Event{HookEventName: EventPreToolUse, ToolName: ToolBash}, false},
		{"post tool use without a command", Event{HookEventName: EventPostToolUse, ToolName: ToolBash}, true},
		{"config change", Event{HookEventName: EventConfigChange}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.Gated(); got != tt.want {
				t.Errorf("Gated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDialectFollowsTheTool(t *testing.T) {
	bash := Event{ToolName: ToolBash}
	if got := bash.Dialect(); got != action.DialectPosix {
		t.Errorf("Bash dialect = %s, want posix", got)
	}

	// Claude's Bash tool is Git Bash on Windows, so it stays POSIX there; the
	// host OS never changes the dialect (§14.4).
	powershell := Event{ToolName: ToolPowerShell}
	if got := powershell.Dialect(); got != action.DialectPowerShell {
		t.Errorf("PowerShell dialect = %s, want powershell", got)
	}
}

func TestActionRequestCarriesNoAgentSpecificData(t *testing.T) {
	// INVARIANT I-7: no hook JSON, settings or permission rule reaches the core.
	event := loadEvent(t, "pretooluse_bash.json")
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	request := event.ActionRequest("/Users/u/projects/demo", nil, now)
	if request.Agent != Agent {
		t.Errorf("agent = %q", request.Agent)
	}
	if request.Tool != ToolBash || request.RawCommand != "npm run cleanup" {
		t.Errorf("request = %+v", request)
	}
	if request.ProjectHint != "/Users/u/projects/demo" {
		t.Errorf("project hint = %q", request.ProjectHint)
	}
	if !request.ReceivedAt.Equal(now) {
		t.Errorf("received at = %s", request.ReceivedAt)
	}
	if request.AdapterContext["hook_event"] != EventPreToolUse {
		t.Errorf("adapter context = %+v", request.AdapterContext)
	}
	if request.AdapterContext["permission_mode"] != ModeDefault {
		t.Errorf("adapter context = %+v", request.AdapterContext)
	}
}

func TestBypassingModeIsDetected(t *testing.T) {
	for mode, want := range map[string]bool{
		ModeDefault:           false,
		ModeAcceptEdits:       false,
		ModePlan:              false,
		ModeDontAsk:           false,
		ModeBypassPermissions: true,
	} {
		event := Event{PermissionMode: mode}
		if got := event.Bypassing(); got != want {
			t.Errorf("mode %s: Bypassing() = %v, want %v", mode, got, want)
		}
	}
}

func TestDecodeRefusesUnusableInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace", "   \n"},
		{"not json", "this is not json"},
		{"truncated", `{"hook_event_name":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeEvent(strings.NewReader(tt.input)); err == nil {
				t.Error("want an error the caller turns into silence")
			}
		})
	}
}

func TestDecodeRefusesAnOversizedPayload(t *testing.T) {
	huge := `{"hook_event_name":"PreToolUse","tool_input":{"command":"` +
		strings.Repeat("a", MaxHookInputBytes) + `"}}`
	if _, err := DecodeEvent(strings.NewReader(huge)); err == nil {
		t.Error("want a refusal rather than an unbounded buffer")
	}
}

func TestExecutionStatusFromToolResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     action.ExecutionStatus
	}{
		{"no response", "", action.ExecutionUnknown},
		{"unparsable", `"just a string"`, action.ExecutionUnknown},
		{"nothing recognizable", `{"other":1}`, action.ExecutionUnknown},
		{"interrupted", `{"interrupted":true}`, action.ExecutionFailed},
		{"error flag", `{"is_error":true}`, action.ExecutionFailed},
		{"success false", `{"success":false}`, action.ExecutionFailed},
		{"success true", `{"success":true}`, action.ExecutionCompleted},
		{"clean run", `{"interrupted":false,"is_error":false,"stdout":"ok"}`, action.ExecutionCompleted},
		{"stderr only", `{"stderr":"warning"}`, action.ExecutionCompleted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := Event{ToolResponse: []byte(tt.response)}
			if got := event.ExecutionStatus(); got != tt.want {
				t.Errorf("ExecutionStatus() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestResponseSummaryIsBoundedAndUTF8Safe(t *testing.T) {
	long := strings.Repeat("é", MaxResponseSummaryBytes)
	event := Event{ToolResponse: []byte(`{"stdout":"` + long + `"}`)}

	summary := event.ResponseSummary()
	if len(summary) > MaxResponseSummaryBytes {
		t.Errorf("summary is %d bytes, want at most %d", len(summary), MaxResponseSummaryBytes)
	}
	if !strings.HasPrefix(long, summary) {
		t.Error("the summary must be a prefix of the output")
	}
	for _, r := range summary {
		if r == '�' {
			t.Fatal("the summary must not split a rune")
		}
	}
}

func TestResponseSummaryFallsBackToRawJSON(t *testing.T) {
	event := Event{ToolResponse: []byte(`{"result":"done"}`)}
	if summary := event.ResponseSummary(); !strings.Contains(summary, "done") {
		t.Errorf("summary = %q, want the raw response when it has no stdout", summary)
	}

	empty := Event{}
	if summary := empty.ResponseSummary(); summary != "" {
		t.Errorf("summary = %q, want empty", summary)
	}
}

func TestProjectHintReadsTheEnvironment(t *testing.T) {
	lookup := func(name string) string {
		if name == EnvProjectDir {
			return "  /Users/u/projects/demo  "
		}
		return ""
	}
	if got := ProjectHint(lookup); got != "/Users/u/projects/demo" {
		t.Errorf("ProjectHint() = %q", got)
	}
	if got := ProjectHint(nil); got != "" {
		t.Errorf("ProjectHint(nil) = %q", got)
	}
}
