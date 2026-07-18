// Package execution owns the §7.5 execution plane of the deterministic core: the
// EXE-001 pre-write revalidation gate matrix, the EXE-002 idempotent write with a
// single execution record, the EXE-003 external result states (including the
// fail-closed Pending Reconciliation), the EXE-005 recommend-only mode, and the
// wiring that keeps execution OFF by default (a write requires a Supported
// price_write capability AND the S35 region write-verification flag).
//
// Two never-cut invariants (PRD §4.6) are enforced here:
//
//   - Approval versioning (APR-001) / EXE-001: confirmation triggers revalidation
//     of nine gates — identity, current price, costs, money unit, boundary,
//     evidence/JIT, guardrails, permission, and expiry. An injected change in ANY
//     gate prevents the write. The version dimensions are re-resolved SERVER-SIDE
//     (never trusted from a client-echoed request body) and compared against the
//     card's bound versions; the external gates are resolved from authoritative
//     state.
//   - Idempotency (EXE-002): every write is keyed by the card's stable idempotency
//     key and produces exactly one execution record. A duplicate request finds the
//     existing record and performs ZERO duplicate external writes.
//
// The gate matrix in this file is PURE (no DB, no clock beyond the passed
// instant) so the 9-gate × injected-change property is provable in isolation.
package execution

import (
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
)

// Gate is one of the nine EXE-001 revalidation gates. The set is closed and in the
// fixed §7.5 order; an unrecognised gate authorizes nothing.
type Gate string

const (
	// GateIdentity — the resolved market-product identity is still Confirmed and
	// the bound action id is unchanged (a different action id is a different
	// identity, never the same write).
	GateIdentity Gate = "identity"
	// GateCurrentPrice — the current price the card was built against is unchanged
	// (the bound parameter version still matches and the live price still equals
	// the card's baseline).
	GateCurrentPrice Gate = "current_price"
	// GateCosts — the in-force cost-profile version is unchanged (CST-002).
	GateCosts Gate = "costs"
	// GateMoneyUnit — the source money unit is unambiguous (§16 quarantine).
	GateMoneyUnit Gate = "money_unit"
	// GateBoundary — the marketplace price boundary is still known.
	GateBoundary Gate = "boundary"
	// GateEvidence — every cited evidence version is unchanged AND a JIT refresh is
	// fresh within OBS-009 (≤10min, in budget, within tolerance).
	GateEvidence Gate = "evidence_jit"
	// GateGuardrails — the policy-evaluation version (floor/cap/cooldown/strategy)
	// and the context version are unchanged.
	GateGuardrails Gate = "guardrails"
	// GatePermission — the actor still holds the L4 execute permission.
	GatePermission Gate = "permission"
	// GateExpiry — the control has not lapsed (APR-001 expiry).
	GateExpiry Gate = "expiry"
)

// AllGates is the nine EXE-001 gates in fixed evaluation order.
var AllGates = []Gate{
	GateIdentity, GateCurrentPrice, GateCosts, GateMoneyUnit, GateBoundary,
	GateEvidence, GateGuardrails, GatePermission, GateExpiry,
}

// RevalidationInputs is the fully-resolved input to the EXE-001 gate matrix. The
// version dimensions live in Bound (the card's frozen binding) and Current (the
// SERVER-resolved live binding). The remaining fields are authoritative external
// signals the service resolves at revalidation time; NONE of them may be sourced
// from a client-echoed request body.
type RevalidationInputs struct {
	// Bound is the card's frozen APR-001 binding.
	Bound approval.Binding
	// Current is the binding re-resolved SERVER-SIDE from the authoritative store
	// at instant Now. It is compared against Bound; any divergence blocks the write.
	Current approval.Binding
	Now     time.Time

	// IdentityConfirmed — the variant's market-product identity is Confirmed.
	IdentityConfirmed bool
	// CurrentPriceMatches — the live current price still equals the card baseline.
	CurrentPriceMatches bool
	// MoneyUnitAmbiguous — the source money unit is ambiguous (blocks; §16).
	MoneyUnitAmbiguous bool
	// BoundaryKnown — the marketplace price boundary is known.
	BoundaryKnown bool
	// PermissionGranted — the actor holds the L4 execute permission.
	PermissionGranted bool
	// JITFresh — a JIT evidence refresh satisfies OBS-009 (≤10min, in budget,
	// within tolerance). False blocks (stale/over-budget/out-of-tolerance).
	JITFresh bool
}

// GateOutcome is the result of evaluating the EXE-001 matrix: OK true means every
// gate passed and the write may proceed; otherwise Failed names the FIRST gate
// that blocked (in fixed order) and Reason carries the version dimension when the
// block came from the binding comparison (ReasonNone for a purely external gate).
type GateOutcome struct {
	OK     bool
	Failed Gate
	Reason approval.InvalidationReason
}

// EvaluateGates runs the nine EXE-001 gates in fixed order and returns the first
// blocking gate, or OK. It is pure: it performs no I/O and reads no clock beyond
// in.Now. Every binding dimension maps onto exactly one gate, so the matrix is
// exhaustive — an injected change in ANY gate prevents the write.
func EvaluateGates(in RevalidationInputs) GateOutcome {
	// 1. Identity: confirmed, and the same action.
	if !in.IdentityConfirmed {
		return GateOutcome{Failed: GateIdentity}
	}
	if in.Bound.ActionID != in.Current.ActionID {
		return GateOutcome{Failed: GateIdentity, Reason: approval.ReasonActionMismatch}
	}
	// 2. Current price: bound parameter version and live baseline both unchanged.
	if in.Bound.ParameterVersion != in.Current.ParameterVersion {
		return GateOutcome{Failed: GateCurrentPrice, Reason: approval.ReasonParameterChanged}
	}
	if !in.CurrentPriceMatches {
		return GateOutcome{Failed: GateCurrentPrice}
	}
	// 3. Costs: cost-profile version unchanged (CST-002).
	if in.Bound.CostProfileVersion != in.Current.CostProfileVersion {
		return GateOutcome{Failed: GateCosts, Reason: approval.ReasonCostChanged}
	}
	// 4. Money unit: unambiguous.
	if in.MoneyUnitAmbiguous {
		return GateOutcome{Failed: GateMoneyUnit}
	}
	// 5. Boundary: known.
	if !in.BoundaryKnown {
		return GateOutcome{Failed: GateBoundary}
	}
	// 6. Evidence / JIT: cited evidence unchanged AND a fresh JIT refresh.
	if evidenceChanged(in.Bound.EvidenceVersions, in.Current.EvidenceVersions) {
		return GateOutcome{Failed: GateEvidence, Reason: approval.ReasonEvidenceChanged}
	}
	if !in.JITFresh {
		return GateOutcome{Failed: GateEvidence}
	}
	// 7. Guardrails: policy version and context version unchanged.
	if in.Bound.PolicyVersion != in.Current.PolicyVersion {
		return GateOutcome{Failed: GateGuardrails, Reason: approval.ReasonPolicyChanged}
	}
	if in.Bound.ContextVersion != in.Current.ContextVersion {
		return GateOutcome{Failed: GateGuardrails, Reason: approval.ReasonContextChanged}
	}
	// 8. Permission: L4 execute still granted.
	if !in.PermissionGranted {
		return GateOutcome{Failed: GatePermission}
	}
	// 9. Expiry: control not lapsed.
	if !in.Now.Before(in.Bound.Expiry) {
		return GateOutcome{Failed: GateExpiry, Reason: approval.ReasonExpired}
	}
	return GateOutcome{OK: true}
}

// evidenceChanged reports whether two evidence-version maps differ in any key or
// value (added, removed, or changed). A nil and an empty map are equal.
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
