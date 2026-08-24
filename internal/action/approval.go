package action

import (
	"sort"
	"strings"
	"time"
)

// Approval is a persisted semantic approval: the resolved effects a user
// consented to, in one project, under recorded fingerprints
// (PROTOTYPE_SPEC.md §19.1, data-model.md §2.2).
type Approval struct {
	ID          int64           `json:"id"`
	ProjectID   string          `json:"project_id"`
	Kind        ApprovalKind    `json:"kind"`
	SemanticOps []SemanticOp    `json:"semantic_ops"`
	Envelope    []EnvelopeEntry `json:"envelope"`
	// Targets holds the display paths an EXACT approval covers; nil for SEMANTIC.
	Targets []string        `json:"targets,omitempty"`
	Network []NetworkTarget `json:"network"`
	// Conditions are the fingerprints that must still hold (§20.3 rule 3).
	Conditions            []ApprovalCondition `json:"conditions,omitempty"`
	EngineVersion         int                 `json:"engine_version"`
	Origin                ApprovalOrigin      `json:"origin"`
	OriginRef             string              `json:"origin_ref,omitempty"`
	CreatedFromEventID    *int64              `json:"created_from_event_id,omitempty"`
	CreatedFromRawCommand string              `json:"created_from_raw_command"`
	CreatedByAgent        string              `json:"created_by_agent"`
	State                 ApprovalState       `json:"state"`
	Note                  string              `json:"note,omitempty"`
	CreatedAt             time.Time           `json:"created_at"`
	LastUsedAt            *time.Time          `json:"last_used_at,omitempty"`
	UseCount              int64               `json:"use_count"`
	DisabledAt            *time.Time          `json:"disabled_at,omitempty"`
	RevokedAt             *time.Time          `json:"revoked_at,omitempty"`
}

// ApprovalCondition is one condition an approval depends on. The prototype has
// a single kind: fingerprint (data-model.md §2.3).
type ApprovalCondition struct {
	Kind  string `json:"kind"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ConditionFingerprint is the only condition kind in schema v1.
const ConditionFingerprint = "fingerprint"

// Fingerprints returns the approval's fingerprint conditions as a key→value map.
func (a *Approval) Fingerprints() map[string]string {
	out := make(map[string]string, len(a.Conditions))
	for _, c := range a.Conditions {
		if c.Kind == ConditionFingerprint {
			out[c.Key] = c.Value
		}
	}
	return out
}

// FingerprintKeys returns the sorted fingerprint keys of the approval.
func (a *Approval) FingerprintKeys() []string {
	keys := make([]string, 0, len(a.Conditions))
	for _, c := range a.Conditions {
		if c.Kind == ConditionFingerprint {
			keys = append(keys, c.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

// EnvelopeKeys returns the sorted comparison keys of the approval envelope.
func (a *Approval) EnvelopeKeys() []string {
	keys := make([]string, 0, len(a.Envelope))
	for _, entry := range a.Envelope {
		keys = append(keys, entry.Key())
	}
	sort.Strings(keys)
	return keys
}

// NetworkKeys returns the sorted comparison keys of the approval's network set.
func (a *Approval) NetworkKeys() []string {
	keys := make([]string, 0, len(a.Network))
	for _, n := range a.Network {
		keys = append(keys, n.Key())
	}
	sort.Strings(keys)
	return keys
}

// Active reports whether the approval may be matched.
func (a *Approval) Active() bool { return a != nil && a.State == ApprovalActive }

// Summary renders the one-line description used by `intenter approvals`
// (contracts/cli.md).
func (a *Approval) Summary() string {
	parts := make([]string, 0, len(a.Envelope))
	for _, entry := range a.Envelope {
		parts = append(parts, entry.String())
	}
	summary := strings.Join(parts, ", ")
	if len(a.Targets) > 0 {
		summary += " " + strings.Join(a.Targets, " ")
	}
	for _, n := range a.Network {
		summary += " " + n.String()
	}
	return strings.TrimSpace(summary)
}

// OpsString renders the ordered semantic ops, e.g. "RUN_SCRIPT>FS_DELETE".
func (a *Approval) OpsString() string {
	parts := make([]string, 0, len(a.SemanticOps))
	for _, op := range a.SemanticOps {
		parts = append(parts, string(op))
	}
	return strings.Join(parts, ">")
}

// ApprovalEventType records what happened to an approval (data-model.md §2.4).
type ApprovalEventType string

const (
	ApprovalEventCreated    ApprovalEventType = "created"
	ApprovalEventMatched    ApprovalEventType = "matched"
	ApprovalEventNotMatched ApprovalEventType = "not_matched"
	ApprovalEventDisabled   ApprovalEventType = "disabled"
	ApprovalEventEnabled    ApprovalEventType = "enabled"
	ApprovalEventRevoked    ApprovalEventType = "revoked"
)

// ApprovalEvent is one entry of an approval's history.
type ApprovalEvent struct {
	ID           int64             `json:"id"`
	ApprovalID   int64             `json:"approval_id"`
	Type         ApprovalEventType `json:"event_type"`
	AuditEventID *int64            `json:"audit_event_id,omitempty"`
	At           time.Time         `json:"at"`
	Details      map[string]any    `json:"details,omitempty"`
}

// Project is a workspace Intenter has seen (data-model.md §2.1).
type Project struct {
	ID          string    `json:"id"`
	RootPath    string    `json:"root_path"`
	RemoteURL   string    `json:"remote_url,omitempty"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// RuleImport records that an agent permission rule was converted into an
// approval once, for one project and raw command (data-model.md §2.6, §19.5).
type RuleImport struct {
	ID         int64     `json:"id"`
	ProjectID  string    `json:"project_id"`
	Agent      string    `json:"agent"`
	RuleKey    string    `json:"rule_key"`
	RawCommand string    `json:"raw_command"`
	ApprovalID int64     `json:"approval_id"`
	ImportedAt time.Time `json:"imported_at"`
}
