package action

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Scope classifies where a target lives relative to the workspace, the user's
// home and the operating system (PROTOTYPE_SPEC.md §16.3). Scopes are disjoint
// and exhaustive.
type Scope string

const (
	ScopeSystem             Scope = "SYSTEM"
	ScopeWorkspaceGenerated Scope = "WORKSPACE_GENERATED"
	ScopeWorkspace          Scope = "WORKSPACE"
	ScopeHome               Scope = "HOME"
	ScopeOutsideWorkspace   Scope = "OUTSIDE_WORKSPACE"
)

// EffectType is a low-level effect on the system (PROTOTYPE_SPEC.md §17.1).
type EffectType string

const (
	EffectRead    EffectType = "READ"
	EffectWrite   EffectType = "WRITE"
	EffectCreate  EffectType = "CREATE"
	EffectDelete  EffectType = "DELETE"
	EffectExecute EffectType = "EXECUTE"
	EffectNetwork EffectType = "NETWORK"
)

// SemanticOp names what a command means, independent of how it was written
// (PROTOTYPE_SPEC.md §17.2).
type SemanticOp string

const (
	OpFSRead              SemanticOp = "FS_READ"
	OpFSCreate            SemanticOp = "FS_CREATE"
	OpFSCopy              SemanticOp = "FS_COPY"
	OpFSMove              SemanticOp = "FS_MOVE"
	OpFSDelete            SemanticOp = "FS_DELETE"
	OpGitStatus           SemanticOp = "GIT_STATUS"
	OpGitDiff             SemanticOp = "GIT_DIFF"
	OpGitLog              SemanticOp = "GIT_LOG"
	OpGitShow             SemanticOp = "GIT_SHOW"
	OpGitBranch           SemanticOp = "GIT_BRANCH"
	OpGitRevParse         SemanticOp = "GIT_REV_PARSE"
	OpGitAdd              SemanticOp = "GIT_ADD"
	OpGitCommit           SemanticOp = "GIT_COMMIT"
	OpGitCheckout         SemanticOp = "GIT_CHECKOUT"
	OpGitReset            SemanticOp = "GIT_RESET"
	OpGitPush             SemanticOp = "GIT_PUSH"
	OpRunScript           SemanticOp = "RUN_SCRIPT"
	OpRunTests            SemanticOp = "RUN_TESTS"
	OpBuild               SemanticOp = "BUILD"
	OpClean               SemanticOp = "CLEAN"
	OpBuildToolInfo       SemanticOp = "BUILD_TOOL_INFO"
	OpInstallDependencies SemanticOp = "INSTALL_DEPENDENCIES"
	OpHTTPRequest         SemanticOp = "HTTP_REQUEST"
	OpShellNavigate       SemanticOp = "SHELL_NAVIGATE"
	OpNoop                SemanticOp = "NOOP"
	OpUnknown             SemanticOp = "UNKNOWN"
)

// ResolutionStatus records how well Intenter understands an action
// (PROTOTYPE_SPEC.md §15.2). Only RESOLVED and DECLARED are approvable.
type ResolutionStatus string

const (
	StatusResolved      ResolutionStatus = "RESOLVED"
	StatusDeclared      ResolutionStatus = "DECLARED"
	StatusUnresolved    ResolutionStatus = "UNRESOLVED"
	StatusParseFailed   ResolutionStatus = "PARSE_FAILED"
	StatusContextFailed ResolutionStatus = "CONTEXT_FAILED"
)

// statusRank orders resolution statuses from strongest to weakest. Aggregation
// takes the weakest status over all commands (PROTOTYPE_SPEC.md §13.6).
var statusRank = map[ResolutionStatus]int{
	StatusResolved:      0,
	StatusDeclared:      1,
	StatusUnresolved:    2,
	StatusParseFailed:   3,
	StatusContextFailed: 3,
}

// WeakerStatus returns the weaker of two resolution statuses. PARSE_FAILED and
// CONTEXT_FAILED rank equally; the first argument wins a tie so an existing
// aggregate keeps its more specific cause.
func WeakerStatus(a, b ResolutionStatus) ResolutionStatus {
	if statusRank[b] > statusRank[a] {
		return b
	}
	return a
}

// Approvable reports whether an action with this status may be auto-allowed or
// turned into an approval (INVARIANT I-11).
func (s ResolutionStatus) Approvable() bool {
	return s == StatusResolved || s == StatusDeclared
}

// TargetStatus marks whether a target path could be determined exactly.
type TargetStatus string

const (
	TargetResolved  TargetStatus = "RESOLVED"
	TargetAmbiguous TargetStatus = "AMBIGUOUS"
)

// DecisionOutcome is the answer Intenter gives the agent. The domain form is
// upper case (PROTOTYPE_SPEC.md §13.7); the IPC protocol and the audit log use
// the lower-case wire form (contracts/ipc-protocol.md).
type DecisionOutcome string

const (
	OutcomeAllow DecisionOutcome = "ALLOW"
	OutcomeAsk   DecisionOutcome = "ASK"
	OutcomeBlock DecisionOutcome = "BLOCK"
)

// Wire returns the lower-case protocol and storage form ("allow", "ask", "block").
func (o DecisionOutcome) Wire() string { return strings.ToLower(string(o)) }

// ParseOutcome accepts either the domain or the wire form.
func ParseOutcome(s string) (DecisionOutcome, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case string(OutcomeAllow):
		return OutcomeAllow, true
	case string(OutcomeAsk):
		return OutcomeAsk, true
	case string(OutcomeBlock):
		return OutcomeBlock, true
	}
	return "", false
}

// MarshalJSON emits the wire form so IPC results match the protocol contract.
func (o DecisionOutcome) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.Wire())
}

// UnmarshalJSON accepts either form.
func (o *DecisionOutcome) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, ok := ParseOutcome(raw)
	if !ok {
		return fmt.Errorf("invalid decision outcome %q", raw)
	}
	*o = parsed
	return nil
}

// DecisionClass explains which branch of the evaluation order produced the
// outcome (PROTOTYPE_SPEC.md §18.5). The adapter maps (outcome, class) onto the
// agent's permission protocol.
type DecisionClass string

const (
	ClassPolicyReadonlyWorkspace DecisionClass = "POLICY_READONLY_WORKSPACE"
	ClassApprovalMatch           DecisionClass = "APPROVAL_MATCH"
	ClassRuleImport              DecisionClass = "RULE_IMPORT"

	ClassNoMatchingApproval         DecisionClass = "NO_MATCHING_APPROVAL"
	ClassApprovalMismatch           DecisionClass = "APPROVAL_MISMATCH"
	ClassPolicyRequiresConfirmation DecisionClass = "POLICY_REQUIRES_CONFIRMATION"
	ClassUnresolvedCommand          DecisionClass = "UNRESOLVED_COMMAND"
	ClassUnsupportedSyntax          DecisionClass = "UNSUPPORTED_SYNTAX"
	ClassAmbiguousPath              DecisionClass = "AMBIGUOUS_PATH"
	ClassContextUnavailable         DecisionClass = "CONTEXT_UNAVAILABLE"
	ClassAgentRuleConflict          DecisionClass = "AGENT_RULE_CONFLICT"
	ClassEngineError                DecisionClass = "ENGINE_ERROR"
	ClassHardRulePrefix             string        = "HARD_RULE_"
)

// HardRuleClass builds the decision class for a blocking hard rule, e.g. "R2"
// becomes HARD_RULE_R2.
func HardRuleClass(ruleID string) DecisionClass {
	return DecisionClass(ClassHardRulePrefix + ruleID)
}

// ApprovalKind distinguishes an approval of one exact resolved action from one
// of a semantic envelope (PROTOTYPE_SPEC.md §19.2).
type ApprovalKind string

const (
	ApprovalExact    ApprovalKind = "EXACT"
	ApprovalSemantic ApprovalKind = "SEMANTIC"
)

// ApprovalState is the lifecycle state of an approval. REVOKED is terminal;
// records are never deleted (INVARIANT I-15).
type ApprovalState string

const (
	ApprovalActive   ApprovalState = "ACTIVE"
	ApprovalDisabled ApprovalState = "DISABLED"
	ApprovalRevoked  ApprovalState = "REVOKED"
)

// ApprovalOrigin records which creation path produced an approval
// (PROTOTYPE_SPEC.md §19.1).
type ApprovalOrigin string

const (
	OriginClaudePrompt ApprovalOrigin = "claude_prompt"
	OriginClaudeRule   ApprovalOrigin = "claude_rule"
	OriginCLI          ApprovalOrigin = "cli"
)

// TargetFlag qualifies a target path (PROTOTYPE_SPEC.md §13.4).
type TargetFlag string

const (
	FlagWildcard      TargetFlag = "wildcard"
	FlagBroad         TargetFlag = "broad"
	FlagTraversal     TargetFlag = "traversal"
	FlagSymlinkEscape TargetFlag = "symlink_escape"
	FlagSensitive     TargetFlag = "sensitive"
	FlagToolCache     TargetFlag = "tool_cache"
	FlagNetworkPath   TargetFlag = "network_path"
	FlagTemp          TargetFlag = "temp"
)

// EffectFlag qualifies an effect (PROTOTYPE_SPEC.md §13.5). Flags are part of
// the approval envelope, so a new flag invalidates an approval (I-1).
type EffectFlag string

const (
	EffectFlagRecursive        EffectFlag = "recursive"
	EffectFlagForce            EffectFlag = "force"
	EffectFlagWildcard         EffectFlag = "wildcard"
	EffectFlagDiscardsChanges  EffectFlag = "discards_changes"
	EffectFlagElevated         EffectFlag = "elevated"
	EffectFlagInlineCredential EffectFlag = "inline_credential"
	EffectFlagInsecureTLS      EffectFlag = "insecure_tls"
	// EffectFlagDelete and EffectFlagBroad qualify a `git push` that removes a
	// ref or pushes many at once (§15.4, hard rule R7). Both are part of the
	// envelope, so an approval for a plain push never covers `--mirror` (I-1).
	EffectFlagDelete EffectFlag = "delete"
	EffectFlagBroad  EffectFlag = "broad"
)

// ProgramResolution says whether an executed program's behavior is declared by
// convention or entirely unknown (PROTOTYPE_SPEC.md §13.5).
type ProgramResolution string

const (
	ProgramDeclared   ProgramResolution = "DECLARED"
	ProgramUnresolved ProgramResolution = "UNRESOLVED"
)

// Dialect is the shell syntax a command is written in.
type Dialect string

const (
	DialectPosix      Dialect = "posix"
	DialectPowerShell Dialect = "powershell"
	DialectCmd        Dialect = "cmd"
)

// ContextStatus reports whether a usable workspace context was established
// (PROTOTYPE_SPEC.md §16.2).
type ContextStatus string

const (
	ContextOK                 ContextStatus = "OK"
	ContextWorkspaceUndefined ContextStatus = "WORKSPACE_UNDEFINED"
	ContextError              ContextStatus = "ERROR"
)

// PackageManagerKind identifies the JavaScript package manager of a workspace.
type PackageManagerKind string

const (
	PMNpm         PackageManagerKind = "npm"
	PMPnpm        PackageManagerKind = "pnpm"
	PMYarnClassic PackageManagerKind = "yarn-classic"
	PMYarnBerry   PackageManagerKind = "yarn-berry"
	PMUnknown     PackageManagerKind = "unknown"
)

// AdapterAction is what the adapter actually emitted to the agent after mapping
// a decision. It is recorded for audit only; the core never decides it.
type AdapterAction string

const (
	AdapterAllow  AdapterAction = "allow"
	AdapterDeny   AdapterAction = "deny"
	AdapterPrompt AdapterAction = "prompt"
	AdapterDefer  AdapterAction = "defer"
)

// ParseAdapterAction validates an adapter action arriving over IPC. The value
// is reported by the adapter rather than decided by the core, so it crosses a
// trust boundary and is checked instead of stored as written.
func ParseAdapterAction(s string) (AdapterAction, bool) {
	switch AdapterAction(strings.ToLower(strings.TrimSpace(s))) {
	case AdapterAllow:
		return AdapterAllow, true
	case AdapterDeny:
		return AdapterDeny, true
	case AdapterPrompt:
		return AdapterPrompt, true
	case AdapterDefer:
		return AdapterDefer, true
	}
	return "", false
}

// ExecutionStatus is the outcome the agent reported for an executed command.
type ExecutionStatus string

const (
	ExecutionCompleted ExecutionStatus = "completed"
	ExecutionFailed    ExecutionStatus = "failed"
	ExecutionUnknown   ExecutionStatus = "unknown"
)
