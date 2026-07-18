package recommendation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/margin"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

func irr(t *testing.T, m int64) money.Money {
	t.Helper()
	v, err := money.New(m, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return v
}

// baseValidInput builds a fully approvable recommendation input: identity
// confirmed, cost complete, money unit unambiguous, evidence usable, boundary
// known, permission granted, and a policy proposal present.
func baseValidInput(t *testing.T) recommendation.AssembleInput {
	t.Helper()
	now := time.Now()
	obs := uuid.New()
	proposal := &policy.Proposal{Price: irr(t, 1050), Contribution: irr(t, 300)}
	return recommendation.AssembleInput{
		AccountID:           uuid.New(),
		VariantID:           uuid.New(),
		EventID:             uuid.New(),
		Objective:           policy.ObjectiveMaximizeContribution,
		CurrentPrice:        irr(t, 1000),
		CurrentContribution: irr(t, 250),
		Contribution: margin.Contribution{
			Amount:    irr(t, 250),
			Readiness: cost.StateComplete,
			Deductions: []margin.Deduction{
				{Component: cost.ComponentCOGS, Amount: irr(t, 600), Kind: margin.KindAbsolute, Version: 3},
			},
		},
		Policy:             policy.Result{Proposed: proposal},
		Boundary:           policy.Boundary{Known: true, Min: irr(t, 900), Max: irr(t, 1200)},
		Evidence:           recommendation.Evidence{Quality: "verified", Refs: []string{"obs://ref"}, ObservationID: obs, AsOf: now.Add(-time.Minute)},
		IdentityConfirmed:  true,
		MoneyUnitAmbiguous: false,
		BoundaryKnown:      true,
		PermissionGranted:  true,
		Readiness:          cost.StateComplete,
		EvidenceQuality:    "verified",
		Assumptions:        []string{"owned-offer P0 model"},
		Now:                now,
		Expiry:             now.Add(5 * time.Minute),
		ActionID:           uuid.New(),
		ParameterVersion:   1,
		ContextVersion:     1,
		PolicyVersion:      1,
		CostProfileVersion: 3,
		EvidenceVersions:   map[uuid.UUID]int64{obs: 2},
	}
}

// TestApprovable_HappyPath confirms the fully-valid input IS approvable and mints
// a Draft card with a bound control seam.
func TestApprovable_HappyPath(t *testing.T) {
	rec := recommendation.Assemble(baseValidInput(t))
	if !rec.Approvable() {
		t.Fatalf("valid recommendation not approvable; blockers=%v", rec.Blockers)
	}
	if _, ok := rec.BuildBinding(); !ok {
		t.Fatalf("approvable recommendation refused to build a binding")
	}
	card, ok := rec.NewDraftCard(uuid.New(), uuid.New(), 1)
	if !ok {
		t.Fatalf("approvable recommendation refused to mint a Draft card")
	}
	if card.Simulation {
		t.Fatalf("executable card must not be a simulation")
	}
}

// TestPRC002_NegativeFixtureSuite_ZeroControls is the PRC-002 never-cut proof:
// each of the seven blockers (and a simulation) makes the recommendation
// non-approvable, exposes NO binding, and mints NO card — zero approval controls.
func TestPRC002_NegativeFixtureSuite_ZeroControls(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(in *recommendation.AssembleInput)
		wantCode recommendation.BlockerCode
	}{
		{"unconfirmed_identity", func(in *recommendation.AssembleInput) { in.IdentityConfirmed = false }, recommendation.BlockerUnconfirmedIdentity},
		{"incomplete_cost", func(in *recommendation.AssembleInput) {
			in.Readiness = cost.StatePartial
			in.Contribution.Readiness = cost.StatePartial
		}, recommendation.BlockerIncompleteCost},
		{"stale_cost", func(in *recommendation.AssembleInput) {
			in.Readiness = cost.StateStale
			in.Contribution.Readiness = cost.StateStale
		}, recommendation.BlockerIncompleteCost},
		{"missing_cost", func(in *recommendation.AssembleInput) {
			in.Readiness = cost.StateMissing
			in.Contribution.Readiness = cost.StateMissing
		}, recommendation.BlockerIncompleteCost},
		{"ambiguous_money_unit", func(in *recommendation.AssembleInput) { in.MoneyUnitAmbiguous = true }, recommendation.BlockerAmbiguousMoneyUnit},
		{"unusable_evidence", func(in *recommendation.AssembleInput) { in.EvidenceQuality = "conflicted" }, recommendation.BlockerUnusableEvidence},
		{"unknown_boundary", func(in *recommendation.AssembleInput) {
			in.BoundaryKnown = false
			in.Boundary = policy.Boundary{Known: false}
		}, recommendation.BlockerUnknownBoundary},
		{"permission_failure", func(in *recommendation.AssembleInput) { in.PermissionGranted = false }, recommendation.BlockerPermissionFailure},
		{"policy_conflict", func(in *recommendation.AssembleInput) {
			in.Policy = policy.Result{Blockers: []policy.Blocker{{Stage: policy.StageHardFloor, Code: policy.BlockerBelowFloor, Message: "x"}}}
		}, recommendation.BlockerPolicyConflict},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseValidInput(t)
			tc.mutate(&in)
			rec := recommendation.Assemble(in)

			if rec.Approvable() {
				t.Fatalf("blocker %q left recommendation approvable", tc.name)
			}
			if _, ok := rec.BuildBinding(); ok {
				t.Fatalf("blocker %q exposed a binding (approval control)", tc.name)
			}
			if _, ok := rec.NewDraftCard(uuid.New(), uuid.New(), 1); ok {
				t.Fatalf("blocker %q minted a card (approval control)", tc.name)
			}
			if !hasBlocker(rec.Blockers, tc.wantCode) {
				t.Fatalf("blocker %q missing expected code %q; got %v", tc.name, tc.wantCode, rec.Blockers)
			}
			// Expiry is explicitly unavailable-with-reason (PRC-001).
			if rec.Expiry.IsPresent() {
				t.Fatalf("blocker %q left an expiry present (implies a live control)", tc.name)
			}
			if rec.Expiry.Reason() == "" {
				t.Fatalf("blocker %q: unavailable expiry has no reason (PRC-001)", tc.name)
			}
			t.Logf("PRC-002 %-22s -> approvable=false, controls=0, code=%s", tc.name, tc.wantCode)
		})
	}
}

// TestSimulation_NeverApprovable asserts a simulation carries no control even
// when every other signal is valid (§8, §12.3).
func TestSimulation_NeverApprovable(t *testing.T) {
	in := baseValidInput(t)
	in.Simulation = true
	rec := recommendation.Assemble(in)
	if rec.Approvable() {
		t.Fatalf("simulation is approvable")
	}
	if _, ok := rec.NewDraftCard(uuid.New(), uuid.New(), 1); ok {
		t.Fatalf("simulation minted a card")
	}
}

// TestPRC001_EveryFieldPresentOrUnavailableWithReason walks the optional PRC-001
// fields and asserts each is either present or carries a non-empty reason — no
// silent gaps — for both an approvable and a blocked recommendation.
func TestPRC001_EveryFieldPresentOrUnavailableWithReason(t *testing.T) {
	check := func(t *testing.T, rec recommendation.Recommendation) {
		assertField(t, "eventId", rec.EventID.IsPresent(), rec.EventID.Reason())
		assertField(t, "proposedPrice", rec.ProposedPrice.IsPresent(), rec.ProposedPrice.Reason())
		assertField(t, "currentContribution", rec.CurrentContribution.IsPresent(), rec.CurrentContribution.Reason())
		assertField(t, "proposedContribution", rec.ProposedContribution.IsPresent(), rec.ProposedContribution.Reason())
		assertField(t, "allowedRange", rec.AllowedRange.IsPresent(), rec.AllowedRange.Reason())
		assertField(t, "age", rec.Age.IsPresent(), rec.Age.Reason())
		assertField(t, "expiry", rec.Expiry.IsPresent(), rec.Expiry.Reason())
	}
	t.Run("approvable", func(t *testing.T) { check(t, recommendation.Assemble(baseValidInput(t))) })
	t.Run("blocked", func(t *testing.T) {
		in := baseValidInput(t)
		in.BoundaryKnown = false
		in.Boundary = policy.Boundary{Known: false}
		in.Evidence.AsOf = time.Time{}
		check(t, recommendation.Assemble(in))
	})
}

func assertField(t *testing.T, name string, present bool, reason string) {
	t.Helper()
	if !present && reason == "" {
		t.Fatalf("field %q is unavailable without a reason (PRC-001 violated)", name)
	}
}

func hasBlocker(bs []recommendation.Blocker, code recommendation.BlockerCode) bool {
	for _, b := range bs {
		if b.Code == code {
			return true
		}
	}
	return false
}
