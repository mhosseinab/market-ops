package recommendation_test

import (
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// Issue #141: a selection aggregate is KNOWN only when EVERY included member has
// compatible, available contribution evidence. These are pure, DB-free tests of
// the aggregate-completeness rule so it goes Red→Green locally (the end-to-end
// preview/persist equality lives in the DB-backed test, deferred to CI).

// TestAggregateContribution_OneUnavailableMemberMakesWholeAggregateUnknown is the
// core fix: a member whose contribution evidence is unavailable must flip the
// WHOLE aggregate to unknown — never a partial known total that silently treats
// the missing member as a zero contributor.
func TestAggregateContributionOneUnavailableMemberMakesWholeAggregateUnknown(t *testing.T) {
	contribs := []recommendation.MemberContribution{
		{Available: true, Mantissa: 300, Currency: "IRR", Exponent: 0},
		{Available: false},
	}
	got, err := recommendation.AggregateContributionForTest(contribs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("aggregate = %v, want nil/unknown — one unavailable member makes the complete aggregate unavailable, never a partial total", *got)
	}
}

// TestAggregateContribution_UnavailableFirstMemberIgnoresLaterAvailable proves the
// order-independence of the rule: an unavailable member anywhere in the set makes
// the aggregate unknown, even if it precedes available members.
func TestAggregateContributionUnavailableFirstMemberIgnoresLaterAvailable(t *testing.T) {
	contribs := []recommendation.MemberContribution{
		{Available: false},
		{Available: true, Mantissa: 300, Currency: "IRR", Exponent: 0},
	}
	got, err := recommendation.AggregateContributionForTest(contribs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("aggregate = %v, want nil/unknown", *got)
	}
}

// TestAggregateContribution_MixedCompatibleAvailableSumExactly proves the known
// path: every member available and compatible sums to the exact integer total.
func TestAggregateContributionMixedCompatibleAvailableSumExactly(t *testing.T) {
	contribs := []recommendation.MemberContribution{
		{Available: true, Mantissa: 300, Currency: "IRR", Exponent: 0},
		{Available: true, Mantissa: 125, Currency: "IRR", Exponent: 0},
		{Available: true, Mantissa: -50, Currency: "IRR", Exponent: 0},
	}
	got, err := recommendation.AggregateContributionForTest(contribs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("aggregate = nil, want a known sum")
	}
	want, err := money.New(375, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	if eq, _ := got.Equal(want); !eq {
		t.Fatalf("aggregate = %s, want %s (300 + 125 - 50)", got, want)
	}
}

// TestAggregateContribution_SingleAvailableMemberIsKnown covers the minimal known
// case.
func TestAggregateContributionSingleAvailableMemberIsKnown(t *testing.T) {
	got, err := recommendation.AggregateContributionForTest([]recommendation.MemberContribution{
		{Available: true, Mantissa: 300, Currency: "IRR", Exponent: 0},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("aggregate = nil, want known 300 IRR")
	}
	if got.Mantissa() != 300 || got.Currency() != "IRR" || got.Exponent() != 0 {
		t.Fatalf("aggregate = %s, want IRR:300:0", got)
	}
}

// TestAggregateContribution_EmptyIsUnknown: no members ⇒ no known aggregate (never
// a fabricated zero).
func TestAggregateContributionEmptyIsUnknown(t *testing.T) {
	got, err := recommendation.AggregateContributionForTest(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("aggregate = %v, want nil for an empty membership", *got)
	}
}

// TestAggregateContribution_CurrencyMismatchFailsClosed: two valid-but-different
// currencies cannot be summed ⇒ explicit unknown, never a partial amount.
func TestAggregateContributionCurrencyMismatchFailsClosed(t *testing.T) {
	got, err := recommendation.AggregateContributionForTest([]recommendation.MemberContribution{
		{Available: true, Mantissa: 300, Currency: "IRR", Exponent: 0},
		{Available: true, Mantissa: 300, Currency: "USD", Exponent: 0},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("aggregate = %v, want nil/unknown on currency mismatch", *got)
	}
}

// TestAggregateContribution_ExponentMismatchFailsClosed: same currency, different
// exponent cannot be summed ⇒ explicit unknown.
func TestAggregateContributionExponentMismatchFailsClosed(t *testing.T) {
	got, err := recommendation.AggregateContributionForTest([]recommendation.MemberContribution{
		{Available: true, Mantissa: 300, Currency: "IRR", Exponent: 0},
		{Available: true, Mantissa: 300, Currency: "IRR", Exponent: -2},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("aggregate = %v, want nil/unknown on exponent mismatch", *got)
	}
}

// TestAggregateContribution_OverflowFailsClosed: an int64 overflow while summing
// leaves the aggregate in an explicit unknown state, never a wrapped value.
func TestAggregateContributionOverflowFailsClosed(t *testing.T) {
	const maxInt64 = int64(9223372036854775807)
	got, err := recommendation.AggregateContributionForTest([]recommendation.MemberContribution{
		{Available: true, Mantissa: maxInt64, Currency: "IRR", Exponent: 0},
		{Available: true, Mantissa: 1, Currency: "IRR", Exponent: 0},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("aggregate = %v, want nil/unknown on int64 overflow", *got)
	}
}

// TestAggregateContribution_MalformedCurrencyIsHardError: an invalid currency code
// is a data fault that fails closed as an error (quarantine), never a silent
// unknown that could be mistaken for a routine unavailable aggregate.
func TestAggregateContributionMalformedCurrencyIsHardError(t *testing.T) {
	_, err := recommendation.AggregateContributionForTest([]recommendation.MemberContribution{
		{Available: true, Mantissa: 300, Currency: "ZZZ", Exponent: 0},
	})
	if err == nil {
		t.Fatal("want a hard error for a malformed currency code, got nil")
	}
}
