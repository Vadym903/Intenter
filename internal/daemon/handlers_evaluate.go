package daemon

import (
	"context"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/audit"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/policy"
)

// CacheTTL is how long an evaluation is reused for the same tool call or the
// same command in one session. Claude fires several hooks for one command, and
// they must all get the same answer even if the workspace changes in between.
const CacheTTL = 60 * time.Second

// handleEvaluate answers the `evaluate` method (§10.4). It never returns a
// protocol error for an action it cannot model: an action Intenter does not
// understand is a decision (ASK), not a failure.
func (d *Daemon) handleEvaluate(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.EvaluateParams](req)
	if err != nil {
		return nil, err
	}

	request := params.Request
	if len(request.RawCommand) > action.MaxRawCommandBytes {
		// Rejecting an over-long command would hand it back to the agent's
		// native flow with the safety floor skipped — an agent could evade the
		// gate by padding a command past the limit. Instead the command is
		// truncated to what Intenter evaluates and marked, so the decision is
		// at most ASK (R13) rather than a bad request that defers.
		request.RawCommand = safeTruncate(request.RawCommand, action.MaxRawCommandBytes)
		request.RawCommandTruncated = true
	}
	if request.ReceivedAt.IsZero() {
		request.ReceivedAt = time.Now()
	}

	// A dry run is the setup self-test: never cached, never recorded.
	if params.DryRun {
		return d.evaluate(ctxOrBackground(ctx), request, true), nil
	}

	if cached, ok := d.cache.get(request); ok {
		return cached, nil
	}
	result := d.evaluate(ctxOrBackground(ctx), request, false)
	d.cache.put(request, result)
	return result, nil
}

// safeTruncate cuts a string to a byte budget without splitting a UTF-8 rune.
func safeTruncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// evaluate runs the whole pipeline for one request: resolve, decide, explain,
// record.
func (d *Daemon) evaluate(ctx context.Context, request action.ActionRequest, dryRun bool) action.EvaluationResult {
	resolved, resolveContext := d.resolver.Resolve(request)
	if request.RawCommandTruncated {
		resolved.MarkIncomplete("the command was longer than Intenter evaluates and was examined only in part")
	}

	// The project row has to exist before the engine runs: a consent import at
	// step 6 inserts an approval that references it.
	if !dryRun {
		d.rememberProject(ctx, resolveContext.Action)
	}

	in := policy.Input{
		Action:  resolved,
		Context: resolveContext.Action,
		Config:  d.config,
		Rules:   d.platform.PathRules(),
		Agent:   request.Agent,
	}

	decision, findings := d.engine.EvaluateDetailed(in, request.AgentConsent)
	explanation := policy.Explain(in, decision, findings)

	eventID, err := d.auditor.RecordEvaluation(ctx, audit.Evaluation{
		Request:     request,
		Context:     resolveContext.Action,
		Resolved:    resolved,
		Decision:    decision,
		Explanation: explanation,
		DryRun:      dryRun,
	})
	if err != nil {
		// §24: a decision that could not be recorded is not one we act on.
		// Downgrading to ASK keeps the agent's native flow in charge (I-3).
		d.logger.Error("daemon: could not record the audit event", "error", err)
		decision = action.Decision{
			Outcome:       action.OutcomeAsk,
			Class:         action.ClassEngineError,
			Reason:        "the decision could not be recorded: " + err.Error(),
			EngineVersion: decision.EngineVersion,
		}
		explanation = append(explanation, "the audit event could not be written, so the decision was downgraded to ask")
	}

	return action.EvaluationResult{
		AuditEventID:     eventID,
		Decision:         decision.Outcome,
		Class:            decision.Class,
		Reason:           decision.Reason,
		ApprovalID:       decision.ApprovalID,
		HardRule:         decision.HardRule,
		MismatchReports:  decision.MismatchReports,
		ResolutionStatus: resolved.Status,
		Explanation:      explanation,
		UserMessage:      policy.UserMessage(decision),
	}
}

// rememberProject records the workspace so the CLI can show a project root next
// to its approvals. A failure here is not worth failing an evaluation over.
func (d *Daemon) rememberProject(ctx context.Context, actionContext *action.Context) {
	if actionContext == nil || actionContext.ProjectID == "" || actionContext.WorkspaceRoot == "" {
		return
	}
	remote := ""
	if actionContext.Git != nil {
		remote = actionContext.Git.RemoteURLs["origin"]
	}
	err := d.store.Projects.Upsert(ctx, action.Project{
		ID:        actionContext.ProjectID,
		RootPath:  actionContext.WorkspaceRoot,
		RemoteURL: remote,
	})
	if err != nil {
		d.logger.Warn("daemon: could not record the project", "error", err)
	}
}

// resultCache remembers recent evaluations so the several hooks an agent fires
// for one command receive one consistent answer.
type resultCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
	now     func() time.Time
}

type cacheEntry struct {
	result  action.EvaluationResult
	expires time.Time
}

func newResultCache(ttl time.Duration) *resultCache {
	return &resultCache{entries: make(map[string]cacheEntry), ttl: ttl, now: time.Now}
}

// get returns the cached evaluation this request may reuse.
//
// A request carrying a tool_use_id is a distinct tool invocation, so it is only
// ever answered from its own entry: re-running the same command must resolve
// again, because the script behind it may have been rewritten in between. Only
// a request without one — Claude's PermissionRequest, which carries no
// tool_use_id — falls back to the (session, command) entry, which is exactly
// the correlation §11.4 asks for.
func (c *resultCache) get(request action.ActionRequest) (action.EvaluationResult, bool) {
	if c == nil {
		return action.EvaluationResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key := toolUseKey(request)
	if key == "" {
		key = sessionCommandKey(request)
	}
	if key == "" {
		return action.EvaluationResult{}, false
	}

	entry, ok := c.entries[key]
	if !ok {
		return action.EvaluationResult{}, false
	}
	if c.now().After(entry.expires) {
		delete(c.entries, key)
		return action.EvaluationResult{}, false
	}
	return entry.result, true
}

// put stores an evaluation under every key it can later be found by.
func (c *resultCache) put(request action.ActionRequest, result action.EvaluationResult) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.evictExpired(now)

	entry := cacheEntry{result: result, expires: now.Add(c.ttl)}
	for _, key := range cacheKeys(request) {
		c.entries[key] = entry
	}
}

// reset drops every cached evaluation. Anything that changes what a decision
// would be — new, disabled or revoked trust, edited agent settings — must not
// be answered from a cache that predates the change.
func (c *resultCache) reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry)
}

// evictExpired keeps the map from growing without bound in a long session.
func (c *resultCache) evictExpired(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, key)
		}
	}
}

// cacheKeys are the identities one evaluation is stored under (§10.4). Both are
// written; which one may be read is decided by get.
func cacheKeys(request action.ActionRequest) []string {
	keys := make([]string, 0, 2)
	if key := toolUseKey(request); key != "" {
		keys = append(keys, key)
	}
	if key := sessionCommandKey(request); key != "" {
		keys = append(keys, key)
	}
	return keys
}

func toolUseKey(request action.ActionRequest) string {
	if request.ToolUseID == "" {
		return ""
	}
	return "tool\x00" + request.ToolUseID
}

func sessionCommandKey(request action.ActionRequest) string {
	if request.SessionID == "" || request.RawCommand == "" {
		return ""
	}
	return "session\x00" + request.SessionID + "\x00" + request.Cwd + "\x00" + request.RawCommand
}
