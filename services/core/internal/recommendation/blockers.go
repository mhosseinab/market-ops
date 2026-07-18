package recommendation

import (
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// BlockerCode is a stable, machine-readable PRC-002 blocker reason. The seven
// codes are exactly the PRD §7.5 PRC-002 / §16 blockers. A message may localize
// at the edge; the CODE is authoritative, the message carries no authority (§8).
type BlockerCode string

const (
	// BlockerUnconfirmedIdentity — the variant has no Confirmed Market Product
	// Identity (CAT-002); a Needs-Review/Rejected/Obsolete mapping is not
	// executable.
	BlockerUnconfirmedIdentity BlockerCode = "unconfirmed_identity"
	// BlockerIncompleteCost — margin readiness is not Complete (CST-003): Missing,
	// Stale, or Partial cost blocks an executable recommendation.
	BlockerIncompleteCost BlockerCode = "incomplete_cost"
	// BlockerAmbiguousMoneyUnit — a source money unit is ambiguous and was
	// quarantined (§9.1, §16): no inference, so no executable price.
	BlockerAmbiguousMoneyUnit BlockerCode = "ambiguous_money_unit"
	// BlockerUnusableEvidence — the cited evidence quality is not usable
	// (conflicted/stale/unavailable, §10.3): the number cannot be grounded.
	BlockerUnusableEvidence BlockerCode = "unusable_evidence"
	// BlockerUnknownBoundary — the marketplace price boundary is unknown (§9.2,
	// §16): no executable price exists.
	BlockerUnknownBoundary BlockerCode = "unknown_boundary"
	// BlockerPermissionFailure — the acting principal lacks approval permission
	// (ACC-002/§8.3): no control may be exposed to it.
	BlockerPermissionFailure BlockerCode = "permission_failure"
	// BlockerPolicyConflict — the six-stage policy engine produced a hard blocker
	// (§9.3): a conflict with boundary/floor/cap/cooldown prevents a proposal.
	BlockerPolicyConflict BlockerCode = "policy_conflict"
)

// blockerOrder is the fixed, deterministic surfacing order (PRC-002 / journey 10
// "blockers in policy order"). Identity and cost precede money, evidence,
// boundary, permission, then policy conflicts.
var blockerOrder = []BlockerCode{
	BlockerUnconfirmedIdentity,
	BlockerIncompleteCost,
	BlockerAmbiguousMoneyUnit,
	BlockerUnusableEvidence,
	BlockerUnknownBoundary,
	BlockerPermissionFailure,
	BlockerPolicyConflict,
}

// Blocker is one typed PRC-002 reason no approval control exists. It never
// carries authority; it is the explicit "unavailable with reason" for the whole
// executable path.
type Blocker struct {
	Code    BlockerCode
	Message string
}

// EvidenceUsable reports whether an evidence quality state may ground an
// executable number. Only Verified and Supported are usable (§10.3); Unverified,
// Conflicted, Stale, and Unavailable are not.
func EvidenceUsable(quality string) bool {
	switch quality {
	case "verified", "supported":
		return true
	default:
		return false
	}
}

// detectBlockers assembles the PRC-002 blockers for the inputs, in the fixed
// surfacing order. A missing/empty set means the executable path is open (subject
// to the readiness + policy-proposal gate in Approvable).
func detectBlockers(in AssembleInput) []Blocker {
	var out []Blocker
	add := func(code BlockerCode, msg string) {
		out = append(out, Blocker{Code: code, Message: msg})
	}

	for _, code := range blockerOrder {
		switch code {
		case BlockerUnconfirmedIdentity:
			if !in.IdentityConfirmed {
				add(code, "variant has no confirmed market product identity")
			}
		case BlockerIncompleteCost:
			if in.Readiness != cost.StateComplete {
				add(code, "margin readiness is not complete; cost is incomplete/stale")
			}
		case BlockerAmbiguousMoneyUnit:
			if in.MoneyUnitAmbiguous {
				add(code, "source money unit is ambiguous and was quarantined")
			}
		case BlockerUnusableEvidence:
			if !EvidenceUsable(in.EvidenceQuality) {
				add(code, "cited evidence quality is not usable for an executable number")
			}
		case BlockerUnknownBoundary:
			if !in.BoundaryKnown {
				add(code, "marketplace price boundary is unknown")
			}
		case BlockerPermissionFailure:
			if !in.PermissionGranted {
				add(code, "acting principal lacks approval permission")
			}
		case BlockerPolicyConflict:
			if policyConflicted(in.Policy) {
				add(code, "policy engine produced a hard blocker")
			}
		}
	}
	return out
}

// policyConflicted reports whether the policy result carries any blocker (a
// hard-stage conflict) or produced no proposal. A simulation is never executable
// either, but that is handled by the Simulation flag in Approvable.
func policyConflicted(r policy.Result) bool {
	return len(r.Blockers) > 0 || r.Proposed == nil
}
