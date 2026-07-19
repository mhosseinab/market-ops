package recommendation_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

func mustMoney(t *testing.T, mantissa int64, currency string, exponent int8) money.Money {
	t.Helper()
	m, err := money.New(mantissa, currency, exponent)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return m
}

// TestMembershipFingerprint_DeterministicAndOrderIndependent proves the digest is
// STABLE for the same membership+aggregate and independent of input order (the
// members are canonically sorted) — the property version-binding relies on (#91).
func TestMembershipFingerprint_DeterministicAndOrderIndependent(t *testing.T) {
	a := recommendation.PreviewMemberView{VariantID: uuid.New(), RecommendationID: uuid.New(), Disposition: recommendation.DispositionExecutable}
	b := recommendation.PreviewMemberView{VariantID: uuid.New(), RecommendationID: uuid.New(), Disposition: recommendation.DispositionWarning}
	agg := mustMoney(t, 12345, "IRR", 0)

	fp1 := recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{a, b}, &agg)
	fp2 := recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{a, b}, &agg)
	fp3 := recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{b, a}, &agg) // reversed order

	if !bytes.Equal(fp1, fp2) {
		t.Fatalf("same membership produced different fingerprints")
	}
	if !bytes.Equal(fp1, fp3) {
		t.Fatalf("fingerprint depends on member order; it must be order-independent")
	}
}

// TestMembershipFingerprint_SensitiveToEveryBoundDimension proves the digest DIFFERS
// when any member, count, disposition, or aggregate dimension changes (#91).
func TestMembershipFingerprint_SensitiveToEveryBoundDimension(t *testing.T) {
	a := recommendation.PreviewMemberView{VariantID: uuid.New(), RecommendationID: uuid.New(), Disposition: recommendation.DispositionExecutable}
	b := recommendation.PreviewMemberView{VariantID: uuid.New(), RecommendationID: uuid.New(), Disposition: recommendation.DispositionExecutable}
	agg := mustMoney(t, 100, "IRR", 0)

	base := recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{a}, &agg)

	cases := map[string][]byte{
		"member added (count differs)": recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{a, b}, &agg),
		"disposition differs": recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{
			{VariantID: a.VariantID, RecommendationID: a.RecommendationID, Disposition: recommendation.DispositionBlocked}}, &agg),
		"aggregate mantissa differs": recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{a}, ptr(mustMoney(t, 101, "IRR", 0))),
		"aggregate currency differs": recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{a}, ptr(mustMoney(t, 100, "USD", 0))),
		"aggregate exponent differs": recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{a}, ptr(mustMoney(t, 100, "IRR", 2))),
		"aggregate unknown vs known": recommendation.MembershipFingerprint([]recommendation.PreviewMemberView{a}, nil),
	}
	for name, fp := range cases {
		if bytes.Equal(base, fp) {
			t.Fatalf("fingerprint did not change when %s", name)
		}
	}
}

func ptr(m money.Money) *money.Money { return &m }
