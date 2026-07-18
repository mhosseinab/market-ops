package execution

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
)

// passingInputs builds a RevalidationInputs where every one of the nine EXE-001
// gates passes: Bound == Current, identity confirmed, price matched, unambiguous
// unit, known boundary, fresh JIT, permission granted, and an unexpired control.
func passingInputs(now time.Time) RevalidationInputs {
	obs := uuid.New()
	b := approval.Binding{
		ActionID:           uuid.New(),
		ParameterVersion:   int64(3),
		ContextVersion:     int64(4),
		PolicyVersion:      int64(5),
		CostProfileVersion: int64(6),
		EvidenceVersions:   map[uuid.UUID]int64{obs: int64(2)},
		Expiry:             now.Add(10 * time.Minute),
	}
	current := b
	current.EvidenceVersions = map[uuid.UUID]int64{obs: int64(2)}
	return RevalidationInputs{
		Bound:               b,
		Current:             current,
		Now:                 now,
		IdentityConfirmed:   true,
		CurrentPriceMatches: true,
		MoneyUnitAmbiguous:  false,
		BoundaryKnown:       true,
		PermissionGranted:   true,
		JITFresh:            true,
	}
}

// TestEvaluateGates_AllPass is the happy path: a fully-consistent revalidation
// clears every gate and authorizes the write.
func TestEvaluateGates_AllPass(t *testing.T) {
	out := EvaluateGates(passingInputs(time.Now()))
	if !out.OK {
		t.Fatalf("all gates should pass; failed on %q (reason %q)", out.Failed, out.Reason)
	}
}

// TestEvaluateGates_InjectedChangePerGate is the EXE-001 acceptance property: for
// EACH of the nine gates, an injected change (and nothing else) blocks the write
// and is attributed to exactly that gate. This proves the matrix is exhaustive —
// there is no gate through which a stale write can slip.
func TestEvaluateGates_InjectedChangePerGate(t *testing.T) {
	now := time.Now()

	cases := map[Gate]func(in *RevalidationInputs){
		GateIdentity:     func(in *RevalidationInputs) { in.IdentityConfirmed = false },
		GateCurrentPrice: func(in *RevalidationInputs) { in.CurrentPriceMatches = false },
		GateCosts:        func(in *RevalidationInputs) { in.Current.CostProfileVersion = int64(999) },
		GateMoneyUnit:    func(in *RevalidationInputs) { in.MoneyUnitAmbiguous = true },
		GateBoundary:     func(in *RevalidationInputs) { in.BoundaryKnown = false },
		GateEvidence:     func(in *RevalidationInputs) { in.JITFresh = false },
		GateGuardrails:   func(in *RevalidationInputs) { in.Current.PolicyVersion = int64(999) },
		GatePermission:   func(in *RevalidationInputs) { in.PermissionGranted = false },
		GateExpiry:       func(in *RevalidationInputs) { in.Now = in.Bound.Expiry },
	}

	// Every named gate must be covered.
	if len(cases) != len(AllGates) {
		t.Fatalf("gate matrix covers %d gates; want %d", len(cases), len(AllGates))
	}

	for _, g := range AllGates {
		inject, ok := cases[g]
		if !ok {
			t.Fatalf("no injected-change case for gate %q", g)
		}
		t.Run(string(g), func(t *testing.T) {
			in := passingInputs(now)
			inject(&in)
			out := EvaluateGates(in)
			if out.OK {
				t.Fatalf("gate %q: injected change did not block the write", g)
			}
			if out.Failed != g {
				t.Fatalf("gate %q: block attributed to %q instead", g, out.Failed)
			}
		})
	}
}

// TestEvaluateGates_EvidenceVersionChangeBlocks proves a changed cited evidence
// version (not just a stale JIT) blocks at the evidence gate (§16).
func TestEvaluateGates_EvidenceVersionChangeBlocks(t *testing.T) {
	now := time.Now()
	in := passingInputs(now)
	for id := range in.Current.EvidenceVersions {
		in.Current.EvidenceVersions[id] = int64(99)
	}
	out := EvaluateGates(in)
	if out.OK || out.Failed != GateEvidence || out.Reason != approval.ReasonEvidenceChanged {
		t.Fatalf("evidence version change: got ok=%v failed=%q reason=%q", out.OK, out.Failed, out.Reason)
	}
}
