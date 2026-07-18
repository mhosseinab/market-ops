package routec_test

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

// TestLimiterEnforcesHostConcurrency asserts the per-host cap bounds total
// in-flight fetches across accounts.
func TestLimiterEnforcesHostConcurrency(t *testing.T) {
	const perAccount, perHost, workers = 5, 3, 20
	lim := routec.NewLimiter(perAccount, perHost)
	var inflight, maxSeen int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			account := uuid.New() // distinct accounts: only the host cap binds
			release, err := lim.Acquire(context.Background(), account)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			cur := atomic.AddInt64(&inflight, 1)
			for {
				m := atomic.LoadInt64(&maxSeen)
				if cur <= m || atomic.CompareAndSwapInt64(&maxSeen, m, cur) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt64(&inflight, -1)
			release()
		}()
	}
	wg.Wait()
	if maxSeen > perHost {
		t.Fatalf("host concurrency exceeded: peak %d > cap %d", maxSeen, perHost)
	}
}

// TestLimiterPerAccountCap asserts a single account never exceeds its own cap.
func TestLimiterPerAccountCap(t *testing.T) {
	const perAccount, perHost, workers = 2, 100, 12
	lim := routec.NewLimiter(perAccount, perHost)
	account := uuid.New()
	var inflight, maxSeen int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, _ := lim.Acquire(context.Background(), account)
			cur := atomic.AddInt64(&inflight, 1)
			for {
				m := atomic.LoadInt64(&maxSeen)
				if cur <= m || atomic.CompareAndSwapInt64(&maxSeen, m, cur) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt64(&inflight, -1)
			release()
		}()
	}
	wg.Wait()
	if maxSeen > perAccount {
		t.Fatalf("account concurrency exceeded: peak %d > cap %d", maxSeen, perAccount)
	}
}

// TestBudgetReserveAndConsume asserts request headroom is enforced and byte spend
// is tracked per account within a window.
func TestBudgetReserveAndConsume(t *testing.T) {
	clock := time.Unix(0, 0).UTC()
	b := routec.NewBudget(3, 1000, 24*time.Hour, func() time.Time { return clock })
	a := uuid.New()
	for i := 0; i < 3; i++ {
		if !b.Reserve(a) {
			t.Fatalf("reserve %d should succeed", i)
		}
	}
	if b.Reserve(a) {
		t.Fatal("4th reserve should fail (request budget exhausted)")
	}
	// A different account has its own budget.
	if !b.Reserve(uuid.New()) {
		t.Fatal("other account should have fresh budget")
	}
	b.Consume(a, 999)
	if got := b.Snapshot(a).BytesRemaining; got != 1 {
		t.Fatalf("bytes remaining: got %d want 1", got)
	}
}

// TestBudgetWindowRollover is the §17.3 daily-budget acceptance: an exhausted
// account resumes after the window boundary. The reset is deterministic —
// driven by advancing the injected clock past the window, never wall-clock.
func TestBudgetWindowRollover(t *testing.T) {
	clock := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	b := routec.NewBudget(2, 1<<30, 24*time.Hour, func() time.Time { return clock })
	a := uuid.New()

	r1 := b.Reserve(a)
	r2 := b.Reserve(a)
	if !r1 || !r2 {
		t.Fatal("first two reserves in window should succeed")
	}
	if b.Reserve(a) {
		t.Fatal("third reserve within window must fail (budget exhausted)")
	}
	// Advance within the SAME day: still exhausted (no premature reset).
	clock = clock.Add(6 * time.Hour)
	if b.Reserve(a) {
		t.Fatal("still same window: must remain exhausted")
	}
	// Cross the daily boundary: the account resumes.
	clock = time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	if !b.Reserve(a) {
		t.Fatal("after window rollover the account must resume (no permanent starvation)")
	}
	if got := b.Snapshot(a).RequestsRemaining; got != 1 {
		t.Fatalf("post-rollover remaining: got %d want 1", got)
	}
}

// TestBackoffFullJitterBounds asserts exponential backoff stays within
// [0, min(base*factor^attempt, max)].
func TestBackoffFullJitterBounds(t *testing.T) {
	bo := routec.Backoff{Base: time.Second, Max: 30 * time.Second, Factor: 2}
	rng := rand.New(rand.NewSource(7))
	for attempt := 0; attempt < 8; attempt++ {
		ceil := bo.Base
		for i := 0; i < attempt; i++ {
			ceil *= 2
		}
		if ceil > bo.Max {
			ceil = bo.Max
		}
		for i := 0; i < 200; i++ {
			d := bo.Delay(attempt, rng)
			if d < 0 || d > ceil {
				t.Fatalf("attempt %d: delay %s out of [0,%s]", attempt, d, ceil)
			}
		}
	}
}
