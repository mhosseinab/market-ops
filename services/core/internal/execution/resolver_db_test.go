package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// TestDefaultResolver_DarkByDefault proves the default production resolver has NO
// live signal sources, so it FAILS CLOSED (ErrSignalsStatic) rather than
// fabricating the external gate signals or invalidating a card on static truth.
// This is the strengthened dark-by-default invariant: recommend-only and write
// alike refuse to run on non-live signals (EXE-001).
func TestDefaultResolver_DarkByDefault(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)

	res := NewDefaultResolver(pool, func(context.Context, uuid.UUID) (bool, error) { return true, nil })
	if _, err := res.Resolve(ctx, card); !errors.Is(err, ErrSignalsStatic) {
		t.Fatalf("dark resolver must fail closed (ErrSignalsStatic), got %v", err)
	}
}

// TestDefaultResolver_FailsClosedWhenWriteEnabledButStatic is the write-enablement
// guard: even with capability Supported AND the region write-verification flag ON,
// a resolver with no live signal sources REFUSES to authorize (ErrSignalsStatic) —
// write enablement remains impossible with any static/default signal. Once the
// resolver is constructed LIVE with authoritative sources, a write-enabled context
// is returned and every gate passes.
func TestDefaultResolver_FailsClosedWhenWriteEnabledButStatic(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)

	if _, err := q.UpsertWriteVerification(ctx, db.UpsertWriteVerificationParams{
		MarketplaceAccountID:     card.MarketplaceAccountID,
		RegionCode:               "IR",
		Verified:                 true,
		ParameterContractVersion: 1,
		VerifiedAt:               pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		Note:                     "test",
	}); err != nil {
		t.Fatalf("upsert write verification: %v", err)
	}
	cap := func(context.Context, uuid.UUID) (bool, error) { return true, nil }

	// No live sources ⇒ fail closed even though write enablement is on.
	if _, err := NewDefaultResolver(pool, cap).Resolve(ctx, card); !errors.Is(err, ErrSignalsStatic) {
		t.Fatalf("write-enabled + static signals: want ErrSignalsStatic, got %v", err)
	}

	// Constructed LIVE (all authoritative sources present) with the identity
	// confirmed ⇒ a write-enabled context is returned and every gate passes.
	seedConfirmedIdentity(t, pool, q, card)
	live, err := NewLiveResolver(pool, cap, SignalSources{
		Identity:   NewDBIdentitySignal(pool),
		Price:      passingSignals(),
		MoneyUnit:  passingSignals(),
		Boundary:   passingSignals(),
		Permission: passingSignals(),
		Evidence:   passingSignals(),
	})
	if err != nil {
		t.Fatalf("NewLiveResolver: %v", err)
	}
	rc, err := live.Resolve(ctx, card)
	if err != nil {
		t.Fatalf("live resolve: %v", err)
	}
	if !rc.Enablement.CanWrite() {
		t.Fatalf("live resolver should report write-enabled")
	}
	if out := EvaluateGates(rc.Inputs); !out.OK {
		t.Fatalf("confirmed identity + passing signals should pass; blocked %s", out.Failed)
	}
}

// TestDBIdentitySignal_ReopenBlocks is the FIRST-failing repro (§16 reopen): a
// variant whose Confirmed identity has been reopened must block the identity gate.
// The signal is resolved LIVE from market_product_identities, so a reopened mapping
// (no active Confirmed row) flips IdentityConfirmed to false and EvaluateGates
// blocks GateIdentity — no fabricated positive survives.
func TestDBIdentitySignal_ReopenBlocks(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)
	variantID := seedConfirmedIdentity(t, pool, q, card)

	sig := NewDBIdentitySignal(pool)
	confirmed, err := sig.IdentityConfirmed(ctx, variantID)
	if err != nil {
		t.Fatalf("identity confirmed: %v", err)
	}
	if !confirmed {
		t.Fatalf("freshly confirmed identity should report confirmed")
	}

	// Reopen the mapping (§16): it leaves the executable Confirmed+active set.
	ident, err := q.GetActiveConfirmedIdentityForVariant(ctx, variantID)
	if err != nil {
		t.Fatalf("get identity: %v", err)
	}
	if _, err := q.ReopenConfirmedIdentity(ctx, db.ReopenConfirmedIdentityParams{
		ID: ident.ID, State: "needs_review",
	}); err != nil {
		t.Fatalf("reopen identity: %v", err)
	}

	reopened, err := sig.IdentityConfirmed(ctx, variantID)
	if err != nil {
		t.Fatalf("identity confirmed after reopen: %v", err)
	}
	if reopened {
		t.Fatalf("reopened identity must report NOT confirmed (fail closed)")
	}

	// End-to-end through the live resolver: the identity gate blocks.
	live, err := NewLiveResolver(pool, nil, SignalSources{
		Identity:   sig,
		Price:      passingSignals(),
		MoneyUnit:  passingSignals(),
		Boundary:   passingSignals(),
		Permission: passingSignals(),
		Evidence:   passingSignals(),
	})
	if err != nil {
		t.Fatalf("NewLiveResolver: %v", err)
	}
	rc, err := live.Resolve(ctx, card)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out := EvaluateGates(rc.Inputs); out.OK || out.Failed != GateIdentity {
		t.Fatalf("reopened identity must block GateIdentity, got OK=%v failed=%s", out.OK, out.Failed)
	}
}

// seedConfirmedIdentity inserts an active Confirmed market-product identity for the
// card's variant and returns the variant id.
func seedConfirmedIdentity(t *testing.T, pool *pgxpool.Pool, q *db.Queries, card db.ApprovalCard) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	rc, err := q.GetCurrentExecutionContext(ctx, card.RecommendationID)
	if err != nil {
		t.Fatalf("current execution context: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO market_product_identities (
			marketplace_account_id, variant_id, native_variant_id, native_product_id,
			state, active)
		VALUES ($1, $2, $3, $3, 'confirmed', true)`,
		card.MarketplaceAccountID, rc.VariantID, rc.NativeVariantID); err != nil {
		t.Fatalf("seed confirmed identity: %v", err)
	}
	return rc.VariantID
}
