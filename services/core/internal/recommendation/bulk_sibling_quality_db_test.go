package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// FIX-CYCLE-1 FINDING F1 (HIGH — issue #87 criterion C; PRD §4.6 evidence-quality
// states, OBS-004, §10.3).
//
// THE DEFECT, reproduced by the safety reviewer: a target carrying a CONFLICTED sibling
// offer was still submitted, previewed, sealed, and classified as an APPROVABLE bulk
// member on the strength of its VERIFIED sibling. Every sibling offer on a target shares
// ONE recommendation id; the client's memberPayload drops the blocked sibling and
// submits the survivor; the server derived the disposition solely from that
// recommendation, and detectBlockers evaluates only the recommendation's OWN cited
// evidence. No code path — client or server — consulted the sibling offers, so the
// server never learned the conflicted offer existed and could not fail closed.
//
// THE NON-NEGOTIABLE (issue #87): a conflicted or stale offer must never make a target
// MORE eligible than its worst applicable offer. An "executable" that hides a conflicted
// sibling IS the defect.
//
// THE REMEDY IMPLEMENTED HERE is the SERVER-SIDE conservative gate, and only that. It
// must hold even when the client submits NOTHING about the sibling — that is the
// reproduction, and trusting a client omission is the exact posture BULK-PROTOCOL
// DESIGN RECORD (e) forbids. Adding a sibling-quality trigger to detectBlockers was
// explicitly DECLINED (it changes recommendation semantics and could move the §20
// event-precision and identity-precision thresholds, which is a product-owner choice).
//
// A structural fact this design works around: migration 0012 carries
// UNIQUE (selection_set_id, variant_id), and there is exactly one live control-bearing
// approval card per variant, so two sibling offers on one target CANNOT be two members
// of one selection set. Criterion C is therefore met by conservative gating, not by
// splitting members.
//
// Negative tests first (§4.6 posture): the ways this makes a target too eligible are
// asserted before the non-over-tightening positives.

// seedObservedOffer inserts one row of the CURRENT Observed Offer projection
// (observed_offers: one row per (target, offer identity), OBS-008) with an explicit
// evidence quality. It is the server-side fact the gate reads; the test never tells the
// service anything about sibling quality.
func seedObservedOffer(t *testing.T, pool *pgxpool.Pool, account, target uuid.UUID, nativeVariant int64, offer, quality string) {
	t.Helper()
	at := time.Now().UTC()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO observed_offers (
			target_id, marketplace_account_id, offer_identity, native_variant_id,
			availability_status, quality, captured_at, freshness_deadline, last_observation_id
		) VALUES ($1,$2,$3,$4,'in_stock',$5,$6,$7,$8)`,
		target, account, offer, nativeVariant, quality, at, at.Add(6*time.Hour), uuid.New(),
	); err != nil {
		t.Fatalf("seed observed offer %q (%s): %v", offer, quality, err)
	}
}

// targetOf returns the observation target the fixtures provisioned for a variant.
func targetOf(t *testing.T, pool *pgxpool.Pool, variant uuid.UUID) (uuid.UUID, int64) {
	t.Helper()
	var id uuid.UUID
	var nv int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id, native_variant_id FROM observation_targets WHERE variant_id = $1`, variant).Scan(&id, &nv); err != nil {
		t.Fatalf("resolve observation target for variant %s: %v", variant, err)
	}
	return id, nv
}

// TestPreviewBulkSelection_ConflictedSiblingOfferBlocksTheTarget is FINDING F1 itself,
// in the exact reproduction shape: the client submits ONLY the survivor member and says
// NOTHING about the conflicted sibling. The gate is server-side and authoritative, so
// the omission changes nothing — the member is degraded to the conservative disposition
// with a stable, non-localized ASCII reason key.
func TestPreviewBulkSelection_ConflictedSiblingOfferBlocksTheTarget(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "f1-verified-survivor")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)
	target, nativeVariant := targetOf(t, pool, variant)

	// The market as the SERVER sees it: the member's own VERIFIED offer, and a
	// CONFLICTED sibling on the same target that the client never mentions.
	seedObservedOffer(t, pool, account, target, nativeVariant, "f1-verified-survivor", "verified")
	seedObservedOffer(t, pool, account, target, nativeVariant, "f1-conflicted-sibling", "conflicted")

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "f1-conflicted", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(res.Members) != 1 {
		t.Fatalf("members: %+v; want 1", res.Members)
	}
	m := res.Members[0]
	if m.Disposition == recommendation.DispositionExecutable {
		t.Fatal("a target carrying a CONFLICTED sibling offer was classified EXECUTABLE on the strength of its " +
			"VERIFIED sibling; a conflicted offer must never make a target MORE eligible than its worst applicable offer (issue #87 criterion C)")
	}
	if m.Disposition != recommendation.DispositionBlocked {
		t.Fatalf("gated member disposition %q; want %q (the conservative disposition)", m.Disposition, recommendation.DispositionBlocked)
	}
	if m.Reason != recommendation.ReasonTargetOfferEvidenceUnusable {
		t.Fatalf("gated member reason %q; want the stable ASCII key %q", m.Reason, recommendation.ReasonTargetOfferEvidenceUnusable)
	}

	// The SEALED identity must not regress: it is still resolved from the member's OWN
	// recommendation evidence, NEVER by a lookup-by-target. The gate reads sibling
	// QUALITIES by target; it never picks an identity by target.
	if m.OfferIdentity != "f1-verified-survivor" {
		t.Fatalf("sealed offer identity %q; want the member's own %q — the gate must not have "+
			"turned identity sealing into a lookup-by-target", m.OfferIdentity, "f1-verified-survivor")
	}

	// The conservative disposition is DURABLE on the sealed member row, so the
	// authoritative confirmation excludes it and it can never execute.
	members, err := svc.Members(ctx, res.Set.ID)
	if err != nil {
		t.Fatalf("reload sealed members: %v", err)
	}
	if members[0].Disposition != string(recommendation.DispositionBlocked) {
		t.Fatalf("sealed member disposition %q; want blocked", members[0].Disposition)
	}
}

// TestPreviewBulkSelection_EveryUnusableSiblingQualityGatesTheTarget pins the
// ACCEPTABLE SET rather than one example: it is exactly the canonical evidence-quality
// states the code already models as usable (recommendation.EvidenceUsable — verified and
// supported, §10.3), which is also the client's own per-offer classification in
// apps/web/src/data/disposition.ts. No new taxonomy is invented and no new state is
// coined here.
func TestPreviewBulkSelection_EveryUnusableSiblingQualityGatesTheTarget(t *testing.T) {
	for _, quality := range []string{"conflicted", "stale", "unavailable", "unverified"} {
		t.Run(quality, func(t *testing.T) {
			pool, q := newPool(t)
			ctx := context.Background()
			svc := recommendation.NewService(pool)
			account, variant := seedVariant(t, q)

			obs := seedEvidenceOffer(t, pool, q, account, variant, "f1-own-"+quality)
			card := awaitingCardWithEvidence(t, svc, account, variant, obs)
			target, nativeVariant := targetOf(t, pool, variant)

			seedObservedOffer(t, pool, account, target, nativeVariant, "f1-own-"+quality, "verified")
			seedObservedOffer(t, pool, account, target, nativeVariant, "f1-sibling-"+quality, quality)

			res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "f1-"+quality, nil,
				[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
			if err != nil {
				t.Fatalf("preview: %v", err)
			}
			if res.Members[0].Disposition != recommendation.DispositionBlocked {
				t.Fatalf("a %s sibling offer left the target %q; want blocked — a target is never more "+
					"eligible than its worst applicable offer", quality, res.Members[0].Disposition)
			}
		})
	}
}

// TestPreviewBulkSelection_UsableSiblingOffersStayExecutable is the OVER-TIGHTENING
// guard, and it is not optional: a gate that blocks every multi-offer target would pass
// every negative test above while destroying the feature. A target whose offers are ALL
// within the acceptable set stays executable, and a single-offer verified target — the
// overwhelmingly common case — is untouched.
func TestPreviewBulkSelection_UsableSiblingOffersStayExecutable(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)

	// (a) SINGLE verified offer on the target.
	account, variant := seedVariant(t, q)
	obs := seedEvidenceOffer(t, pool, q, account, variant, "f1-single-verified")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)
	target, nativeVariant := targetOf(t, pool, variant)
	seedObservedOffer(t, pool, account, target, nativeVariant, "f1-single-verified", "verified")

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "f1-single", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Members[0].Disposition != recommendation.DispositionExecutable {
		t.Fatalf("a single VERIFIED offer yielded %q; want executable (over-tightened)", res.Members[0].Disposition)
	}
	if res.Members[0].Reason != "" {
		t.Fatalf("an ungated member carries reason %q; want empty", res.Members[0].Reason)
	}

	// (b) TWO offers on the target, both within the acceptable set.
	account2, variant2 := seedVariant(t, q)
	obs2 := seedEvidenceOffer(t, pool, q, account2, variant2, "f1-multi-verified")
	card2 := awaitingCardWithEvidence(t, svc, account2, variant2, obs2)
	target2, nativeVariant2 := targetOf(t, pool, variant2)
	seedObservedOffer(t, pool, account2, target2, nativeVariant2, "f1-multi-verified", "verified")
	seedObservedOffer(t, pool, account2, target2, nativeVariant2, "f1-multi-supported", "supported")

	res2, err := svc.PreviewBulkSelection(ctx, account2, uuid.Nil, "f1-multi", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant2, RecommendationID: card2.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res2.Members[0].Disposition != recommendation.DispositionExecutable {
		t.Fatalf("two USABLE sibling offers yielded %q; want executable (over-tightened)", res2.Members[0].Disposition)
	}
}

// TestPreviewBulkSelection_ClosedSiblingOfferDoesNotGate: an offer whose row is CLOSED
// (§16 offer disappearance — ended_at set, last raw price left intact, never zeroed) is
// no longer an APPLICABLE offer on the target. Gating on it would let a long-gone
// competitor listing permanently block a live one, which is over-tightening, not
// conservatism.
func TestPreviewBulkSelection_ClosedSiblingOfferDoesNotGate(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "f1-live-verified")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)
	target, nativeVariant := targetOf(t, pool, variant)
	seedObservedOffer(t, pool, account, target, nativeVariant, "f1-live-verified", "verified")
	seedObservedOffer(t, pool, account, target, nativeVariant, "f1-gone-conflicted", "conflicted")
	if _, err := pool.Exec(ctx,
		`UPDATE observed_offers SET ended_at = now() WHERE target_id = $1 AND offer_identity = $2`,
		target, "f1-gone-conflicted"); err != nil {
		t.Fatalf("close the disappeared offer: %v", err)
	}

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "f1-closed", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Members[0].Disposition != recommendation.DispositionExecutable {
		t.Fatalf("a CLOSED (disappeared) conflicted offer gated the target: %q; want executable — "+
			"only LIVE applicable offers are applicable", res.Members[0].Disposition)
	}
}

// TestConfirmBulkSelection_GatedMemberIsExcludedAndNeverExecutes closes the seam: the
// conservative disposition is not cosmetic. The authoritative confirmation excludes the
// gated member, so it carries no authorization and no execution intent.
func TestConfirmBulkSelection_GatedMemberIsExcludedAndNeverExecutes(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "f1-confirm-survivor")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)
	target, nativeVariant := targetOf(t, pool, variant)
	seedObservedOffer(t, pool, account, target, nativeVariant, "f1-confirm-survivor", "verified")
	seedObservedOffer(t, pool, account, target, nativeVariant, "f1-confirm-conflicted", "conflicted")

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "f1-confirm", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	out, err := svc.ConfirmBulkSelection(ctx, account, res.Set.LineageID, res.Set.Version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !out.Valid || len(out.Items) != 1 {
		t.Fatalf("confirm outcome: %+v; want one valid item", out)
	}
	if out.Items[0].State != recommendation.BulkItemExcluded {
		t.Fatalf("gated member state %q; want excluded — it must never execute", out.Items[0].State)
	}
	if out.ExecutionPending {
		t.Fatal("a gated member reported an execution pending; nothing may be dispatched for it")
	}

	// Belt and braces: no card of this member ever left AwaitingConfirmation.
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM approval_cards WHERE id = $1`, card.ID).Scan(&state); err != nil {
		t.Fatalf("read card state: %v", err)
	}
	if state != "awaiting_confirmation" {
		t.Fatalf("gated member's card advanced to %q; want awaiting_confirmation (never authorized)", state)
	}
	_ = db.New(pool)
}
