package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// bulk_action_bindings — the APPEND-ONLY bulk provenance ledger (issue #87, prior
// findings 1 and 3; PRD §4.6 idempotency + audit, AUD-001).
//
// Prior finding 1 (HIGH): approved-card replay did not verify that a durable binding
// EXACTLY matches the current selection set, member, lineage, version, variant,
// recommendation, and offer. A card approved INDIVIDUALLY — or through selection A —
// could be reported `already_authorized` for selection B without matching durable
// provenance, which tells an operator that selection B authorized something it never
// touched.
//
// Prior finding 3 (HIGH): the ledger duplicated provenance fields with NO composite FK
// or trigger, and PostgreSQL accepted a FORGED row whose claimed set / lineage /
// version / offer did not match the referenced member. Relational consistency must be
// enforced IN THE DATABASE (migration 0049's composite FKs + provenance trigger), the
// way migration 0045 does it — not merely in Go, which a direct SQL writer bypasses.
//
// Negative tests come FIRST: the forgeries and the append-only violations are asserted
// before any happy-path replay behaviour.

// bindingFixture seals a one-member selection set whose member's card is a live
// control, and returns everything a ledger row must be consistent with.
type bindingFixture struct {
	account   uuid.UUID
	variant   uuid.UUID
	set       db.SelectionSet
	member    db.SelectionSetMember
	card      db.ApprovalCard
	offerName string
}

func seedBindingFixture(t *testing.T, pool *pgxpool.Pool, q *db.Queries, svc *recommendation.Service, offer string) bindingFixture {
	t.Helper()
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	obs := seedEvidenceOffer(t, pool, q, account, variant, offer)
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "ledger-"+offer, nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	members, err := svc.Members(ctx, res.Set.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("reload members: %v (%d)", err, len(members))
	}
	return bindingFixture{
		account: account, variant: variant,
		set: res.Set, member: members[0], card: card, offerName: offer,
	}
}

// insertLedgerRow attempts a DIRECT SQL insert into the ledger, bypassing every Go
// guard. It returns the database's error (nil when accepted).
func insertLedgerRow(ctx context.Context, pool *pgxpool.Pool, r db.BulkActionBinding) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO bulk_action_bindings (
			selection_set_member_id, selection_set_id, selection_set_lineage_id,
			selection_set_version, marketplace_account_id, variant_id,
			recommendation_id, offer_identity, card_id, action_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		r.SelectionSetMemberID, r.SelectionSetID, r.SelectionSetLineageID,
		r.SelectionSetVersion, r.MarketplaceAccountID, r.VariantID,
		r.RecommendationID, r.OfferIdentity, r.CardID, r.ActionID)
	return err
}

// TestBulkActionBindings_ForgedRowRejectedByPostgres is prior finding 3, asserted the
// only way that closes it: RAW SQL, several independent forgery variants, each of
// which a Go-only check would miss entirely. Every one must be rejected by PostgreSQL.
func TestBulkActionBindings_ForgedRowRejectedByPostgres(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	f := seedBindingFixture(t, pool, q, svc, "ledger-offer-a")

	// The HONEST row. It must be accepted, so the forgery assertions below cannot be
	// passing merely because the table rejects everything (over-tightening guard).
	honest := db.BulkActionBinding{
		SelectionSetMemberID:  f.member.ID,
		SelectionSetID:        f.set.ID,
		SelectionSetLineageID: f.set.LineageID,
		SelectionSetVersion:   f.set.Version,
		MarketplaceAccountID:  f.account,
		VariantID:             f.variant,
		RecommendationID:      f.card.RecommendationID,
		OfferIdentity:         f.offerName,
		CardID:                f.card.ID,
		ActionID:              f.card.ActionID,
	}

	// A second, INDEPENDENT fixture supplies genuinely existing but WRONG references,
	// so a forgery cannot be rejected merely for naming a nonexistent row.
	other := seedBindingFixture(t, pool, q, svc, "ledger-offer-b")

	forgeries := []struct {
		name   string
		mutate func(r *db.BulkActionBinding)
	}{
		{"lineage of another selection set", func(r *db.BulkActionBinding) { r.SelectionSetLineageID = other.set.LineageID }},
		{"a version the set does not have", func(r *db.BulkActionBinding) { r.SelectionSetVersion = f.set.Version + 1 }},
		{"a variant the member does not name", func(r *db.BulkActionBinding) { r.VariantID = other.variant }},
		{"a recommendation the member does not name", func(r *db.BulkActionBinding) { r.RecommendationID = other.card.RecommendationID }},
		{"an offer identity the member did not seal", func(r *db.BulkActionBinding) { r.OfferIdentity = other.offerName }},
		{"an account the member does not belong to", func(r *db.BulkActionBinding) { r.MarketplaceAccountID = other.account }},
		{"a member of a DIFFERENT selection set", func(r *db.BulkActionBinding) { r.SelectionSetMemberID = other.member.ID }},
		{"a selection set the member is not in", func(r *db.BulkActionBinding) { r.SelectionSetID = other.set.ID }},
		// FIX-CYCLE-1 FINDING F2 (HIGH — AUD-001 + identity quarantine). card_id and
		// action_id are the two columns naming WHAT WAS ACTUALLY AUTHORIZED, and the
		// provenance trigger verified NEITHER: card_id carried only a bare
		// REFERENCES approval_cards (id), which ANY existing card of ANY account
		// satisfies, and action_id carried no constraint at all. A ledger row could
		// therefore attribute this member's authorization to another account's card,
		// or name an action id that is not that card's — an audit trail describing an
		// authorization that never happened, which AUD-001 forbids outright.
		{"a card belonging to a DIFFERENT member and account", func(r *db.BulkActionBinding) {
			r.CardID = other.card.ID
			r.ActionID = other.card.ActionID
		}},
		{"an ARBITRARY action id that is not the card's", func(r *db.BulkActionBinding) {
			r.ActionID = uuid.New()
		}},
	}
	for _, fg := range forgeries {
		t.Run(fg.name, func(t *testing.T) {
			row := honest
			fg.mutate(&row)
			if err := insertLedgerRow(ctx, pool, row); err == nil {
				t.Fatalf("PostgreSQL ACCEPTED a forged provenance row (%s); "+
					"relational consistency must be enforced by the database, not only in Go", fg.name)
			}
		})
	}

	// The honest row is still accepted — the constraints reject forgeries, not the truth.
	if err := insertLedgerRow(ctx, pool, honest); err != nil {
		t.Fatalf("PostgreSQL rejected the HONEST provenance row: %v (over-tightened)", err)
	}
}

// TestBulkActionBindings_AppendOnly asserts the §4.6 append-only posture on the
// ledger. It is audit-class: a binding records that an authorization HAPPENED, so
// rewriting or deleting it would let a later replay be re-pointed at a different
// selection — precisely the forgery prior finding 1 describes.
func TestBulkActionBindings_AppendOnly(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	f := seedBindingFixture(t, pool, q, svc, "ledger-append-only")

	if err := insertLedgerRow(ctx, pool, db.BulkActionBinding{
		SelectionSetMemberID:  f.member.ID,
		SelectionSetID:        f.set.ID,
		SelectionSetLineageID: f.set.LineageID,
		SelectionSetVersion:   f.set.Version,
		MarketplaceAccountID:  f.account,
		VariantID:             f.variant,
		RecommendationID:      f.card.RecommendationID,
		OfferIdentity:         f.offerName,
		CardID:                f.card.ID,
		ActionID:              f.card.ActionID,
	}); err != nil {
		t.Fatalf("seed honest binding: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE bulk_action_bindings SET offer_identity = 'rewritten' WHERE selection_set_id = $1`, f.set.ID,
	); err == nil {
		t.Fatal("UPDATE on bulk_action_bindings was ACCEPTED; the ledger is audit-class and append-only (§4.6)")
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM bulk_action_bindings WHERE selection_set_id = $1`, f.set.ID,
	); err == nil {
		t.Fatal("DELETE on bulk_action_bindings was ACCEPTED; the ledger is audit-class and append-only (§4.6)")
	}
}

// TestConfirmBulkSelection_IndividuallyApprovedCardIsNotAlreadyAuthorizedForASelection
// is prior finding 1's first half. A card approved through the INDIVIDUAL §8.4 confirm
// carries no bulk provenance at all. A later bulk confirmation whose bound version
// contains that member must NOT report `already_authorized` — that would claim this
// selection authorized something it never did. It fails closed as `invalidated`: the
// card is not a bindable control for THIS selection, and it is not retriable into
// execution.
func TestConfirmBulkSelection_IndividuallyApprovedCardIsNotAlreadyAuthorizedForASelection(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "finding1-individual")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "finding1", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// The card is approved OUTSIDE the selection, through the individual path.
	if _, err := svc.ConfirmIndividual(ctx, card.ID, bindingOf(t, card), time.Now().UTC(), testActor()); err != nil {
		t.Fatalf("individual confirm: %v", err)
	}

	out, err := svc.ConfirmBulkSelection(ctx, account, res.Set.LineageID, res.Set.Version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("bulk confirm after an individual approval: %v", err)
	}
	if !out.Valid || len(out.Items) != 1 {
		t.Fatalf("outcome: %+v; want one item", out)
	}
	if got := out.Items[0].State; got != recommendation.BulkItemInvalidated {
		t.Fatalf("item state %q for a card this selection never authorized; want %q "+
			"(already_authorized requires MATCHING durable provenance — prior finding 1)",
			got, recommendation.BulkItemInvalidated)
	}
	// No ledger row was fabricated for a selection that authorized nothing.
	if n := countBindings(t, pool, res.Set.ID); n != 0 {
		t.Fatalf("selection wrote %d provenance rows without authorizing anything; want 0", n)
	}
}

// TestConfirmBulkSelection_CardApprovedThroughSelectionAIsNotAlreadyAuthorizedForB is
// prior finding 1's second half, and the sharper case: BOTH selections are real bulk
// selections over the SAME member. Selection A authorizes it and writes provenance;
// selection B, which authorized nothing, must not inherit A's authorization.
func TestConfirmBulkSelection_CardApprovedThroughSelectionAIsNotAlreadyAuthorizedForB(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "finding1-selection-a")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)
	member := []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}}

	// Two INDEPENDENT lineages over the same member (design record (d): their version
	// counters are not comparable, and nothing below orders or diffs them).
	setA, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "selection-a", nil, member)
	if err != nil {
		t.Fatalf("preview A: %v", err)
	}
	setB, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "selection-b", nil, member)
	if err != nil {
		t.Fatalf("preview B: %v", err)
	}

	outA, err := svc.ConfirmBulkSelection(ctx, account, setA.Set.LineageID, setA.Set.Version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm A: %v", err)
	}
	if outA.Items[0].State != recommendation.BulkItemAuthorized {
		t.Fatalf("selection A item state %q; want authorized", outA.Items[0].State)
	}
	if n := countBindings(t, pool, setA.Set.ID); n != 1 {
		t.Fatalf("selection A wrote %d provenance rows; want exactly 1", n)
	}

	outB, err := svc.ConfirmBulkSelection(ctx, account, setB.Set.LineageID, setB.Set.Version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm B: %v", err)
	}
	if got := outB.Items[0].State; got != recommendation.BulkItemInvalidated {
		t.Fatalf("selection B item state %q; want %q — B authorized nothing and must not "+
			"inherit A's authorization (prior finding 1)", got, recommendation.BulkItemInvalidated)
	}
	if n := countBindings(t, pool, setB.Set.ID); n != 0 {
		t.Fatalf("selection B wrote %d provenance rows without authorizing anything; want 0", n)
	}
}

// TestConfirmBulkSelection_ReplayCollapsesToOneAuthorizationAndOneIntent is the
// idempotency invariant (§4.6, design record (c)) now that provenance gates the
// replay: re-confirming the SAME (lineage, version) reports `already_authorized`
// because THIS selection's durable binding matches — and produces exactly ONE
// authorization, ONE execution intent, and ONE ledger row.
func TestConfirmBulkSelection_ReplayCollapsesToOneAuthorizationAndOneIntent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))
	account, variant := seedVariant(t, q)

	obs := seedEvidenceOffer(t, pool, q, account, variant, "replay-offer")
	card := awaitingCardWithEvidence(t, svc, account, variant, obs)
	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "replay", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	first, err := svc.ConfirmBulkSelection(ctx, account, res.Set.LineageID, res.Set.Version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if first.Items[0].State != recommendation.BulkItemAuthorized {
		t.Fatalf("first confirm item state %q; want authorized", first.Items[0].State)
	}

	replay, err := svc.ConfirmBulkSelection(ctx, account, res.Set.LineageID, res.Set.Version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("replay confirm: %v", err)
	}
	if got := replay.Items[0].State; got != recommendation.BulkItemAlreadyAuthorized {
		t.Fatalf("replay item state %q; want already_authorized (matching durable provenance)", got)
	}
	// The replay carries the SAME sealed offer identity (criterion D across a resume).
	if got := replay.Items[0].OfferIdentity; got != "replay-offer" {
		t.Fatalf("replay item offer identity %q; want %q", got, "replay-offer")
	}
	if n := countBindings(t, pool, res.Set.ID); n != 1 {
		t.Fatalf("replay produced %d provenance rows; want exactly 1", n)
	}
	if n := countIntents(t, pool, card.ID); n != 1 {
		t.Fatalf("replay produced %d execution intents; want exactly 1", n)
	}
	if got := reloadState(t, svc, card.ID); got != approval.StateApproved {
		t.Fatalf("card state after replay %q; want approved", got)
	}
}

func countBindings(t *testing.T, pool *pgxpool.Pool, setID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM bulk_action_bindings WHERE selection_set_id = $1`, setID).Scan(&n); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	return n
}
