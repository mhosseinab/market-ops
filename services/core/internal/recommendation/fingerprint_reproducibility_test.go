package recommendation_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// TestMembershipFingerprint_IsUnaffectedByOfferIdentity is the CST-002 / historical
// reproducibility guard for issue #87.
//
// #87 seals an observed-offer identity onto every selection-set member. The tempting
// "improvement" is to hash it into the membership fingerprint. That would be a
// REGRESSION, not a fix: every selection-set version sealed BEFORE #87 stored a
// fingerprint computed WITHOUT the identity, so recomputing one of those versions'
// digests would no longer reproduce its stored value, and "which membership produced
// this fingerprint" would stop being answerable for historical versions.
//
// It buys nothing either. The sealed identity is a PURE FUNCTION of the member's
// recommendation id — recommendations are append-only, so a given id's evidence
// observation is immutable — and the recommendation id is ALREADY hashed. The
// identity is therefore bound TRANSITIVELY.
//
// This test exists so a later change that adds the identity to the digest fails
// loudly here rather than silently breaking replay of past recommendations.
func TestMembershipFingerprint_IsUnaffectedByOfferIdentity(t *testing.T) {
	variant := uuid.New()
	rec := uuid.New()

	base := []recommendation.PreviewMemberView{{
		VariantID:        variant,
		RecommendationID: rec,
		Disposition:      recommendation.DispositionExecutable,
	}}
	// The SAME membership, sealed after #87 with the identity the server resolved.
	withIdentity := []recommendation.PreviewMemberView{{
		VariantID:        variant,
		RecommendationID: rec,
		Disposition:      recommendation.DispositionExecutable,
		OfferIdentity:    "1234567:seller-x",
	}}

	if !bytes.Equal(
		recommendation.MembershipFingerprint(base, nil),
		recommendation.MembershipFingerprint(withIdentity, nil),
	) {
		t.Fatal("membership fingerprint changed when an offer identity was sealed: " +
			"every version sealed before #87 would stop reproducing its stored digest (CST-002 regression)")
	}

	// And the dimensions the fingerprint DOES bind still discriminate — this test
	// must not be satisfiable by a fingerprint that ignores everything.
	other := []recommendation.PreviewMemberView{{
		VariantID:        variant,
		RecommendationID: uuid.New(),
		Disposition:      recommendation.DispositionExecutable,
	}}
	if bytes.Equal(
		recommendation.MembershipFingerprint(base, nil),
		recommendation.MembershipFingerprint(other, nil),
	) {
		t.Fatal("membership fingerprint does not discriminate a different recommendation id")
	}
}
