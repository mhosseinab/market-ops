package recommendation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// BULK-PROTOCOL DESIGN RECORD (e) — `offerIdentity` WIRE COMPATIBILITY (issue #87
// criterion D; OBS-004, PRD §4.6 evidence quality + identity quarantine).
//
// #87's defect: Market and Bulk Approval collapsed an account's observed offers to
// the FIRST row per target, so an arbitrary offer stood in for every offer on a
// target. The client-side reduction is gone (criteria A/B/C/E), but the identity did
// not survive the WIRE: bulk preview and execution addressed members by
// (variant, recommendation) only, so the offer an operator reviewed could not be
// attributed in the authoritative result, and nothing tied the authorization back to
// a specific observed offer.
//
// The rules these tests pin, verbatim from the record — none of them may be weakened:
//   - ADDITIVE and OPTIONAL: the field enters no `required` set and is never
//     behaviorally required by the handler. An existing generated client submitting
//     the formerly valid {variantId, recommendationId} shape MUST keep working.
//   - NEVER a client assertion: the server seals the identity from its OWN persisted
//     observation/recommendation state at preview time. A client-supplied value is a
//     SELECTOR validated against the sealed value — never an input that can widen or
//     redirect what gets authorized.
//   - A mismatch FAILS CLOSED as a uniform not-found, exactly like an unknown member
//     (no existence oracle).
//
// Negative tests come FIRST (§4.6 posture): the ways this can go wrong are asserted
// before the happy path.

// seedEvidenceOffer provisions an observation target for `variant` (once) and appends
// ONE observation carrying `offer` as its offer identity, returning the observation id
// a recommendation can cite as its evidence. It is the server-side fact the sealed
// identity is derived from — the test never hands the identity to the service.
//
// Calling it twice for one variant appends a SIBLING offer to the SAME target, which
// is exactly #87's shape: one target legitimately carries MULTIPLE offer identities,
// and each must stay individually attributable. (A variant may have only one active
// confirmed identity, hence one target — so the second call reuses it.)
func seedEvidenceOffer(t *testing.T, pool *pgxpool.Pool, q *db.Queries, account, variant uuid.UUID, offer string) uuid.UUID {
	t.Helper()
	target, nativeVariant, ok := existingTargetFor(t, pool, variant)
	if !ok {
		target, nativeVariant = seedTargetFor(t, pool, q, account, variant)
	}
	return appendOfferObservation(t, pool, account, target, nativeVariant, offer)
}

// existingTargetFor returns the variant's observation target when one already exists.
func existingTargetFor(t *testing.T, pool *pgxpool.Pool, variant uuid.UUID) (uuid.UUID, int64, bool) {
	t.Helper()
	var id uuid.UUID
	var nv int64
	err := pool.QueryRow(context.Background(),
		`SELECT id, native_variant_id FROM observation_targets WHERE variant_id = $1`, variant).Scan(&id, &nv)
	if err != nil {
		return uuid.Nil, 0, false
	}
	return id, nv, true
}

func seedTargetFor(t *testing.T, pool *pgxpool.Pool, q *db.Queries, account, variant uuid.UUID) (uuid.UUID, int64) {
	t.Helper()
	ctx := context.Background()

	nativeVariant := int64(uuid.New().ID())
	nativeProduct := int64(uuid.New().ID())
	var identityID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO market_product_identities
		    (marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active)
		VALUES ($1,$2,$3,$4,'confirmed',true)
		RETURNING id`, account, variant, nativeVariant, nativeProduct).Scan(&identityID); err != nil {
		t.Fatalf("insert confirmed identity: %v", err)
	}
	tgt, err := q.InsertObservationTarget(ctx, db.InsertObservationTargetParams{
		MarketplaceAccountID:     account,
		IdentityID:               identityID,
		VariantID:                variant,
		NativeVariantID:          nativeVariant,
		NativeProductID:          nativeProduct,
		Tier:                     "standard",
		CadenceSeconds:           3600,
		FreshnessDeadlineSeconds: 21600,
	})
	if err != nil {
		t.Fatalf("insert observation target: %v", err)
	}
	return tgt.ID, nativeVariant
}

// appendOfferObservation appends ONE append-only observation for a specific offer
// identity on an existing target.
func appendOfferObservation(t *testing.T, pool *pgxpool.Pool, account, target uuid.UUID, nativeVariant int64, offer string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	at := time.Now().UTC()
	var obsID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO observations (
			captured_at, target_id, marketplace_account_id, native_variant_id,
			native_seller_id, offer_identity, route, parser_version, source_type,
			evidence_ref, price_raw_text, price_raw_value, availability_status,
			quality, freshness_deadline, dedup_key, schema_valid, identity_valid
		) VALUES (
			$1, $2, $3, $4, 'seller-x', $5, 'route_c', 'p1', 'public-web-endpoint',
			'ev-`+offer+`', '1,000', '1000', 'in_stock', 'verified', $6, $7, true, true
		) RETURNING id`,
		at, target, account, nativeVariant, offer, at.Add(6*time.Hour), "dedup-"+uuid.NewString(),
	).Scan(&obsID); err != nil {
		t.Fatalf("append observation for offer %q: %v", offer, err)
	}
	return obsID
}

// awaitingCardWithEvidence persists an approvable card whose recommendation CITES
// `obs` as its evidence observation, then drives it to AwaitingConfirmation. The
// sealed offer identity is derived from that citation by the SERVER — the test never
// tells the service what the identity is.
func awaitingCardWithEvidence(t *testing.T, svc *recommendation.Service, account, variant, obs uuid.UUID) db.ApprovalCard {
	t.Helper()
	ctx := context.Background()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	in.Evidence.ObservationID = obs
	in.EvidenceVersions = map[uuid.UUID]int64{obs: 2}
	rec := recommendation.Assemble(in)
	persisted, err := svc.Persist(ctx, uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation with evidence %s: %v", obs, err)
	}
	card, err := svc.CreateCard(ctx, persisted.ID, uuid.New(), account, rec)
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	if _, err := svc.Advance(ctx, card.ID, approval.StateDraft, approval.StateReadyForReview, "ready"); err != nil {
		t.Fatalf("advance draft→ready: %v", err)
	}
	if _, err := svc.Advance(ctx, card.ID, approval.StateReadyForReview, approval.StateAwaitingConfirmation, "open"); err != nil {
		t.Fatalf("advance ready→awaiting: %v", err)
	}
	got, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("reload awaiting card: %v", err)
	}
	return got
}

// TestPreviewBulkSelection_ClientOfferIdentityMismatchFailsClosedAsUnknownMember is
// the FIRST negative test of design record (e): a client-supplied offerIdentity that
// does not match the SERVER-SEALED identity must fail closed as the SAME uniform
// not-found an unknown member produces (ErrUnknownMember) — never a distinct error
// that would act as an existence oracle for the real identity, and never a value
// that redirects the authorization onto the asserted offer.
//
// This is the containment that makes the selector safe: a client can NARROW to what
// it believes it reviewed, but it can never WIDEN or REDIRECT what gets authorized.
func TestPreviewBulkSelection_ClientOfferIdentityMismatchFailsClosedAsUnknownMember(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "sealed-offer-a")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)

	// The attacker asserts a SIBLING offer identity on the same target. The
	// recommendation's own evidence names sealed-offer-a; the assertion names another.
	_, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "mismatch", nil,
		[]recommendation.PreviewMemberInput{{
			VariantID:        variant,
			RecommendationID: card.RecommendationID,
			OfferIdentity:    "sibling-offer-b",
		}})
	if !errors.Is(err, recommendation.ErrUnknownMember) {
		t.Fatalf("offer-identity selector mismatch: err=%v; want ErrUnknownMember (uniform not-found, no existence oracle)", err)
	}

	// Fail closed means NOTHING was minted: the whole preview transaction rolled back.
	if _, err := db.New(pool).GetCurrentSelectionSet(ctx, uuid.Nil); err == nil {
		t.Fatal("a selection set was minted under a rejected offer-identity selector")
	}
}

// TestPreviewBulkSelection_OmittedOfferIdentityStaysBackwardCompatible is the
// record-(e) BACKWARD-COMPATIBILITY negative test (prior finding 5): the field must
// NEVER be behaviorally required by the handler. An existing generated client that
// submits the formerly valid {variantId, recommendationId} shape — no offerIdentity
// at all — keeps working unchanged, and still receives the SERVER-SEALED identity
// back. Omission is "not asserted", never "assert the empty identity".
func TestPreviewBulkSelection_OmittedOfferIdentityStaysBackwardCompatible(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "sealed-offer-legacy")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)

	// EXACTLY the pre-#87 request shape: no offer identity is supplied.
	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "legacy-client", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("legacy {variantId, recommendationId} shape was rejected: %v", err)
	}
	if len(res.Members) != 1 {
		t.Fatalf("legacy preview members: %+v; want exactly 1", res.Members)
	}
	if got := res.Members[0].OfferIdentity; got != "sealed-offer-legacy" {
		t.Fatalf("legacy client got offer identity %q; want the SERVER-SEALED %q", got, "sealed-offer-legacy")
	}
}

// TestPreviewBulkSelection_SealsOfferIdentityFromRecommendationEvidence is the
// happy path: the sealed identity comes from the member's OWN recommendation
// evidence (recommendations.evidence_observation_id -> observations.offer_identity),
// never from a lookup-by-target. A target may carry MANY offer identities —
// picking one BY TARGET is the #87 defect itself, so the resolution is anchored to
// the recommendation, which names exactly one observation.
//
// A recommendation with NO evidence observation seals ” — EXPLICIT ABSENCE, never
// some other offer's identity (quarantine over inference, §4.6).
func TestPreviewBulkSelection_SealsOfferIdentityFromRecommendationEvidence(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	account, variant := seedVariant(t, q)

	// FIX-CYCLE-1 FINDING F8 (test discrimination). This fixture seeds TWO SIBLING
	// offers on ONE target — the #87 shape — and asserts each member seals its OWN
	// identity. With a single offer per target, a faithful re-introduction of the defect
	// (sealed identity resolved by lookup-by-target,
	// `ORDER BY captured_at DESC LIMIT 1`) returns the SAME string and this test stays
	// green: it could not tell the fix from the defect. Only siblings discriminate.
	//
	// The two members are previewed in SEPARATE selection sets on purpose: migration
	// 0012 carries UNIQUE (selection_set_id, variant_id), and sibling offers on one
	// target share one variant, so they cannot be two members of one set.
	obs := seedEvidenceOffer(t, pool, q, account, variant, "sealed-offer-evidence")
	sibling := seedEvidenceOffer(t, pool, q, account, variant, "sealed-offer-sibling")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)
	siblingCard := awaitingCardWithEvidence(t, svc, account, variant, sibling)

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "sealed", nil,
		[]recommendation.PreviewMemberInput{{
			VariantID:        variant,
			RecommendationID: card.RecommendationID,
			// A MATCHING selector is accepted — it selects, it does not supply.
			OfferIdentity: "sealed-offer-evidence",
		}})
	if err != nil {
		t.Fatalf("preview with a matching selector: %v", err)
	}
	if got := res.Members[0].OfferIdentity; got != "sealed-offer-evidence" {
		t.Fatalf("preview member offer identity %q; want %q", got, "sealed-offer-evidence")
	}

	// The SIBLING, on the SAME target, seals its OWN identity — not the other one and
	// not "whichever offer the target happens to surface first".
	resSibling, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "sealed-sibling", nil,
		[]recommendation.PreviewMemberInput{{
			VariantID:        variant,
			RecommendationID: siblingCard.RecommendationID,
			OfferIdentity:    "sealed-offer-sibling",
		}})
	if err != nil {
		t.Fatalf("preview the sibling offer's member: %v", err)
	}
	if got := resSibling.Members[0].OfferIdentity; got != "sealed-offer-sibling" {
		t.Fatalf("sibling member sealed %q; want its OWN %q — a target carries MANY offer "+
			"identities and picking one BY TARGET is the #87 defect itself", got, "sealed-offer-sibling")
	}
	if res.Members[0].OfferIdentity == resSibling.Members[0].OfferIdentity {
		t.Fatal("two SIBLING offers on one target sealed the SAME identity; each offer must stay individually attributable (OBS-004)")
	}

	// The identity is DURABLE on the sealed member row, so the authorization can be
	// attributed to the exact observed offer long after the request is gone (AUD-001,
	// transcript-independent).
	members, err := svc.Members(ctx, res.Set.ID)
	if err != nil {
		t.Fatalf("reload sealed members: %v", err)
	}
	if len(members) != 1 || members[0].OfferIdentity != "sealed-offer-evidence" {
		t.Fatalf("sealed member row offer identity: %+v; want %q", members, "sealed-offer-evidence")
	}

	// A recommendation with NO evidence observation seals EXPLICIT ABSENCE.
	_, variant2 := seedVariant(t, q)
	_ = variant2
	account2, variantNoEv := seedVariant(t, q)
	cardNoEv := awaitingCard(t, svc, account2, variantNoEv)
	resNoEv, err := svc.PreviewBulkSelection(ctx, account2, uuid.Nil, "no-evidence", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variantNoEv, RecommendationID: cardNoEv.RecommendationID}})
	if err != nil {
		t.Fatalf("preview for a non-observation-driven recommendation: %v", err)
	}
	if got := resNoEv.Members[0].OfferIdentity; got != "" {
		t.Fatalf("non-observation-driven member sealed offer identity %q; want '' (explicit absence, never inference)", got)
	}
}

// TestConfirmBulkSelection_ItemCarriesTheSealedOfferIdentity is criterion D across
// the WHOLE seam: preview and the authoritative confirmation report the SAME explicit
// offer identity, read from the sealed member row — never re-derived by a
// lookup-by-target at confirm time, which could resolve a different sibling offer if
// the target's observations changed in between.
func TestConfirmBulkSelection_ItemCarriesTheSealedOfferIdentity(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "sealed-offer-confirm")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "confirm-identity", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Members[0].OfferIdentity != "sealed-offer-confirm" {
		t.Fatalf("preview sealed %q; want sealed-offer-confirm", res.Members[0].OfferIdentity)
	}

	out, err := svc.ConfirmBulkSelection(ctx, account, res.Set.LineageID, res.Set.Version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm bulk selection: %v", err)
	}
	if !out.Valid || len(out.Items) != 1 {
		t.Fatalf("confirm outcome: %+v; want one valid item", out)
	}
	if got := out.Items[0].OfferIdentity; got != "sealed-offer-confirm" {
		t.Fatalf("confirm item offer identity %q; want the SAME sealed %q (criterion D)", got, "sealed-offer-confirm")
	}
}

// TestSealedOfferIdentity_ForeignAccountObservationResolvesToAbsence is FIX-CYCLE-1
// FINDING F9 (identity quarantine, defence in depth — §4.6).
//
// GetRecommendationSealedOfferIdentity joined observations with NO account predicate,
// departing from this repo's own precedent (ListUnconsumedObservationsByTarget carries
// an explicit issue-#131 tenant predicate). recommendations.evidence_observation_id
// carries no foreign key — none is constructable to a partitioned table — so NOTHING at
// the database bound a cited observation to the recommendation's account. No reachable
// exploit was constructed (the caller-ownership check runs before the lookup and
// evidence_observation_id is server-written), but a citation that crosses tenants must
// resolve to ” — EXPLICIT ABSENCE / quarantine — and never seal another tenant's offer
// identity onto this tenant's member row.
func TestSealedOfferIdentity_ForeignAccountObservationResolvesToAbsence(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)

	victimAccount, victimVariant := seedVariant(t, q)
	foreignAccount, foreignVariant := seedVariant(t, q)

	// The FOREIGN tenant's observation, carrying a real offer identity.
	foreignObs := seedEvidenceOffer(t, pool, q, foreignAccount, foreignVariant, "foreign-tenant-offer")

	// A recommendation of the VICTIM's account that cites it. Written with raw SQL:
	// no application path constructs this, which is exactly why the resolution must not
	// depend on an application path being careful.
	var recID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO recommendations (
			marketplace_account_id, variant_id, lineage_id, version, objective,
			current_price_mantissa, current_price_currency, current_price_exponent,
			readiness, evidence_quality, evidence_observation_id)
		VALUES ($1,$2,$3,1,'maximize_contribution',1000,'IRR',0,'complete','verified',$4)
		RETURNING id`, victimAccount, victimVariant, uuid.New(), foreignObs).Scan(&recID); err != nil {
		t.Fatalf("insert cross-tenant citation: %v", err)
	}

	res, err := svc.PreviewBulkSelection(ctx, victimAccount, uuid.Nil, "cross-tenant-citation", nil,
		[]recommendation.PreviewMemberInput{{VariantID: victimVariant, RecommendationID: recID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if got := res.Members[0].OfferIdentity; got != "" {
		t.Fatalf("a recommendation citing ANOTHER TENANT'S observation sealed %q; want '' "+
			"(explicit absence / quarantine, never another tenant's offer identity)", got)
	}
}
