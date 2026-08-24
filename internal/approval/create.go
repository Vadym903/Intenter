package approval

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/policy"
	"github.com/Vadym903/Intenter/internal/storage"
)

// ErrNotApprovable marks a source action that may never become an approval.
var ErrNotApprovable = errors.New("approval: the action cannot be approved")

// CreateRequest is everything needed to turn one evaluated action into a stored
// approval (§19.3).
type CreateRequest struct {
	// Action is the resolved action, exactly as it was evaluated.
	Action *action.ResolvedAction
	// Policy is the input the hard rules were evaluated with, so creation can
	// refuse an action the safety floor would not let through.
	Policy policy.Input
	Kind   action.ApprovalKind
	Origin action.ApprovalOrigin
	// OriginRef records the rule key or event the approval came from.
	OriginRef string
	// SourceEventID is the audit event the approval was created from.
	SourceEventID *int64
	Agent         string
	Note          string
	EngineVersion int
	// Now is injectable so stored timestamps are deterministic in tests.
	Now time.Time
}

// Creator writes approvals to the store.
type Creator struct {
	store *storage.Store
}

// NewCreator builds a creator over a store.
func NewCreator(store *storage.Store) *Creator { return &Creator{store: store} }

// Validate applies the creation preconditions of §19.3 and INVARIANT I-11: an
// action Intenter could not fully model, or that the safety floor stops, is
// never approvable — not even by an explicit CLI request.
func Validate(request CreateRequest) error {
	act := request.Action
	if act == nil {
		return fmt.Errorf("%w: there is no resolved action to approve", ErrNotApprovable)
	}
	if !act.Status.Approvable() {
		return fmt.Errorf("%w: its status is %s (%s)", ErrNotApprovable, act.Status, act.StatusReason)
	}
	if act.HasAmbiguousTarget() {
		return fmt.Errorf("%w: one of its targets depends on a variable Intenter cannot expand", ErrNotApprovable)
	}
	if act.ProjectID == "" {
		return fmt.Errorf("%w: it is not associated with a project", ErrNotApprovable)
	}
	if len(act.Effects) == 0 {
		return fmt.Errorf("%w: it has no effects to approve", ErrNotApprovable)
	}

	findings := policy.HardRules(request.Policy)
	if outcome := findings.Outcome(); outcome != policy.OutcomePass {
		strongest, _ := findings.Strongest()
		return fmt.Errorf("%w: safety rule %s always requires confirmation (%s)",
			ErrNotApprovable, strongest.Rule, strongest.Reason)
	}
	return nil
}

// Build turns a validated request into the approval record that would be
// stored. It is separate from Create so callers can show what would be
// remembered before writing it.
func Build(request CreateRequest) (*action.Approval, error) {
	if err := Validate(request); err != nil {
		return nil, err
	}

	kind := request.Kind
	if kind == "" {
		kind = action.ApprovalExact
	}
	createdAt := request.Now
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	act := request.Action
	approval := &action.Approval{
		ProjectID:             act.ProjectID,
		Kind:                  kind,
		SemanticOps:           sortedOps(act.SemanticOps),
		Envelope:              act.Envelope(),
		Network:               act.Network(),
		Conditions:            conditionsFor(act.Fingerprints),
		EngineVersion:         request.EngineVersion,
		Origin:                request.Origin,
		OriginRef:             request.OriginRef,
		CreatedFromEventID:    request.SourceEventID,
		CreatedFromRawCommand: act.RawCommand,
		CreatedByAgent:        request.Agent,
		State:                 action.ApprovalActive,
		Note:                  request.Note,
		CreatedAt:             createdAt,
	}

	// Only an EXACT approval pins the target set; a SEMANTIC one is bounded by
	// the scopes its envelope carries (§19.2).
	if kind == action.ApprovalExact {
		approval.Targets = act.DisplayTargets()
	}
	return approval, nil
}

// Create validates, builds and stores an approval. The store records the
// `created` approval event in the same transaction (I-15, §19.4).
func (c *Creator) Create(ctx context.Context, request CreateRequest) (*action.Approval, error) {
	approval, err := Build(request)
	if err != nil {
		return nil, err
	}
	id, err := c.store.Approvals.Insert(ctx, approval)
	if err != nil {
		return nil, fmt.Errorf("approval: store: %w", err)
	}
	approval.ID = id
	return approval, nil
}
