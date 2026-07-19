package routec_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

// TestDBBudgetConcurrentReserveNeverOvershoots is the issue #48 concurrency
// acceptance: N independent "workers" (separate DBBudget instances over the SAME
// ephemeral DB, simulating separate scheduler processes) race to reserve when
// only K request units exist. An in-process mutex could not stop cross-process
// overshoot; the durable atomic reserve must admit EXACTLY K, never more.
func TestDBBudgetConcurrentReserveNeverOvershoots(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, _, _, _, _ := seedConfirmedTarget(t, pool, q)

	const limitK = 7
	const workers = 64
	clock := func() time.Time { return time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC) }

	var admitted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A SEPARATE store instance per worker: no shared in-process state — the
			// only thing they share is the durable row.
			b := routec.NewDBBudget(pool, limitK, 1<<40, 24*time.Hour, clock)
			<-start
			ok, err := b.Reserve(ctx, account)
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			if ok {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if admitted != limitK {
		t.Fatalf("concurrent reserve overshoot: admitted %d, want exactly %d", admitted, limitK)
	}
	// The durable total must equal the limit — never above it.
	st, err := routec.NewDBBudget(pool, limitK, 1<<40, 24*time.Hour, clock).Snapshot(ctx, account)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if st.RequestsRemaining != 0 {
		t.Fatalf("durable remaining after exhaustion: got %d want 0", st.RequestsRemaining)
	}
	// One more reserve is denied (not an error).
	ok, err := routec.NewDBBudget(pool, limitK, 1<<40, 24*time.Hour, clock).Reserve(ctx, account)
	if err != nil {
		t.Fatalf("post-exhaustion reserve error: %v", err)
	}
	if ok {
		t.Fatal("reserve past the durable ceiling must be denied")
	}
}

// TestDBBudgetConcurrentConsumeNeverLosesBytes proves byte accounting is a
// durable ATOMIC increment: N concurrent Consume calls sum EXACTLY (no lost
// read-modify-write), and once the durable byte total reaches the ceiling the
// atomic reserve's byte predicate denies further requests — so concurrent workers
// cannot collectively overshoot the byte ceiling either.
func TestDBBudgetConcurrentConsumeNeverLosesBytes(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, _, _, _, _ := seedConfirmedTarget(t, pool, q)

	const byteLimit = 1000
	const chunk = 100
	const workers = 10 // 10 * 100 == exactly the ceiling
	clock := func() time.Time { return time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC) }

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := routec.NewDBBudget(pool, 1<<30, byteLimit, 24*time.Hour, clock)
			<-start
			if err := b.Consume(ctx, account, chunk); err != nil {
				t.Errorf("consume: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	st, err := routec.NewDBBudget(pool, 1<<30, byteLimit, 24*time.Hour, clock).Snapshot(ctx, account)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// No lost updates: the durable total is exactly workers*chunk, so remaining is 0.
	if st.BytesRemaining != 0 {
		t.Fatalf("byte accounting lost/duplicated an update: remaining %d want 0", st.BytesRemaining)
	}
	// With bytes AT the ceiling, the reserve byte predicate denies further fetches.
	ok, err := routec.NewDBBudget(pool, 1<<30, byteLimit, 24*time.Hour, clock).Reserve(ctx, account)
	if err != nil {
		t.Fatalf("reserve at byte ceiling: %v", err)
	}
	if ok {
		t.Fatal("reserve must be denied once the durable byte total reaches the ceiling")
	}
}

// TestDBBudgetSurvivesRestart is the issue #48 restart acceptance: a FRESH store
// instance (a new process) over the same durable DB within the same window sees
// the already-consumed budget — the daily total does NOT reset on restart, so a
// restarted worker cannot re-spend a spent ceiling.
func TestDBBudgetSurvivesRestart(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, _, _, _, _ := seedConfirmedTarget(t, pool, q)

	const limit = 3
	clock := func() time.Time { return time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC) }

	// "Process 1" spends two of three units.
	proc1 := routec.NewDBBudget(pool, limit, 1<<40, 24*time.Hour, clock)
	for i := 0; i < 2; i++ {
		ok, err := proc1.Reserve(ctx, account)
		if err != nil || !ok {
			t.Fatalf("proc1 reserve %d: ok=%v err=%v", i, ok, err)
		}
	}

	// "Process 2" is a brand-new store over the same DB, same window. It must see
	// the two already-spent units — NOT a fresh full budget.
	proc2 := routec.NewDBBudget(pool, limit, 1<<40, 24*time.Hour, clock)
	st, err := proc2.Snapshot(ctx, account)
	if err != nil {
		t.Fatalf("proc2 snapshot: %v", err)
	}
	if st.RequestsRemaining != 1 {
		t.Fatalf("restart lost durable spend: remaining %d want 1", st.RequestsRemaining)
	}
	// It may reserve exactly the last unit, then is denied.
	ok, err := proc2.Reserve(ctx, account)
	if err != nil || !ok {
		t.Fatalf("proc2 last-unit reserve: ok=%v err=%v", ok, err)
	}
	ok, err = proc2.Reserve(ctx, account)
	if err != nil {
		t.Fatalf("proc2 over-ceiling reserve error: %v", err)
	}
	if ok {
		t.Fatal("a restarted process must not re-spend the durable ceiling")
	}
}

// TestDBBudgetDayBoundaryRolloverIsIdempotent proves the reset is by bucket-key
// ROLLOVER: advancing the injected clock past the window boundary yields a NEW
// key with FULL headroom, the PRIOR window's durable row is left intact
// (append-only-safe: never rewritten or deleted), and re-running reserves in the
// new window accrue only there — no double-reset, no lost/duplicated accounting.
func TestDBBudgetDayBoundaryRolloverIsIdempotent(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, _, _, _, _ := seedConfirmedTarget(t, pool, q)

	const limit = 2
	day1 := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	now := day1
	b := routec.NewDBBudget(pool, limit, 1<<40, 24*time.Hour, func() time.Time { return now })

	// Exhaust window 1.
	for i := 0; i < limit; i++ {
		ok, err := b.Reserve(ctx, account)
		if err != nil || !ok {
			t.Fatalf("day1 reserve %d: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, _ := b.Reserve(ctx, account); ok {
		t.Fatal("day1 must be exhausted")
	}

	// Cross the daily boundary: a NEW window key => full headroom.
	now = time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	st, err := b.Snapshot(ctx, account)
	if err != nil {
		t.Fatalf("day2 snapshot: %v", err)
	}
	if st.RequestsRemaining != limit {
		t.Fatalf("post-rollover headroom: got %d want %d", st.RequestsRemaining, limit)
	}

	// Reserve twice in window 2 (accrues in the new row only).
	for i := 0; i < limit; i++ {
		ok, err := b.Reserve(ctx, account)
		if err != nil || !ok {
			t.Fatalf("day2 reserve %d: ok=%v err=%v", i, ok, err)
		}
	}

	// The PRIOR window's row is preserved verbatim (append-only-safe): still exactly
	// `limit` used, never rewritten by the rollover. Two distinct durable rows exist.
	key1 := day1.UTC().Truncate(24 * time.Hour)
	key2 := now.UTC().Truncate(24 * time.Hour)
	var used1, used2 int
	if err := pool.QueryRow(ctx,
		`SELECT requests_used FROM route_budget_usage WHERE account_id=$1 AND window_key=$2`,
		account, key1).Scan(&used1); err != nil {
		t.Fatalf("read window1 row: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT requests_used FROM route_budget_usage WHERE account_id=$1 AND window_key=$2`,
		account, key2).Scan(&used2); err != nil {
		t.Fatalf("read window2 row: %v", err)
	}
	if used1 != limit || used2 != limit {
		t.Fatalf("rollover accounting: window1 used=%d window2 used=%d, want %d/%d", used1, used2, limit, limit)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM route_budget_usage WHERE account_id=$1`, account).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("rollover must add a NEW row, not reset: got %d rows want 2", rows)
	}

	// Idempotent re-computation: snapshotting the same boundary again is stable — no
	// double-reset, no extra row.
	if _, err := b.Snapshot(ctx, account); err != nil {
		t.Fatalf("re-snapshot: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM route_budget_usage WHERE account_id=$1`, account).Scan(&rows); err != nil {
		t.Fatalf("count rows again: %v", err)
	}
	if rows != 2 {
		t.Fatalf("re-running rollover logic changed accounting: got %d rows want 2", rows)
	}
}

// TestDBBudgetReserveAndConsume ports the single-threaded happy path to the
// durable store: request headroom is enforced and byte spend tracked per account.
func TestDBBudgetReserveAndConsume(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, _, _, _, _ := seedConfirmedTarget(t, pool, q)
	other, _, _, _, _ := seedConfirmedTarget(t, pool, q)

	clock := func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	b := routec.NewDBBudget(pool, 3, 1000, 24*time.Hour, clock)

	for i := 0; i < 3; i++ {
		if ok, err := b.Reserve(ctx, account); err != nil || !ok {
			t.Fatalf("reserve %d: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, _ := b.Reserve(ctx, account); ok {
		t.Fatal("4th reserve should be denied (request budget exhausted)")
	}
	// A different account has its own durable budget.
	if ok, err := b.Reserve(ctx, other); err != nil || !ok {
		t.Fatalf("other account should have fresh budget: ok=%v err=%v", ok, err)
	}
	if err := b.Consume(ctx, account, 999); err != nil {
		t.Fatalf("consume: %v", err)
	}
	st, err := b.Snapshot(ctx, account)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if st.BytesRemaining != 1 {
		t.Fatalf("bytes remaining: got %d want 1", st.BytesRemaining)
	}
}
