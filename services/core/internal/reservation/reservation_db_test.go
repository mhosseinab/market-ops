package reservation_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/reservation"
)

// BULK-PROTOCOL DESIGN RECORD (a) — DURABLE (account, variant) EXECUTION RESERVATION
// (issue #87, prior finding 2; PRD §7.5 EXE-002/EXE-003, §4.6 idempotency +
// reconciliation).
//
// The defect: one-executable-per-variant was only REQUEST-LOCAL. Two separate
// one-member selection lineages for sibling offers could each enqueue an action for
// the SAME owned variant, because the guards that exist bound one CARD (the
// FROM-guarded advance, the card-id-unique execution intent) and nothing bounded two
// CARDS on one variant.
//
// The record pins three things and none may be weakened: keyed on (account, variant),
// ACQUIRED BEFORE DISPATCH, RELEASED ON A TERMINAL EXTERNAL RESULT. This package owns
// the acquire / release / expiry semantics.
//
// Negative tests first (§4.6 posture).

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping reservation DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// fixture provisions one account with ONE variant and TWO independent approval cards
// on it — the exact shape prior finding 2 describes: two authorizations racing for a
// single owned variant.
type fixture struct {
	account uuid.UUID
	variant uuid.UUID
	cardA   uuid.UUID
	actionA uuid.UUID
	cardB   uuid.UUID
	actionB uuid.UUID
}

func seedFixture(t *testing.T, pool *pgxpool.Pool, q *db.Queries) fixture {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "resv-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Reservation Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID, NativeProductID: nativeProduct, Title: "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID, ProductID: prod.ID,
		NativeVariantID: nativeVariant, NativeProductID: nativeProduct,
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	f := fixture{account: acct.ID, variant: v.ID}
	f.cardA, f.actionA = seedCard(t, pool, acct.ID, v.ID)
	f.cardB, f.actionB = seedCard(t, pool, acct.ID, v.ID)
	return f
}

// seedCard writes a minimal recommendation + approval card directly, so this package's
// tests exercise the reservation seam WITHOUT depending on the recommendation service.
func seedCard(t *testing.T, pool *pgxpool.Pool, account, variant uuid.UUID) (card, action uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var recID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO recommendations (
			marketplace_account_id, variant_id, lineage_id, version, objective,
			current_price_mantissa, current_price_currency, current_price_exponent,
			readiness, evidence_quality)
		VALUES ($1,$2,$3,1,'maximize_contribution',1000,'IRR',0,'complete','verified')
		RETURNING id`, account, variant, uuid.New()).Scan(&recID); err != nil {
		t.Fatalf("insert recommendation: %v", err)
	}
	action = uuid.New()
	if err := pool.QueryRow(ctx, `
		INSERT INTO approval_cards (
			recommendation_id, marketplace_account_id, lineage_id, version, state,
			action_id, parameter_version, context_version, policy_version,
			cost_profile_version, idempotency_key, price_mantissa, price_currency,
			price_exponent, expires_at)
		VALUES ($1,$2,$3,1,'approved',$4,1,1,1,1,$5,1000,'IRR',0, now() + interval '1 hour')
		RETURNING id`, recID, account, uuid.New(), action, "idem-"+uuid.NewString()).Scan(&card); err != nil {
		t.Fatalf("insert approval card: %v", err)
	}
	return card, action
}

// TestAcquire_SecondCardOnTheSameVariantIsRejected is prior finding 2 itself: two
// DIFFERENT cards — the sibling-offer / two-selection-lineages case — must not both
// hold an in-flight write on one owned variant. The second acquire fails closed with
// ErrVariantReserved; it never silently succeeds and never displaces the live holder.
func TestAcquire_SecondCardOnTheSameVariantIsRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	f := seedFixture(t, pool, q)
	now := time.Now().UTC()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Acquire(ctx, db.New(tx), reservation.Request{
		Account: f.account, Variant: f.variant, CardID: f.cardA, ActionID: f.actionA, Now: now,
	}); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin 2: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	err = reservation.Acquire(ctx, db.New(tx2), reservation.Request{
		Account: f.account, Variant: f.variant, CardID: f.cardB, ActionID: f.actionB, Now: now,
	})
	if !errors.Is(err, reservation.ErrVariantReserved) {
		t.Fatalf("second card on the same variant: err=%v; want ErrVariantReserved (fail closed)", err)
	}

	// The LIVE holder is untouched: a rejected acquire never displaces a live write.
	held, err := q.GetVariantReservation(ctx, db.GetVariantReservationParams{
		MarketplaceAccountID: f.account, VariantID: f.variant,
	})
	if err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if held.CardID != f.cardA || held.ReleasedAt.Valid {
		t.Fatalf("holder after a rejected acquire: card=%s released=%v; want card A, still live", held.CardID, held.ReleasedAt.Valid)
	}
}

// TestAcquire_SameCardIsIdempotent: a replayed confirmation of ONE card must not
// deadlock against its own live reservation. Idempotency is a never-cut invariant
// (§4.6): a retry with a stable key is a normal path, not a conflict.
func TestAcquire_SameCardIsIdempotent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	f := seedFixture(t, pool, q)
	now := time.Now().UTC()
	req := reservation.Request{Account: f.account, Variant: f.variant, CardID: f.cardA, ActionID: f.actionA, Now: now}

	for i := range 3 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin %d: %v", i, err)
		}
		if err := reservation.Acquire(ctx, db.New(tx), req); err != nil {
			t.Fatalf("acquire %d by the SAME card: %v; want idempotent success", i, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	// Exactly one reservation row; the append-only ledger records each acquire.
	events, err := q.ListReservationEventsForVariant(ctx, db.ListReservationEventsForVariantParams{
		MarketplaceAccountID: f.account, VariantID: f.variant,
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("reservation events = %d; want 3 append-only acquires", len(events))
	}
	for _, e := range events {
		if e.EventType != "acquired" {
			t.Fatalf("event type %q; want acquired", e.EventType)
		}
	}
}

// TestRelease_OnlyOnADefiniteExternalResult is the FAIL-CLOSED rule (§4.6 quarantine
// over inference). `pending_reconciliation` is EXE-003's state for an UNKNOWN outcome:
// releasing on it would infer "no write is in flight" from "we do not know whether the
// write landed", and let a second write hit a variant whose first write may already
// have been accepted by the marketplace. Only a DEFINITE result releases.
func TestRelease_OnlyOnADefiniteExternalResult(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	f := seedFixture(t, pool, q)
	now := time.Now().UTC()

	acquire := func(card, action uuid.UUID) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := reservation.Acquire(ctx, db.New(tx), reservation.Request{
			Account: f.account, Variant: f.variant, CardID: card, ActionID: action, Now: now,
		}); err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	acquire(f.cardA, f.actionA)

	// An UNKNOWN result does NOT release: the variant stays reserved and card B is
	// still refused.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Release(ctx, db.New(tx), reservation.ReleaseRequest{
		Account: f.account, Variant: f.variant, CardID: f.cardA,
		Reason: reservation.ReasonPendingReconciliation, Now: now,
	}); !errors.Is(err, reservation.ErrNotReleasable) {
		t.Fatalf("release on pending_reconciliation: err=%v; want ErrNotReleasable (unknown is never inferred as settled)", err)
	}
	_ = tx.Rollback(ctx)

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	if err := reservation.Acquire(ctx, db.New(tx2), reservation.Request{
		Account: f.account, Variant: f.variant, CardID: f.cardB, ActionID: f.actionB, Now: now,
	}); !errors.Is(err, reservation.ErrVariantReserved) {
		t.Fatalf("acquire while the first write's outcome is UNKNOWN: err=%v; want ErrVariantReserved", err)
	}
	_ = tx2.Rollback(ctx)

	// A DEFINITE result releases, and only then may another card acquire.
	tx3, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Release(ctx, db.New(tx3), reservation.ReleaseRequest{
		Account: f.account, Variant: f.variant, CardID: f.cardA,
		Reason: reservation.ReasonAccepted, Now: now,
	}); err != nil {
		t.Fatalf("release on a definite result: %v", err)
	}
	if err := tx3.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx4, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Acquire(ctx, db.New(tx4), reservation.Request{
		Account: f.account, Variant: f.variant, CardID: f.cardB, ActionID: f.actionB, Now: now,
	}); err != nil {
		t.Fatalf("acquire after a definite release: %v; want success", err)
	}
	if err := tx4.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	_ = q
}

// TestRelease_ByANonHolderIsRefused: a late release from a DISPLACED holder must never
// free a reservation a different card has since acquired.
func TestRelease_ByANonHolderIsRefused(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	f := seedFixture(t, pool, q)
	now := time.Now().UTC()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Acquire(ctx, db.New(tx), reservation.Request{
		Account: f.account, Variant: f.variant, CardID: f.cardA, ActionID: f.actionA, Now: now,
	}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	if err := reservation.Release(ctx, db.New(tx2), reservation.ReleaseRequest{
		Account: f.account, Variant: f.variant, CardID: f.cardB,
		Reason: reservation.ReasonAccepted, Now: now,
	}); !errors.Is(err, reservation.ErrNotReleasable) {
		t.Fatalf("release by a NON-holder: err=%v; want ErrNotReleasable", err)
	}
}

// TestAcquire_ExpiredHolderIsTakenOverWithAnAuditedEvent: the expiry window is a
// bounded liveness guard, not a silent recovery. A lapsed holder MAY be displaced —
// otherwise a crashed writer would strand a variant forever — but the takeover appends
// an append-only `expired_takeover` event NAMING the displaced holder, so it is
// observable and attributable (§4.6: a fallback engaging without an emitted, audited
// event is always a bug).
func TestAcquire_ExpiredHolderIsTakenOverWithAnAuditedEvent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	f := seedFixture(t, pool, q)
	acquiredAt := time.Now().UTC().Add(-2 * reservation.Window)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Acquire(ctx, db.New(tx), reservation.Request{
		Account: f.account, Variant: f.variant, CardID: f.cardA, ActionID: f.actionA, Now: acquiredAt,
	}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	now := time.Now().UTC()
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Acquire(ctx, db.New(tx2), reservation.Request{
		Account: f.account, Variant: f.variant, CardID: f.cardB, ActionID: f.actionB, Now: now,
	}); err != nil {
		t.Fatalf("takeover of a LAPSED holder: %v; want success", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	events, err := q.ListReservationEventsForVariant(ctx, db.ListReservationEventsForVariantParams{
		MarketplaceAccountID: f.account, VariantID: f.variant,
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d; want 2 (acquire + audited takeover)", len(events))
	}
	last := events[len(events)-1]
	if last.EventType != "expired_takeover" {
		t.Fatalf("takeover event type %q; want expired_takeover (never a silent recovery)", last.EventType)
	}
	if !last.PriorCardID.Valid || uuid.UUID(last.PriorCardID.Bytes) != f.cardA {
		t.Fatalf("takeover event does not name the DISPLACED holder: %+v; want %s", last.PriorCardID, f.cardA)
	}
	if last.CardID != f.cardB {
		t.Fatalf("takeover event holder %s; want the new acquirer %s", last.CardID, f.cardB)
	}
}

// TestReservationEvents_AppendOnly asserts the §4.6 append-only posture on the
// provenance ledger. The mutable projection may have a lifecycle; its HISTORY may not.
func TestReservationEvents_AppendOnly(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	f := seedFixture(t, pool, q)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Acquire(ctx, db.New(tx), reservation.Request{
		Account: f.account, Variant: f.variant, CardID: f.cardA, ActionID: f.actionA, Now: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE execution_reservation_events SET event_type = 'released' WHERE variant_id = $1`, f.variant,
	); err == nil {
		t.Fatal("UPDATE on execution_reservation_events was ACCEPTED; reservation provenance is append-only (§4.6)")
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM execution_reservation_events WHERE variant_id = $1`, f.variant,
	); err == nil {
		t.Fatal("DELETE on execution_reservation_events was ACCEPTED; reservation provenance is append-only (§4.6)")
	}
	_ = q
}
