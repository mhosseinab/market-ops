package approval

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Binding is the exact set of versions an approval control is bound to (APR-001,
// §8.1 "Cards bind the resolved entity, account, context version, and
// recommendation version at creation"). It is the never-cut approval-versioning
// invariant made concrete: a control is valid ONLY while every one of these
// dimensions is unchanged and the expiry has not passed.
//
// All fields are plain comparable values (no float, no arithmetic). Version
// numbers are assigned by the append-only store; this type only compares them.
type Binding struct {
	// ActionID is the stable identity of the action the control authorizes. It is
	// also the anchor of the idempotency key (EXE-002 seam): the same action ID +
	// parameter version always produce the same key.
	ActionID uuid.UUID
	// ParameterVersion is the version of the action's parameters (price, quantity).
	// A card PRICE EDIT mints a NEW parameter version (CHAT-044), so the stale
	// control's ParameterVersion no longer matches and it is rejected.
	ParameterVersion int64
	// ContextVersion is the conversation/account context version at card creation
	// (§8.1). A context change invalidates the control.
	ContextVersion int64
	// PolicyVersion is the policy-evaluation result version the recommendation was
	// built from (APR-001 "policy … versions"). A policy change invalidates.
	PolicyVersion int64
	// CostProfileVersion is the in-force cost-profile version (CST-002) the
	// contribution was computed over. A cost change invalidates; it also lets a
	// historical control reproduce the exact cost profile that produced its number.
	CostProfileVersion int64
	// EvidenceVersions binds each cited observation to the evidence version used.
	// ANY added, removed, or changed evidence version invalidates the control
	// (§16 "Boundary/cost/evidence changes after card → invalidate; recalculate").
	EvidenceVersions map[uuid.UUID]int64
	// Expiry is the instant the control lapses (APR-001 expiry). At or after this
	// instant the control is expired and can no longer approve.
	Expiry time.Time
}

// InvalidationReason is a stable, machine-readable reason a bound control is no
// longer valid. It names the exact dimension that changed (APR-001 per-dimension
// invalidation) so the surface can explain precisely what to recalculate.
type InvalidationReason string

const (
	// ReasonNone — the control is still valid (no dimension changed, not expired).
	ReasonNone InvalidationReason = ""
	// ReasonActionMismatch — the action ID itself differs (a different action).
	ReasonActionMismatch InvalidationReason = "action_mismatch"
	// ReasonParameterChanged — the parameter version changed (e.g. price edit,
	// CHAT-044).
	ReasonParameterChanged InvalidationReason = "parameter_version_changed"
	// ReasonContextChanged — the context version changed (§8.1).
	ReasonContextChanged InvalidationReason = "context_version_changed"
	// ReasonPolicyChanged — the policy version changed.
	ReasonPolicyChanged InvalidationReason = "policy_version_changed"
	// ReasonCostChanged — the cost-profile version changed (§16 cost change).
	ReasonCostChanged InvalidationReason = "cost_version_changed"
	// ReasonEvidenceChanged — an evidence version changed/added/removed (§16).
	ReasonEvidenceChanged InvalidationReason = "evidence_version_changed"
	// ReasonExpired — the expiry was reached (APR-001 expiry / §8.4 Expired).
	ReasonExpired InvalidationReason = "expired"
)

// IdempotencyKey is the stable execution-handoff key (EXE-002 seam). It is a
// deterministic function of the action ID and the parameter version, so a retry
// of the SAME approved parameters carries the SAME key (one execution record),
// while a price edit (new parameter version) yields a DIFFERENT key — a new
// action, never a duplicate write. Built with fmt (no operator on this path).
func (b Binding) IdempotencyKey() string {
	return fmt.Sprintf("action:%s:pv:%d", b.ActionID.String(), b.ParameterVersion)
}

// ValidateAgainst checks this bound control against the CURRENT versions and the
// evaluation instant. It returns the first changed dimension (in a fixed,
// deterministic order) or ReasonNone when every bound dimension still matches and
// the control has not expired. ANY difference invalidates — this is the APR-001
// never-cut invariant, tested per dimension.
//
// Expiry is checked FIRST: an expired control is invalid regardless of version
// state (a lapsed card cannot approve even if nothing else changed).
func (b Binding) ValidateAgainst(current Binding, now time.Time) InvalidationReason {
	if b.expired(now) {
		return ReasonExpired
	}
	if b.ActionID != current.ActionID {
		return ReasonActionMismatch
	}
	if b.ParameterVersion != current.ParameterVersion {
		return ReasonParameterChanged
	}
	if b.ContextVersion != current.ContextVersion {
		return ReasonContextChanged
	}
	if b.PolicyVersion != current.PolicyVersion {
		return ReasonPolicyChanged
	}
	if b.CostProfileVersion != current.CostProfileVersion {
		return ReasonCostChanged
	}
	if evidenceChanged(b.EvidenceVersions, current.EvidenceVersions) {
		return ReasonEvidenceChanged
	}
	return ReasonNone
}

// Valid reports whether the control still matches current and is unexpired.
func (b Binding) Valid(current Binding, now time.Time) bool {
	return b.ValidateAgainst(current, now) == ReasonNone
}

// expired reports whether now is at or after the expiry instant. A control is
// live strictly BEFORE its expiry; at the instant it lapses it is expired.
func (b Binding) expired(now time.Time) bool {
	return !now.Before(b.Expiry)
}

// evidenceChanged reports whether the two evidence-version maps differ in any
// key or value (added, removed, or changed). A nil and an empty map are treated
// as equal (no evidence bound either way).
func evidenceChanged(bound, current map[uuid.UUID]int64) bool {
	if len(bound) != len(current) {
		return true
	}
	for id, v := range bound {
		cv, ok := current[id]
		if !ok || cv != v {
			return true
		}
	}
	return false
}

// cloneEvidence returns a defensive copy of an evidence-version map so a Binding
// held by a card cannot be mutated by a caller after creation.
func cloneEvidence(src map[uuid.UUID]int64) map[uuid.UUID]int64 {
	if len(src) == 0 {
		return nil
	}
	out := make(map[uuid.UUID]int64, len(src))
	for id, v := range src {
		out[id] = v
	}
	return out
}
