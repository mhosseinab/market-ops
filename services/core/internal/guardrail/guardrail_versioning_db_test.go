package guardrail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

func ownerActor() audit.Actor {
	return audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}
}

// TestGuardrailFirstWriteStartsAtVersionOne proves a fresh account's first write
// begins the optimistic-concurrency chain at version 1 (issue #101).
func TestGuardrailFirstWriteStartsAtVersionOne(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	svc := guardrail.NewService(pool)

	view, err := svc.Set(context.Background(), account, ownerActor(), guardrail.Settings{
		ContributionFloor: mustMoney(t, 1000, "USD", -2),
		MovementCapBp:     500,
		CooldownSeconds:   3600,
		Strategy:          policy.StrategyMatch,
		StrategyEnabled:   true,
	}, 0)
	if err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if view.Version != 1 {
		t.Fatalf("first write version = %d, want 1", view.Version)
	}
}

// TestGuardrailTwoOwnersStaleVersionConflict is the issue #101 acceptance at the
// real DB seam: two Owners read the SAME version; the first write wins and bumps
// the version; the second, still holding the stale version, gets a SAFE conflict
// (ErrVersionConflict) — never a lost update. The first Owner's values remain.
func TestGuardrailTwoOwnersStaleVersionConflict(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	svc := guardrail.NewService(pool)
	ctx := context.Background()

	base := guardrail.Settings{
		ContributionFloor: mustMoney(t, 1000, "USD", -2),
		MovementCapBp:     500,
		CooldownSeconds:   3600,
		Strategy:          policy.StrategyMatch,
		StrategyEnabled:   true,
	}
	first, err := svc.Set(ctx, account, ownerActor(), base, 0)
	if err != nil {
		t.Fatalf("seed Set: %v", err)
	}
	// Both Owners now hold version == first.Version (1).
	shared := first.Version

	// Owner A tightens the cap (500 -> 300) and wins.
	winner := base
	winner.MovementCapBp = 300
	won, err := svc.Set(ctx, account, ownerActor(), winner, shared)
	if err != nil {
		t.Fatalf("Owner A write: %v", err)
	}
	if won.Version != shared+1 {
		t.Fatalf("winner version = %d, want %d", won.Version, shared+1)
	}

	// Owner B, still on the stale shared version, also tightens (500 -> 250) but
	// must be REJECTED as a conflict — the write it based on is gone.
	loser := base
	loser.MovementCapBp = 250
	if _, err := svc.Set(ctx, account, ownerActor(), loser, shared); !errors.Is(err, guardrail.ErrVersionConflict) {
		t.Fatalf("stale Owner B write err = %v, want ErrVersionConflict", err)
	}

	// Owner A's value survives; Owner B's stale write left nothing behind.
	got, err := svc.Get(ctx, account)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Settings.MovementCapBp != 300 {
		t.Fatalf("persisted cap = %d, want 300 (Owner A's winning write, not B's lost update)", got.Settings.MovementCapBp)
	}
	if got.Version != shared+1 {
		t.Fatalf("persisted version = %d, want %d (B's conflict must not bump it)", got.Version, shared+1)
	}
}

// TestGuardrailSetRejectsLooseningAgainstBaseline proves the server enforces
// stricter-only against the AUTHORITATIVE persisted baseline (PRC-004 / §8.3):
// a write that loosens the cap is rejected and nothing is mutated.
func TestGuardrailSetRejectsLooseningAgainstBaseline(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	svc := guardrail.NewService(pool)
	ctx := context.Background()

	base := guardrail.Settings{
		ContributionFloor: mustMoney(t, 1000, "USD", -2),
		MovementCapBp:     300,
		CooldownSeconds:   3600,
		Strategy:          policy.StrategyMatch,
		StrategyEnabled:   true,
	}
	v1, err := svc.Set(ctx, account, ownerActor(), base, 0)
	if err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	// Attempt to LOOSEN the cap (300 -> 450) at the correct version.
	loose := base
	loose.MovementCapBp = 450
	if _, err := svc.Set(ctx, account, ownerActor(), loose, v1.Version); !errors.Is(err, guardrail.ErrNotStricter) {
		t.Fatalf("loosening write err = %v, want ErrNotStricter", err)
	}
	// Nothing changed: value and version are intact (fail closed).
	got, err := svc.Get(ctx, account)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Settings.MovementCapBp != 300 || got.Version != v1.Version {
		t.Fatalf("rejected loosening mutated state: cap=%d version=%d, want cap=300 version=%d", got.Settings.MovementCapBp, got.Version, v1.Version)
	}
}
