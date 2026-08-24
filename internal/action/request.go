package action

import "time"

// MaxRawCommandBytes is the largest raw command Intenter evaluates
// (PROTOTYPE_SPEC.md §13.1). A longer command is not rejected as a bad request
// — that would hand it back to the agent's native flow with the safety floor
// skipped — but truncated to this size, marked, and forced to a prompt (R13).
const MaxRawCommandBytes = 64 * 1024

// ActionRequest is what an adapter submits for evaluation. It is agent- and
// OS-independent: no hook JSON, settings or permission rules reach the core
// (PROTOTYPE_SPEC.md §13.1, INVARIANT I-7).
type ActionRequest struct {
	Agent        string  `json:"agent"`
	AgentVersion string  `json:"agent_version,omitempty"`
	SessionID    string  `json:"session_id"`
	ToolUseID    string  `json:"tool_use_id,omitempty"`
	Tool         string  `json:"tool"`
	Dialect      Dialect `json:"dialect"`
	RawCommand   string  `json:"raw_command"`
	// RawCommandTruncated is set by an adapter that cut RawCommand down to
	// MaxRawCommandBytes. The prefix that arrived must not be evaluated as if it
	// were the whole command; the daemon records it and forces a prompt.
	RawCommandTruncated bool           `json:"raw_command_truncated,omitempty"`
	Cwd                 string         `json:"cwd"`
	ProjectHint         string         `json:"project_hint,omitempty"`
	AgentConsent        *AgentConsent  `json:"agent_consent,omitempty"`
	AdapterContext      map[string]any `json:"adapter_context,omitempty"`
	ReceivedAt          time.Time      `json:"received_at,omitempty"`
}

// AgentConsent is the adapter's report that the agent already holds persistent
// user consent covering this raw command. The core uses it only for validated,
// once-only import (PROTOTYPE_SPEC.md §11.6, §19.5, INVARIANT I-8).
type AgentConsent struct {
	Kind     string   `json:"kind"`
	RuleKeys []string `json:"rule_keys"`
	Exact    bool     `json:"exact"`
}

// ConsentKindPersistentRule is the only consent kind the prototype accepts.
const ConsentKindPersistentRule = "persistent_rule"

// Usable reports whether the consent signal is present and of a kind the core
// knows how to import.
func (c *AgentConsent) Usable() bool {
	return c != nil && c.Kind == ConsentKindPersistentRule && len(c.RuleKeys) > 0
}
