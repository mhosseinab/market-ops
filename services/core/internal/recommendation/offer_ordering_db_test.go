package recommendation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// FIX-CYCLE-1 FINDING F7 (issue #87 criterion B) — THE ORDERING REGRESSION PIN.
//
// #87's defect: Market and Bulk Approval collapsed an account's observed offers to the
// FIRST row per target, ordered by `updated_at`. Reordering unrelated `updated_at`
// timestamps therefore changed which offer stood in for the target — and so changed the
// bulk disposition — with no change in the market whatsoever.
//
// The only criterion-B coverage that existed reversed a hand-built array inside an MSW
// fixture, which proves nothing about the server: a hand-sorted in-memory array is not
// an ordering test. This one reorders `observed_offers.updated_at` in REAL PostgreSQL,
// between two runs of the SAME preview, and asserts the semantic result is byte-identical.
//
// No behavioural defect is expected: the seal path is ordering-independent BY
// CONSTRUCTION (GetRecommendationSealedOfferIdentity resolves via
// recommendations.evidence_observation_id — one recommendation names exactly ONE
// observation — and carries no ORDER BY at all), and the FINDING F1 quality gate reads a
// SET of qualities, not a first row. This test exists so that property cannot silently
// regress into a lookup-by-target with an ORDER BY.
//
// Two SEPARATE lineages/sets are used because sibling offers on one target share one
// variant and migration 0012 carries UNIQUE (selection_set_id, variant_id) — so they
// cannot be two members of one selection set.
func TestPreviewBulkSelection_ReorderingObservedOfferTimestampsChangesNothing(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	account, variant := seedVariant(t, q)

	// TWO sibling offers on ONE target, both within the usable evidence-quality set, so
	// this test measures ORDERING and nothing else.
	obsA := seedEvidenceOffer(t, pool, q, account, variant, "order-offer-a")
	obsB := seedEvidenceOffer(t, pool, q, account, variant, "order-offer-b")
	cardA := awaitingCardWithEvidence(t, svc, account, variant, obsA)
	cardB := awaitingCardWithEvidence(t, svc, account, variant, obsB)
	target, nativeVariant := targetOf(t, pool, variant)
	seedObservedOffer(t, pool, account, target, nativeVariant, "order-offer-a", "verified")
	seedObservedOffer(t, pool, account, target, nativeVariant, "order-offer-b", "verified")

	type sealed struct {
		identity    string
		disposition recommendation.Disposition
		reason      string
	}
	previewBoth := func(label string) (sealed, sealed) {
		t.Helper()
		resA, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, label+"-a", nil,
			[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: cardA.RecommendationID}})
		if err != nil {
			t.Fatalf("%s preview of member A: %v", label, err)
		}
		resB, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, label+"-b", nil,
			[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: cardB.RecommendationID}})
		if err != nil {
			t.Fatalf("%s preview of member B: %v", label, err)
		}
		mA, mB := resA.Members[0], resB.Members[0]
		return sealed{mA.OfferIdentity, mA.Disposition, mA.Reason},
			sealed{mB.OfferIdentity, mB.Disposition, mB.Reason}
	}

	beforeA, beforeB := previewBoth("before")
	if beforeA.identity != "order-offer-a" || beforeB.identity != "order-offer-b" {
		t.Fatalf("baseline seals: A=%q B=%q; want each member's OWN offer identity", beforeA.identity, beforeB.identity)
	}

	// Establish a definite order, then REVERSE it in real PostgreSQL. Nothing about the
	// market changed: same offers, same qualities, same evidence, same recommendations.
	if _, err := pool.Exec(ctx, `
		UPDATE observed_offers SET updated_at = CASE offer_identity
			WHEN 'order-offer-a' THEN now() - interval '2 hours'
			WHEN 'order-offer-b' THEN now() - interval '1 hour'
		END WHERE target_id = $1`, target); err != nil {
		t.Fatalf("establish observed-offer order: %v", err)
	}
	orderedA, orderedB := previewBoth("ordered")

	if _, err := pool.Exec(ctx, `
		UPDATE observed_offers SET updated_at = CASE offer_identity
			WHEN 'order-offer-a' THEN now() - interval '1 hour'
			WHEN 'order-offer-b' THEN now() - interval '2 hours'
		END WHERE target_id = $1`, target); err != nil {
		t.Fatalf("reverse observed-offer order: %v", err)
	}
	reversedA, reversedB := previewBoth("reversed")

	if orderedA != reversedA {
		t.Fatalf("member A's sealed result CHANGED when unrelated observed_offers.updated_at timestamps "+
			"were reordered: %+v -> %+v; the disposition and sealed identity must be ordering-independent (issue #87 criterion B)",
			orderedA, reversedA)
	}
	if orderedB != reversedB {
		t.Fatalf("member B's sealed result CHANGED on reorder: %+v -> %+v", orderedB, reversedB)
	}
	// And still each member's OWN offer, not "whichever row sorted first".
	if reversedA.identity != "order-offer-a" || reversedB.identity != "order-offer-b" {
		t.Fatalf("after reorder: A=%q B=%q; want order-offer-a / order-offer-b", reversedA.identity, reversedB.identity)
	}
	if reversedA.disposition != recommendation.DispositionExecutable ||
		reversedB.disposition != recommendation.DispositionExecutable {
		t.Fatalf("after reorder dispositions: A=%q B=%q; want both executable (two usable siblings must not gate)",
			reversedA.disposition, reversedB.disposition)
	}
}
