package approval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/policy"
	"github.com/Vadym903/Intenter/internal/storage"
)

// Importer converts persistent consent the agent already holds into a validated
// approval, at most once per (project, rule, raw command) (§19.5). It
// implements policy.ConsentImporter.
//
// This is the one place a rule the user wrote for their agent becomes trust in
// Intenter, and it is a conversion, not an allowlist: the command is fully
// resolved and passed through the hard rules first, and the result is an
// ordinary approval that stops matching when behavior changes (INVARIANT I-5).
type Importer struct {
	store         *storage.Store
	creator       *Creator
	engineVersion int
	now           func() time.Time
}

// NewImporter builds an importer over a store.
func NewImporter(store *storage.Store, engineVersion int) *Importer {
	return &Importer{
		store:         store,
		creator:       NewCreator(store),
		engineVersion: engineVersion,
		now:           time.Now,
	}
}

// Import applies the preconditions of §19.5 and, when they all hold, creates
// one approval and records one `agent_rule_imports` row per consenting rule.
//
// A rule that was already imported for this project and raw command is never
// imported again, even if the approval it produced was later invalidated,
// disabled or revoked. That is what makes changed behavior ask again instead of
// silently re-importing the agent's still-present string rule.
func (i *Importer) Import(in policy.Input, consent *action.AgentConsent) (policy.MatchOutcome, error) {
	if !consent.Usable() {
		return policy.MatchOutcome{}, nil
	}
	act := in.Action
	if act == nil || act.ProjectID == "" {
		return policy.MatchOutcome{}, nil
	}

	agent := agentName(in)
	ctx := context.Background()

	ruleKeys := normalizedRuleKeys(consent.RuleKeys)
	imported, err := i.store.Imports.AnyExists(ctx, act.ProjectID, agent, ruleKeys, act.RawCommand)
	if err != nil {
		return policy.MatchOutcome{}, fmt.Errorf("approval: import lookup: %w", err)
	}
	if imported {
		// Already converted once. Whatever happened to that approval since is
		// the user's decision to revisit, not something to redo silently.
		return policy.MatchOutcome{}, nil
	}

	request := CreateRequest{
		Action:        act,
		Policy:        in,
		Kind:          action.ApprovalExact,
		Origin:        action.OriginClaudeRule,
		OriginRef:     strings.Join(ruleKeys, ","),
		Agent:         agent,
		EngineVersion: i.engineVersion,
		Now:           i.now(),
	}
	if err := Validate(request); err != nil {
		// A rule the user wrote does not make an action approvable; refusing
		// here is what keeps consent import from becoming a string allowlist.
		return policy.MatchOutcome{}, nil
	}

	created, err := i.creator.Create(ctx, request)
	if err != nil {
		return policy.MatchOutcome{}, err
	}

	for _, ruleKey := range ruleKeys {
		_, err := i.store.Imports.InsertOnce(ctx, action.RuleImport{
			ProjectID:  act.ProjectID,
			Agent:      agent,
			RuleKey:    ruleKey,
			RawCommand: act.RawCommand,
			ApprovalID: created.ID,
			ImportedAt: request.Now,
		})
		if err != nil {
			return policy.MatchOutcome{}, fmt.Errorf("approval: record import: %w", err)
		}
	}

	return policy.MatchOutcome{ApprovalID: created.ID, Matched: true}, nil
}

// ImportForExecution is path (b) of §19.5: the user answered "yes, and don't
// ask again" in the agent's own dialog after Intenter deferred, so the
// consent arrives with the execution report rather than with the request.
//
// It is only honored for an event Intenter actually deferred without a
// matching approval; anything else means the consent does not describe what was
// evaluated.
func (i *Importer) ImportForExecution(in policy.Input, consent *action.AgentConsent, decision action.Decision) (policy.MatchOutcome, error) {
	if decision.Outcome != action.OutcomeAsk || decision.Class != action.ClassNoMatchingApproval {
		return policy.MatchOutcome{}, nil
	}
	return i.Import(in, consent)
}

// agentName reports which agent the consent came from, defaulting to the only
// adapter the prototype ships.
func agentName(in policy.Input) string {
	if in.Agent != "" {
		return in.Agent
	}
	return "claude"
}

// normalizedRuleKeys sorts and deduplicates the consenting rules so the stored
// origin_ref is stable regardless of the order the adapter reported them in.
func normalizedRuleKeys(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
