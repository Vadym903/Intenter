package approval

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/policy"
	"github.com/Vadym903/Intenter/internal/storage"
)

// MaxMismatchReports bounds how many related approvals are explained when
// nothing matched (§20.4).
const MaxMismatchReports = 5

// Matcher answers whether a stored approval covers an action. It implements
// policy.ApprovalMatcher.
type Matcher struct {
	store         *storage.Store
	engineVersion int
	// now is injectable so usage timestamps are deterministic in tests.
	now func() time.Time
}

// NewMatcher builds a matcher over a store.
func NewMatcher(store *storage.Store, engineVersion int) *Matcher {
	return &Matcher{store: store, engineVersion: engineVersion, now: time.Now}
}

// Match finds the first approval that covers the action, in the deterministic
// order of §20.1: EXACT before SEMANTIC, then ascending id.
//
// Matching is a pure function of (approval, resolved action, engine version):
// no scoring, no heuristics, no wall-clock dependence (INVARIANT I-6). The only
// writes are the usage counters recorded after a match is decided.
func (m *Matcher) Match(in policy.Input) (policy.MatchOutcome, error) {
	act := in.Action
	if act == nil || act.ProjectID == "" {
		return policy.MatchOutcome{}, nil
	}
	// §20.3 rule 7: only a fully modeled action can match at all.
	if !act.Status.Approvable() || act.HasAmbiguousTarget() {
		return policy.MatchOutcome{}, nil
	}

	ctx := context.Background()
	candidates, err := m.candidates(ctx, act.ProjectID)
	if err != nil {
		return policy.MatchOutcome{}, err
	}

	for i := range candidates {
		candidate := &candidates[i]
		if _, ok := Matches(candidate, act, m.engineVersion); !ok {
			continue
		}
		if err := m.recordUse(ctx, candidate.ID); err != nil {
			return policy.MatchOutcome{}, err
		}
		return policy.MatchOutcome{ApprovalID: candidate.ID, Matched: true}, nil
	}

	return policy.MatchOutcome{Mismatches: MismatchReports(candidates, act, m.engineVersion)}, nil
}

// Mismatches explains why the related approvals do not cover an action,
// without recording any usage. The policy engine calls it after a decision that
// never reached approval matching — a hard-rule block, or an action it could
// not model — so the audit event can still name the approval that stopped
// applying (§21).
func (m *Matcher) Mismatches(in policy.Input) ([]action.MismatchReport, error) {
	act := in.Action
	if act == nil || act.ProjectID == "" {
		return nil, nil
	}
	candidates, err := m.candidates(context.Background(), act.ProjectID)
	if err != nil {
		return nil, err
	}
	return MismatchReports(candidates, act, m.engineVersion), nil
}

// candidates returns every approval of a project — including DISABLED and
// REVOKED ones — in match order, with their fingerprint conditions loaded.
//
// Inactive approvals can never be selected by Matches (it checks Active()
// itself), but they must still be fetched here: §21 requires a mismatch
// report to say an approval "is disabled"/"is revoked" when that is why it no
// longer applies (mismatch.go Differences), and MismatchReports can only see
// that state on an approval it was actually given (AG-160).
func (m *Matcher) candidates(ctx context.Context, projectID string) ([]action.Approval, error) {
	approvals, err := m.store.Approvals.List(ctx, storage.ApprovalFilter{ProjectID: projectID, IncludeInactive: true})
	if err != nil {
		return nil, fmt.Errorf("approval: list: %w", err)
	}
	if len(approvals) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(approvals))
	for i := range approvals {
		ids = append(ids, approvals[i].ID)
	}
	conditions, err := m.store.Conditions.ListByApprovals(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("approval: conditions: %w", err)
	}
	for i := range approvals {
		approvals[i].Conditions = conditions[approvals[i].ID]
	}

	// §20.1: EXACT before SEMANTIC, then ascending id.
	sort.SliceStable(approvals, func(i, j int) bool {
		left, right := &approvals[i], &approvals[j]
		if (left.Kind == action.ApprovalExact) != (right.Kind == action.ApprovalExact) {
			return left.Kind == action.ApprovalExact
		}
		return left.ID < right.ID
	})
	return approvals, nil
}

// recordUse updates the counters and appends the `matched` event (§19.4).
func (m *Matcher) recordUse(ctx context.Context, approvalID int64) error {
	at := m.now()
	if err := m.store.Approvals.RecordUse(ctx, approvalID, at); err != nil {
		return fmt.Errorf("approval: record use: %w", err)
	}
	_, err := m.store.ApprovalEvents.Insert(ctx, action.ApprovalEvent{
		ApprovalID: approvalID,
		Type:       action.ApprovalEventMatched,
		At:         at,
	})
	if err != nil {
		return fmt.Errorf("approval: record match event: %w", err)
	}
	return nil
}

// Matches applies the field-wise rules of §20.3. It returns the first rule that
// failed, which is what the mismatch report is built from.
//
// INVARIANT I-1: an approval never matches an action whose resolved effects
// exceed what it permitted — a broader scope, a new flag, a new network target
// or a changed fingerprint all fail here.
func Matches(approval *action.Approval, act *action.ResolvedAction, engineVersion int) (string, bool) {
	// Rule 1: the engine that created it still interprets envelopes the same way.
	if !EngineCompatible(approval.EngineVersion, engineVersion) {
		return "engine", false
	}
	if !approval.Active() || approval.ProjectID != act.ProjectID {
		return "candidate", false
	}

	// Rule 2: the same operations, in the same order.
	if !opsEqual(approval.SemanticOps, act.SemanticOps) {
		return "semantic_ops", false
	}

	// Rule 3: every fingerprint the approval was granted under still holds. A
	// key the action no longer produces is a mismatch, so an approval created
	// through a wrapper never covers the direct command.
	current := act.FingerprintMap()
	for key, value := range approval.Fingerprints() {
		stored, present := current[key]
		if !present || stored != value {
			return "fingerprint:" + key, false
		}
	}

	semantic := approval.Kind == action.ApprovalSemantic

	// Rule 4: effects, with flags as part of the identity.
	actionEnvelope := envelopeKeys(act)
	if semantic {
		if !subsetOf(actionEnvelope, approval.EnvelopeKeys()) {
			return "effects", false
		}
	} else if !equalSets(actionEnvelope, approval.EnvelopeKeys()) {
		return "effects", false
	}

	// Rule 5: network targets.
	actionNetwork := networkKeys(act)
	if semantic {
		if !subsetOf(actionNetwork, approval.NetworkKeys()) {
			return "network", false
		}
	} else if !equalSets(actionNetwork, approval.NetworkKeys()) {
		return "network", false
	}

	// Rule 6: EXACT approvals pin the target set; SEMANTIC ones are bounded by
	// the scopes their envelope already carries.
	if !semantic && !equalSets(act.DisplayTargets(), approval.Targets) {
		return "targets", false
	}
	return "", true
}
