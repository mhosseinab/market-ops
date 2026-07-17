package recommendation

import (
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/margin"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// Range is the allowed price range (PRC-001 "allowed range"): the feasible window
// the boundary admits. Present only when the boundary is known.
type Range struct {
	Min money.Money
	Max money.Money
}

// Evidence groups the PRC-001 evidence citation: the observed quality state
// (never upgraded), the evidence references, the cited observation, and the
// capture instant that drives Age.
type Evidence struct {
	Quality       string
	Refs          []string
	ObservationID uuid.UUID
	AsOf          time.Time
}

// AssembleInput is the pure input to Assemble. It carries already-computed money
// values from the margin/policy engines (the only authoritative pricing source,
// §12.3) plus the PRC-002 gate signals; Assemble performs no float math and no
// pricing calculation of its own.
type AssembleInput struct {
	AccountID uuid.UUID
	VariantID uuid.UUID
	// EventID is the market event this recommendation answers, when driven by one.
	EventID uuid.UUID

	Objective           policy.Objective
	CurrentPrice        money.Money
	CurrentContribution money.Money
	// Contribution is the margin breakdown for the PRC-001 inputs and readiness.
	Contribution margin.Contribution
	// Policy is the six-stage engine result: proposed price/contribution or the
	// ordered hard blockers.
	Policy policy.Result
	// Boundary supplies the allowed range when known (PRC-001 allowed range).
	Boundary policy.Boundary

	Evidence Evidence

	// PRC-002 gate signals.
	IdentityConfirmed  bool
	MoneyUnitAmbiguous bool
	BoundaryKnown      bool
	PermissionGranted  bool
	Readiness          cost.State
	EvidenceQuality    string

	Assumptions []string

	// Timing / versioning.
	Now    time.Time
	Expiry time.Time

	// Bound versions carried into the approval control when Approvable (APR-001).
	ActionID           uuid.UUID
	ParameterVersion   int64
	ContextVersion     int64
	PolicyVersion      int64
	CostProfileVersion int64
	EvidenceVersions   map[uuid.UUID]int64

	// Simulation marks a non-executable what-if: it never becomes approvable and
	// never carries a control (§8, §12.3).
	Simulation bool
}

// Recommendation is the PRC-001 record: every field present or explicitly
// unavailable with a reason. It is the deterministic payload chat and screens
// byte-match (CHAT-030); the version-bound control is minted only when Approvable.
type Recommendation struct {
	AccountID uuid.UUID
	VariantID uuid.UUID
	EventID   Optional[uuid.UUID]

	Objective            policy.Objective
	CurrentPrice         money.Money
	ProposedPrice        Optional[money.Money]
	CurrentContribution  Optional[money.Money]
	ProposedContribution Optional[money.Money]
	AllowedRange         Optional[Range]

	// Inputs is the contribution breakdown (PRC-001 inputs), reproducible per
	// CST-002 via each deduction's cost-profile version.
	Inputs []margin.Deduction

	Evidence  Evidence
	Age       Optional[time.Duration]
	Quality   string
	Readiness cost.State

	Assumptions []string
	Blockers    []Blocker
	Expiry      Optional[time.Time]

	Simulation bool

	// binding is the APR-001 version binding used to mint a control; only exposed
	// through BuildBinding when Approvable.
	binding approval.Binding
}

// Assemble builds the PRC-001 recommendation and applies the PRC-002 gate. Every
// optional field is set present or unavailable-with-reason; the blockers are
// detected in fixed order. No approval control is derivable unless Approvable.
func Assemble(in AssembleInput) Recommendation {
	blockers := detectBlockers(in)

	rec := Recommendation{
		AccountID:           in.AccountID,
		VariantID:           in.VariantID,
		Objective:           in.Objective,
		CurrentPrice:        in.CurrentPrice,
		CurrentContribution: Present(in.CurrentContribution),
		Inputs:              in.Contribution.Deductions,
		Evidence:            in.Evidence,
		Quality:             in.EvidenceQuality,
		Readiness:           in.Readiness,
		Assumptions:         in.Assumptions,
		Blockers:            blockers,
		Simulation:          in.Simulation,
	}

	if in.EventID != uuid.Nil {
		rec.EventID = Present(in.EventID)
	} else {
		rec.EventID = Unavailable[uuid.UUID]("recommendation is not driven by a market event")
	}

	// Proposed price/contribution: present as ANALYSIS when the policy engine
	// accepted a price; otherwise explicitly unavailable with the policy reason.
	if in.Policy.Proposed != nil {
		rec.ProposedPrice = Present(in.Policy.Proposed.Price)
		rec.ProposedContribution = Present(in.Policy.Proposed.Contribution)
	} else {
		reason := "policy engine produced no proposal"
		if len(in.Policy.Blockers) > 0 {
			reason = "policy blocked: " + string(in.Policy.Blockers[0].Code)
		}
		rec.ProposedPrice = Unavailable[money.Money](reason)
		rec.ProposedContribution = Unavailable[money.Money](reason)
	}

	// Allowed range: the boundary window when known.
	if in.BoundaryKnown && in.Boundary.Known {
		rec.AllowedRange = Present(Range{Min: in.Boundary.Min, Max: in.Boundary.Max})
	} else {
		rec.AllowedRange = Unavailable[Range]("marketplace price boundary is unknown")
	}

	// Age: from the evidence capture instant.
	if in.Evidence.AsOf.IsZero() {
		rec.Age = Unavailable[time.Duration]("no evidence capture time recorded")
	} else {
		rec.Age = Present(in.Now.Sub(in.Evidence.AsOf))
	}

	// Expiry / control: present ONLY when the recommendation is executable. A
	// non-approvable recommendation carries NO expiry and NO control (PRC-002).
	rec.binding = approval.Binding{
		ActionID:           in.ActionID,
		ParameterVersion:   in.ParameterVersion,
		ContextVersion:     in.ContextVersion,
		PolicyVersion:      in.PolicyVersion,
		CostProfileVersion: in.CostProfileVersion,
		EvidenceVersions:   in.EvidenceVersions,
		Expiry:             in.Expiry,
	}
	if rec.Approvable() {
		rec.Expiry = Present(in.Expiry)
	} else {
		rec.Expiry = Unavailable[time.Time]("recommendation is not executable; no approval control")
	}

	return rec
}

// Approvable is the PRC-002 executable gate: a recommendation may carry a
// structured approval control ONLY when it is not a simulation, has zero
// blockers, readiness is Complete (CST-003), and the policy engine accepted a
// price. If any is false, no control exists.
func (r Recommendation) Approvable() bool {
	return !r.Simulation &&
		len(r.Blockers) == 0 &&
		r.Readiness == cost.StateComplete &&
		r.ProposedPrice.IsPresent()
}

// BuildBinding returns the APR-001 version binding for the approval control and
// true, ONLY when the recommendation is Approvable. A non-approvable
// recommendation returns the zero binding and false — there is no control-bearing
// path around this gate.
func (r Recommendation) BuildBinding() (approval.Binding, bool) {
	if !r.Approvable() {
		return approval.Binding{}, false
	}
	return r.binding, true
}

// NewDraftCard mints the initial Draft approval card for an approvable
// recommendation (the deterministic hand-off into the §8.4 machine). It returns
// false when the recommendation is not approvable, so a blocked or simulated
// recommendation can never produce a card. price is the proposed price; cardID
// and the initial version are assigned by the caller/store.
func (r Recommendation) NewDraftCard(cardID, recommendationID uuid.UUID, version int64) (approval.Card, bool) {
	binding, ok := r.BuildBinding()
	if !ok {
		return approval.Card{}, false
	}
	price, ok := r.ProposedPrice.Get()
	if !ok {
		return approval.Card{}, false
	}
	return approval.NewDraft(cardID, recommendationID, version, binding, price, false), true
}
