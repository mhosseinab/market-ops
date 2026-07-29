package execution

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
	"github.com/mhosseinab/market-ops/services/core/internal/reservation"
)

// The RELEASE half of BULK-PROTOCOL DESIGN RECORD (a) (issue #87, prior finding 2):
// the durable (account, variant) execution reservation is RELEASED ON A TERMINAL
// EXTERNAL RESULT — and, critically, NOT on an unknown one.
//
// `pending_reconciliation` is EXE-003's fail-closed state for an UNKNOWN outcome.
// Releasing on it would infer "no write is in flight" from "we do not know whether the
// write landed", and admit a second concurrent write to a variant whose first write may
// already have been accepted by the marketplace. Quarantine over inference (§4.6): it
// is released only when reconciliation RESOLVES the unknown into a definite result.
//
// The negative case (an unknown result does NOT release) comes first.

// holdReservation acquires the reservation for a card exactly as the §8.4 confirmation
// does (ACQUIRED BEFORE DISPATCH). These fixtures advance cards through raw Advance
// rather than ConfirmIndividual, so the reservation is taken explicitly here.
func holdReservation(t *testing.T, pool *pgxpool.Pool, card db.ApprovalCard) (account, variant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	row, err := db.New(pool).GetVariantForCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("resolve variant for card: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := reservation.Acquire(ctx, db.New(tx), reservation.Request{
		Account: row.MarketplaceAccountID, Variant: row.VariantID,
		CardID: card.ID, ActionID: card.ActionID, Now: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("acquire reservation: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return row.MarketplaceAccountID, row.VariantID
}

func reservationState(t *testing.T, pool *pgxpool.Pool, account, variant uuid.UUID) (bool, string) {
	t.Helper()
	var released bool
	var reason string
	if err := pool.QueryRow(context.Background(), `
		SELECT released_at IS NOT NULL, release_reason
		  FROM execution_variant_reservations
		 WHERE marketplace_account_id = $1 AND variant_id = $2`, account, variant).Scan(&released, &reason); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	return released, reason
}

// TestExecute_UnknownExternalResultDoesNotReleaseTheVariant is the fail-closed rule.
// A write whose outcome could not be determined parks in pending_reconciliation; the
// variant STAYS reserved, so no second card can write over a change that may already
// have landed.
func TestExecute_UnknownExternalResultDoesNotReleaseTheVariant(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	account, variant := holdReservation(t, pool, card)

	svc := NewService(pool, recommendation.NewService(pool),
		unknownWriter{}, fakeResolver{ctx: enabledContext(card, native)})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.ExternalState != StatePendingReconciliation {
		t.Fatalf("external state = %q; want pending_reconciliation (the unknown outcome)", res.ExternalState)
	}
	released, reason := reservationState(t, pool, account, variant)
	if released {
		t.Fatalf("reservation was RELEASED on an UNKNOWN result (reason %q); an unknown outcome "+
			"is never inferred as settled (EXE-003, §4.6 quarantine over inference)", reason)
	}
}

// TestExecute_DefiniteExternalResultReleasesTheVariant is the positive half: a write
// that produced a DEFINITE result releases the variant, so the next authorization on it
// can proceed. Without this the guard would be an over-tightening that strands every
// variant after one execution.
func TestExecute_DefiniteExternalResultReleasesTheVariant(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	account, variant := holdReservation(t, pool, card)

	srv, _ := countingMockDK(t)
	svc := NewService(pool, recommendation.NewService(pool),
		NewHTTPWriter(srv.URL, "tok", srv.Client()), fakeResolver{ctx: enabledContext(card, native)})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.ExternalState != StateAccepted {
		t.Fatalf("external state = %q; want accepted", res.ExternalState)
	}
	released, reason := reservationState(t, pool, account, variant)
	if !released {
		t.Fatal("reservation was NOT released on a DEFINITE external result; the variant would be " +
			"stranded and no later approval on it could ever execute")
	}
	if reason != reservation.ReasonAccepted {
		t.Fatalf("release reason %q; want %q", reason, reservation.ReasonAccepted)
	}
}

// TestExecute_RecommendOnlyReleasesTheVariant: with writes OFF (the P0 dark default,
// §20.2), no external write exists at all, so nothing can be in flight and the variant
// must not stay reserved.
func TestExecute_RecommendOnlyReleasesTheVariant(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	account, variant := holdReservation(t, pool, card)

	rc := enabledContext(card, native)
	rc.Enablement = WriteEnablement{} // default OFF (both keys false).
	rc.VariantID = variant

	srv, writes := countingMockDK(t)
	svc := NewService(pool, recommendation.NewService(pool),
		NewHTTPWriter(srv.URL, "tok", srv.Client()), fakeResolver{ctx: rc})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Mode != ModeRecommendOnly {
		t.Fatalf("mode = %q; want recommend_only (writes are OFF by default)", res.Mode)
	}
	if got := atomic.LoadInt32(writes); got != 0 {
		t.Fatalf("recommend-only performed %d external writes; want 0", got)
	}
	released, reason := reservationState(t, pool, account, variant)
	if !released {
		t.Fatal("reservation was NOT released for a recommend-only action; no external write exists to be in flight")
	}
	if reason != reservation.ReasonRecommendOnly {
		t.Fatalf("release reason %q; want %q", reason, reservation.ReasonRecommendOnly)
	}
}

// TestExecute_GateBlockedCardDoesNotStrandItsVariant is FIX-CYCLE-1 FINDING F3
// (BULK-PROTOCOL DESIGN RECORD (a): the release seam was incomplete).
//
// A gate block at Revalidating drives the card to the TERMINAL Invalidated state and
// the code's own comment records that NO WRITE OCCURRED. Nothing was ever in flight, so
// there is nothing for the reservation to protect — yet the only release call sites were
// commitWriteResult and recordRecommendOnly, so this path held the (account, variant)
// for the full 30-minute reservation.Window and every subsequent approval on that
// variant failed for half an hour. That is precisely the harm this branch cites when it
// justifies the recommend-only release, applied inconsistently, and it degrades the
// highest-priority path in the load-shedding order (approval > audit > reconciliation).
func TestExecute_GateBlockedCardDoesNotStrandItsVariant(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	account, variant := holdReservation(t, pool, card)

	rc := enabledContext(card, native)
	// A revoked permission fails the §8.4 revalidation gate BEFORE any write.
	rc.Inputs.PermissionGranted = false

	srv, writes := countingMockDK(t)
	svc := NewService(pool, recommendation.NewService(pool),
		NewHTTPWriter(srv.URL, "tok", srv.Client()), fakeResolver{ctx: rc})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Blocked {
		t.Fatalf("execute result: %+v; want a gate-blocked result", res)
	}
	if got := atomic.LoadInt32(writes); got != 0 {
		t.Fatalf("a gate-blocked execution performed %d external writes; want 0 (this is the whole premise of the release)", got)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM approval_cards WHERE id = $1`, card.ID).Scan(&state); err != nil {
		t.Fatalf("read card state: %v", err)
	}
	if state != "invalidated" {
		t.Fatalf("card state = %q; want invalidated (the legal §8.4 Revalidating → Invalidated edge)", state)
	}

	released, reason := reservationState(t, pool, account, variant)
	if !released {
		t.Fatal("a TERMINALLY Invalidated card that provably never wrote STRANDED its variant for the full " +
			"reservation window; every later approval on that variant fails until it lapses")
	}
	if reason != reservation.ReasonGateBlocked {
		t.Fatalf("release reason %q; want %q (a stable, non-localized key naming the failing seam)", reason, reservation.ReasonGateBlocked)
	}
}

// TestExecute_GateBlockedRecommendOnlyCardDoesNotStrandItsVariant is FINDING F3 on the
// sibling branch. In recommend-only mode (the P0 dark default) no external write exists
// at all, so a gate block there strands the variant for exactly the same reason and with
// no possible justification.
func TestExecute_GateBlockedRecommendOnlyCardDoesNotStrandItsVariant(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	account, variant := holdReservation(t, pool, card)

	rc := enabledContext(card, native)
	rc.Enablement = WriteEnablement{} // writes OFF ⇒ recommend-only.
	rc.VariantID = variant
	rc.Inputs.PermissionGranted = false

	srv, writes := countingMockDK(t)
	svc := NewService(pool, recommendation.NewService(pool),
		NewHTTPWriter(srv.URL, "tok", srv.Client()), fakeResolver{ctx: rc})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Blocked || res.Mode != ModeRecommendOnly {
		t.Fatalf("execute result: %+v; want a gate-blocked recommend-only result", res)
	}
	if got := atomic.LoadInt32(writes); got != 0 {
		t.Fatalf("recommend-only gate block performed %d external writes; want 0", got)
	}
	released, reason := reservationState(t, pool, account, variant)
	if !released {
		t.Fatal("a gate-blocked recommend-only card STRANDED its variant; no external write exists to protect")
	}
	if reason != reservation.ReasonGateBlocked {
		t.Fatalf("release reason %q; want %q", reason, reservation.ReasonGateBlocked)
	}
}
