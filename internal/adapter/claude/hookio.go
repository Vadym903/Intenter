package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Vadym903/Intenter/internal/action"
)

// Agent is the identifier this adapter registers under and records in
// approvals and audit rows.
const Agent = "claude"

// Hook event names Intenter installs handlers for (contracts/claude-hooks.md).
const (
	EventPreToolUse        = "PreToolUse"
	EventPermissionRequest = "PermissionRequest"
	EventPostToolUse       = "PostToolUse"
	EventConfigChange      = "ConfigChange"
	EventSessionEnd        = "SessionEnd"
)

// Permission modes Claude reports. Only bypassPermissions changes what the
// adapter may emit (§11.3).
const (
	ModeDefault           = "default"
	ModeAcceptEdits       = "acceptEdits"
	ModePlan              = "plan"
	ModeBypassPermissions = "bypassPermissions"
	ModeDontAsk           = "dontAsk"
)

// Tool names Intenter gates. Every other tool is left entirely alone.
const (
	ToolBash       = "Bash"
	ToolPowerShell = "PowerShell"
)

// EnvProjectDir is Claude's project-root hint, used both as the workspace hint
// and as the anchor for settings discovery.
const EnvProjectDir = "CLAUDE_PROJECT_DIR"

// MaxResponseSummaryBytes bounds what a command's output may contribute to the
// audit log (§11.5).
const MaxResponseSummaryBytes = 1024

// MaxHookInputBytes bounds one hook payload. A larger one is refused rather
// than buffered: the agent's own flow still runs.
const MaxHookInputBytes = 4 << 20

// Event is one Claude Code hook invocation, limited to the fields Intenter
// consumes (contracts/claude-hooks.md).
type Event struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`
	ToolName       string `json:"tool_name"`
	ToolUseID      string `json:"tool_use_id"`

	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`

	// ToolResponse is whatever the tool produced; its shape varies, so it is
	// summarized rather than interpreted.
	ToolResponse json.RawMessage `json:"tool_response"`

	// PermissionSuggestions are stored verbatim for the audit log (§11.4).
	PermissionSuggestions []any `json:"permission_suggestions"`

	// Source and FilePath appear on ConfigChange events.
	Source   string `json:"source"`
	FilePath string `json:"file_path"`

	// SessionEndReason appears on SessionEnd: "clear", "resume", "logout",
	// "prompt_input_exit" or "other". It is recorded but not acted on — the
	// session's decisions are worth reporting however it ended.
	SessionEndReason string `json:"session_end_reason"`
}

// DecodeEvent reads one hook payload. A payload Intenter cannot parse is an
// error the caller turns into silence, never into a decision.
func DecodeEvent(stdin io.Reader) (*Event, error) {
	raw, err := io.ReadAll(io.LimitReader(stdin, MaxHookInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("claude: read hook input: %w", err)
	}
	if len(raw) > MaxHookInputBytes {
		return nil, fmt.Errorf("claude: hook input exceeds %d bytes", MaxHookInputBytes)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("claude: empty hook input")
	}

	event := &Event{}
	if err := json.Unmarshal(raw, event); err != nil {
		return nil, fmt.Errorf("claude: parse hook input: %w", err)
	}
	return event, nil
}

// Gated reports whether Intenter has anything to say about this event. Every
// other tool and event is left to Claude untouched.
func (e *Event) Gated() bool {
	switch e.HookEventName {
	case EventPreToolUse, EventPermissionRequest, EventPostToolUse:
	case EventConfigChange:
		return true
	case EventSessionEnd:
		// The summary is per session, so without an id there is nothing to
		// count and nothing worth saying.
		return e.SessionID != ""
	default:
		return false
	}
	if !e.gatedTool() {
		return false
	}
	// A PostToolUse still reports an execution even without the command text,
	// because it is correlated by tool_use_id.
	return e.ToolInput.Command != "" || e.HookEventName == EventPostToolUse
}

func (e *Event) gatedTool() bool {
	return e.ToolName == ToolBash || e.ToolName == ToolPowerShell
}

// Dialect is the shell syntax the tool's command is written in.
//
// Claude's Bash tool is Git Bash on Windows, so it stays POSIX there; only the
// PowerShell tool changes the dialect (§14.4).
func (e *Event) Dialect() action.Dialect {
	if e.ToolName == ToolPowerShell {
		return action.DialectPowerShell
	}
	return action.DialectPosix
}

// Bypassing reports whether Claude is in the mode where a forced prompt would
// become a denial, so Intenter emits only BLOCK (§11.3).
func (e *Event) Bypassing() bool { return e.PermissionMode == ModeBypassPermissions }

// ActionRequest converts the event into the agent-independent request the
// daemon evaluates. No hook JSON, settings or permission rule crosses this
// boundary (INVARIANT I-7).
//
// A command longer than the daemon evaluates is truncated here and flagged,
// never dropped: sending the whole thing would exceed the IPC message limit and
// the request would fail, which the hook turns into a deferral — an agent could
// then pad a dangerous command past the limit to skip the safety floor. A
// truncated command is marked so the daemon forces a prompt (R13) instead.
func (e *Event) ActionRequest(projectHint string, consent *action.AgentConsent, now time.Time) action.ActionRequest {
	command := e.ToolInput.Command
	truncated := false
	if len(command) > action.MaxRawCommandBytes {
		command = truncateUTF8(command, action.MaxRawCommandBytes)
		truncated = true
	}
	return action.ActionRequest{
		Agent:               Agent,
		SessionID:           e.SessionID,
		ToolUseID:           e.toolUseIDForRequest(),
		Tool:                e.ToolName,
		Dialect:             e.Dialect(),
		RawCommand:          command,
		RawCommandTruncated: truncated,
		Cwd:                 e.Cwd,
		ProjectHint:         projectHint,
		AgentConsent:        consent,
		AdapterContext: map[string]any{
			"hook_event":      e.HookEventName,
			"permission_mode": e.PermissionMode,
		},
		ReceivedAt: now,
	}
}

// toolUseIDForRequest withholds the tool id on PermissionRequest, which is the
// one event Claude sends without one. Correlation there happens by session and
// command instead (§11.4).
func (e *Event) toolUseIDForRequest() string {
	if e.HookEventName == EventPermissionRequest {
		return ""
	}
	return e.ToolUseID
}

// ExecutionStatus reads the tool response for signs the command did not
// complete. The shape varies by tool version, so anything unrecognized is
// reported as unknown rather than guessed as success (§11.5).
func (e *Event) ExecutionStatus() action.ExecutionStatus {
	if len(e.ToolResponse) == 0 {
		return action.ExecutionUnknown
	}

	var structured struct {
		Interrupted *bool   `json:"interrupted"`
		IsError     *bool   `json:"is_error"`
		Success     *bool   `json:"success"`
		Stderr      *string `json:"stderr"`
	}
	if err := json.Unmarshal(e.ToolResponse, &structured); err != nil {
		return action.ExecutionUnknown
	}

	switch {
	case structured.Interrupted != nil && *structured.Interrupted:
		return action.ExecutionFailed
	case structured.IsError != nil && *structured.IsError:
		return action.ExecutionFailed
	case structured.Success != nil:
		if *structured.Success {
			return action.ExecutionCompleted
		}
		return action.ExecutionFailed
	case structured.Interrupted != nil || structured.IsError != nil || structured.Stderr != nil:
		return action.ExecutionCompleted
	}
	return action.ExecutionUnknown
}

// ResponseSummary renders a bounded, UTF-8-safe excerpt of the tool response
// for the audit log (§11.5).
func (e *Event) ResponseSummary() string {
	if len(e.ToolResponse) == 0 {
		return ""
	}

	text := ""
	var structured struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal(e.ToolResponse, &structured); err == nil {
		text = strings.TrimSpace(strings.TrimSpace(structured.Stdout) + "\n" + strings.TrimSpace(structured.Stderr))
	}
	if text == "" {
		text = string(e.ToolResponse)
	}
	return truncateUTF8(strings.TrimSpace(text), MaxResponseSummaryBytes)
}

// truncateUTF8 cuts text to a byte budget without splitting a rune.
func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// ProjectHint returns Claude's project-root hint, which the resolver uses when
// the working directory is not inside a git repository (§16.2).
func ProjectHint(lookup func(string) string) string {
	if lookup == nil {
		return ""
	}
	return strings.TrimSpace(lookup(EnvProjectDir))
}
