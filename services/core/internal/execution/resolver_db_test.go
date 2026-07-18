package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// TestDefaultResolver_FailsClosedWhenWriteEnabledButSignalsStatic is the R5
// guard: if write enablement is turned on (capability Supported AND region
// verified) while the external gate signals are still static placeholders, the
// resolver REFUSES to hand back a write-enabled context — a fail-closed guard
// against an EXE-001 bypass. Once WithLiveSignals asserts the signals are live,
// the write-enabled context is returned.
func TestDefaultResolver_FailsClosedWhenWriteEnabledButSignalsStatic(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)

	// Turn on the region write-verification flag (S35 would do this).
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
	// Capability Supported.
	cap := func(context.Context, uuid.UUID) (bool, error) { return true, nil }

	// Signals still static ⇒ fail closed.
	staticResolver := NewDefaultResolver(pool, cap)
	if _, err := staticResolver.Resolve(ctx, card); !errors.Is(err, ErrSignalsStatic) {
		t.Fatalf("write-enabled + static signals: want ErrSignalsStatic, got %v", err)
	}

	// Once the signals are asserted live (S35), a write-enabled context is returned.
	liveResolver := NewDefaultResolver(pool, cap).WithLiveSignals()
	rc, err := liveResolver.Resolve(ctx, card)
	if err != nil {
		t.Fatalf("live resolver: %v", err)
	}
	if !rc.Enablement.CanWrite() {
		t.Fatalf("live resolver should report write-enabled")
	}
}

// TestDefaultResolver_DarkByDefault proves the default production resolver reports
// write enablement OFF (recommend-only) even with the capability Supported, because
// the region flag is absent — and therefore never trips the static-signal guard.
func TestDefaultResolver_DarkByDefault(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)

	res := NewDefaultResolver(pool, func(context.Context, uuid.UUID) (bool, error) { return true, nil })
	rc, err := res.Resolve(ctx, card)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rc.Enablement.CanWrite() {
		t.Fatalf("default resolver must be dark (region unverified)")
	}
}
