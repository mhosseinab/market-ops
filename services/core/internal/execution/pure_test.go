package execution

import (
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

func mustMoney(t *testing.T, mantissa int64, currency string, exp int8) money.Money {
	t.Helper()
	m, err := money.New(mantissa, currency, exp)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return m
}

// TestClassify_UnknownFailsClosedToPendingReconciliation is the EXE-003 never-cut
// property: an UNKNOWN write result is never inferred as success/failure — it
// parks in PendingReconciliation.
func TestClassify_UnknownFailsClosedToPendingReconciliation(t *testing.T) {
	cases := map[WriteOutcome]ExternalState{
		OutcomeAccepted:  StateAccepted,
		OutcomeRejected:  StateRejected,
		OutcomeFailed:    StateFailed,
		OutcomeUnknown:   StatePendingReconciliation,
		WriteOutcome(""): StatePendingReconciliation, // unrecognised also fails closed.
	}
	for outcome, want := range cases {
		if got := Classify(WriteResult{Outcome: outcome}); got != want {
			t.Fatalf("Classify(%q) = %q; want %q", outcome, got, want)
		}
	}
}

// TestWriteEnablement_DefaultDenies proves execution is OFF by default: the zero
// value, an unverified region, and an unsupported capability all deny — only both
// keys turned permit a write.
func TestWriteEnablement_DefaultDenies(t *testing.T) {
	if (WriteEnablement{}).CanWrite() {
		t.Fatalf("zero-value enablement must deny writes")
	}
	if (WriteEnablement{CapabilitySupported: true}).CanWrite() {
		t.Fatalf("capability alone (region unverified) must deny")
	}
	if (WriteEnablement{RegionWriteVerified: true}).CanWrite() {
		t.Fatalf("region verified alone (capability unsupported) must deny")
	}
	if !(WriteEnablement{CapabilitySupported: true, RegionWriteVerified: true}).CanWrite() {
		t.Fatalf("both keys turned must permit")
	}
}

// TestEnablementFromRegistry_UnknownCapabilityDenies proves a default registry
// (every capability Unknown) never enables a write even when the region flag is
// somehow set — Unknown never enables dependent logic (§15.2 never-cut).
func TestEnablementFromRegistry_UnknownCapabilityDenies(t *testing.T) {
	reg := connector.NewRegistry() // all Unknown
	if EnablementFromRegistry(reg, true).CanWrite() {
		t.Fatalf("Unknown price_write capability must never enable a write")
	}
	if EnablementFromRegistry(nil, true).CanWrite() {
		t.Fatalf("nil registry must fail closed")
	}
}

// TestMatch_ExternallyExecutedWithinWindow proves EXE-005: a matching owned-price
// observation within 24h of approval tags the action externally executed.
func TestMatch_ExternallyExecutedWithinWindow(t *testing.T) {
	approvedAt := time.Now()
	price := mustMoney(t, 95000, "IRR", 0)
	res := Match(MatchInput{
		ApprovedPrice: price,
		ApprovedAt:    approvedAt,
		Observations: []OwnedPriceObservation{
			{Price: mustMoney(t, 90000, "IRR", 0), ObservedAt: approvedAt.Add(time.Hour)}, // different value
			{Price: price, ObservedAt: approvedAt.Add(2 * time.Hour)},                     // match
		},
		Now: approvedAt.Add(3 * time.Hour),
	})
	if res.State != StateExternallyExecuted {
		t.Fatalf("state = %q; want externally_executed", res.State)
	}
	if res.Matched == nil {
		t.Fatalf("expected a matched observation")
	}
}

// TestMatch_LapsedAfterWindow proves the window closes to Lapsed when no matching
// observation arrived in 24h, and that a match OUTSIDE the window does not count.
func TestMatch_LapsedAfterWindow(t *testing.T) {
	approvedAt := time.Now().Add(-48 * time.Hour)
	price := mustMoney(t, 95000, "IRR", 0)
	res := Match(MatchInput{
		ApprovedPrice: price,
		ApprovedAt:    approvedAt,
		Observations: []OwnedPriceObservation{
			{Price: price, ObservedAt: approvedAt.Add(30 * time.Hour)}, // matched value, but past 24h
		},
		Now: time.Now(),
	})
	if res.State != StateLapsed {
		t.Fatalf("state = %q; want lapsed", res.State)
	}
}

// TestMatch_AwaitingWhileWindowOpen proves the pending state while the window is
// still open with no match yet.
func TestMatch_AwaitingWhileWindowOpen(t *testing.T) {
	approvedAt := time.Now()
	res := Match(MatchInput{
		ApprovedPrice: mustMoney(t, 95000, "IRR", 0),
		ApprovedAt:    approvedAt,
		Now:           approvedAt.Add(time.Hour),
	})
	if res.State != StateAwaitingExternalExecution {
		t.Fatalf("state = %q; want awaiting_external_execution", res.State)
	}
}

// TestMatch_IncompatibleUnitNeverCoerced proves a currency/exponent-incompatible
// observation is never coerced into a match (money never inferred, §9.1/§16).
func TestMatch_IncompatibleUnitNeverCoerced(t *testing.T) {
	approvedAt := time.Now()
	res := Match(MatchInput{
		ApprovedPrice: mustMoney(t, 95000, "IRR", 0),
		ApprovedAt:    approvedAt,
		Observations: []OwnedPriceObservation{
			{Price: mustMoney(t, 95000, "IRR", 2), ObservedAt: approvedAt.Add(time.Hour)}, // exponent differs
		},
		Now: approvedAt.Add(time.Hour),
	})
	if res.State == StateExternallyExecuted {
		t.Fatalf("incompatible-unit observation must not match")
	}
}
