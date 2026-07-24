package notify_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// Issue #124 / PD-4 item 2 — REAL River + REAL PostgreSQL correlated-failure evidence.
//
// The hazard these tests exist for: River's attempt-completion/snooze write and the
// application's terminal delivery write share ONE PostgreSQL dependency. When that
// dependency fails, BOTH fail. The job can then be left `running` on its final attempt,
// and River's rescuer DISCARDS a crashed job that has exhausted MaxAttempts — while the
// delivery row is still nonterminal. Once the business day advances, a fan-out that
// only ever finalizes the CURRENT closed day would never look at that account/day
// again, so the work would be permanently abandoned.
//
// Recovery therefore may NOT be anchored in River state. It is anchored in the durable
// delivery table, which pins its own business day. These tests drive that through a
// real River client against a real database — no mocks.

// riverPool provisions the pool AND River's own schema (the jobs_test template).
func riverPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping real River digest test")
	}
	pool, _ := newPool(t)
	if err := jobs.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("apply river migrations: %v", err)
	}
	return pool
}

// faultyDeliveryStore wraps the real pgx-backed store and fails the TERMINAL writes
// while armed — the correlated failure in which PostgreSQL is unavailable exactly when
// River needs it too. Every non-terminal read/write still goes to the real database, so
// the durable state under test is genuine.
type faultyDeliveryStore struct {
	notify.DigestDeliveryStore
	mu    sync.Mutex
	armed bool
}

var errTerminalWriteDown = errors.New("notify-test: terminal state write failed (database unavailable)")

func (f *faultyDeliveryStore) arm(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed = v
}

func (f *faultyDeliveryStore) down() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.armed
}

func (f *faultyDeliveryStore) MarkDelivered(ctx context.Context, a uuid.UUID, d time.Time, at time.Time) error {
	if f.down() {
		return errTerminalWriteDown
	}
	return f.DigestDeliveryStore.MarkDelivered(ctx, a, d, at)
}

func (f *faultyDeliveryStore) MarkDeadLetter(ctx context.Context, a uuid.UUID, d time.Time, r notify.DigestReason, c int32, at time.Time) error {
	if f.down() {
		return errTerminalWriteDown
	}
	return f.DigestDeliveryStore.MarkDeadLetter(ctx, a, d, r, c, at)
}

func (f *faultyDeliveryStore) MarkUnconfirmed(ctx context.Context, a uuid.UUID, d time.Time, r notify.DigestReason, c int32, at time.Time) error {
	if f.down() {
		return errTerminalWriteDown
	}
	return f.DigestDeliveryStore.MarkUnconfirmed(ctx, a, d, r, c, at)
}

// startDigestRiver builds and starts a REAL River client that works ONLY the digest
// queue, using the platform's real worker registry and the production queue bound.
//
// It deliberately does not enable the shared `default` queue. Go runs packages in
// parallel against one database, so a client that also drained `default` would consume
// other packages' jobs (heartbeat, execution intents, reopens) and make their
// assertions fail — a harness artifact that says nothing about the digest. Scoping to
// the queue under test is also the point of the change: digest work lives on its own
// bounded queue and never touches default capacity.
func startDigestRiver(t *testing.T, pool *pgxpool.Pool, svc *notify.DigestService) *jobs.Client {
	t.Helper()
	return startDigestOnlyRiver(t, pool, svc, 0)
}

// startDigestOnlyRiver is startDigestRiver with an explicit per-account work deadline.
func startDigestOnlyRiver(t *testing.T, pool *pgxpool.Pool, svc *notify.DigestService, timeout time.Duration) *jobs.Client {
	t.Helper()
	workers, err := jobs.NewWorkers(nil, jobs.ExecutionRunners{
		DigestAccount:        svc.DeliverAccountDay,
		DigestAccountTimeout: timeout,
	})
	if err != nil {
		t.Fatalf("workers: %v", err)
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Workers: workers,
		Queues: map[string]river.QueueConfig{
			// The SAME queue name and bound production configures.
			jobs.QueueDigestAccount: {MaxWorkers: jobs.DigestAccountMaxConcurrency},
		},
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("start client: %v", err)
	}
	svc.SetAccountEnqueuer(notify.NewDigestAccountDispatcher(client))
	return client
}

func stopRiver(t *testing.T, client *jobs.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Stop(ctx); err != nil {
		t.Logf("stop client: %v", err)
	}
}

// TestRiver_CorrelatedTerminalWriteFailureRecoversWithZeroResend is the PD-4 item 2
// evidence, end to end on real River + real PostgreSQL:
//
//	terminal-write failure → job discarded at its exhausted attempt (River's own
//	completion path failing in the same outage) → process restart → business day
//	advances → OWNED recovery rediscovers the pinned row → terminal state reached →
//	EXACTLY ONE delivery, ever.
func TestRiver_CorrelatedTerminalWriteFailureRecoversWithZeroResend(t *testing.T) {
	ctx := context.Background()
	pool := riverPool(t)
	_, q := newPool(t)
	account := seedAccount(t, q)

	at := time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC)
	day := time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC)
	// Every digest line names its SHARED event id (NOT-001), so counting messages that
	// carry THIS event id counts deliveries of THIS account/day exactly. Recovery is
	// deliberately global — it re-drives every nonterminal row in the database — so the
	// assertion must be scoped to this test's own work rather than to the sink's size.
	event := uuid.New()
	insertNotifAt(t, pool, account, event, "corr-"+uuid.NewString(), "v1", day.Add(2*time.Hour))

	mailer := &captureMailer{}
	faulty := &faultyDeliveryStore{DigestDeliveryStore: notify.NewDBDigestDeliveryStore(pool)}
	faulty.arm(true) // the terminal write is DOWN, exactly as River's own write would be

	svc := digestFor(pool, mailer, at).WithDeliveryStore(faulty)
	client := startDigestRiver(t, pool, svc)

	// --- Phase A: the send succeeds, the TERMINAL write fails --------------------
	if err := svc.EnsureDelivery(ctx, account, day, true); err != nil {
		t.Fatalf("ensure + transactional enqueue: %v", err)
	}
	waitFor(t, 20*time.Second, "the relay to accept the digest", func() bool {
		return deliveriesOf(mailer, event) == 1
	})
	// The row must remain NONTERMINAL: no terminal signal may be recorded for a state
	// that was never persisted.
	waitFor(t, 10*time.Second, "the row to settle in the ambiguous sending state", func() bool {
		return deliveryState(t, pool, account, day) == notify.DigestStateSending
	})

	// --- Phase B: River's own durability fails too; the job is discarded ---------
	// A crashed job that exhausted MaxAttempts is discarded by River's rescuer. Model
	// that terminal River outcome directly, then restart the process.
	stopRiver(t, client)
	if _, err := pool.Exec(ctx,
		`UPDATE river_job SET state = 'discarded', attempt = max_attempts, finalized_at = now()
		 WHERE kind = $1 AND args->>'account' = $2`,
		jobs.DigestAccountArgs{}.Kind(), account.String()); err != nil {
		t.Fatalf("simulate river discard: %v", err)
	}
	if got := deliveryState(t, pool, account, day); got != notify.DigestStateSending {
		t.Fatalf("after the correlated failure the delivery row is %q; it must still be nonterminal so recovery can repair it", got)
	}

	// --- Phase C: restart, the day advances, OWNED recovery repairs -------------
	// Two days later the original day is long past. A fan-out that only finalizes the
	// CURRENT closed day would never revisit it — recovery must, because the row pins
	// its own business day and is rediscovered from the delivery table, not from River.
	faulty.arm(false) // the database is back
	later := at.Add(48 * time.Hour)
	svc2 := digestFor(pool, mailer, later).WithDeliveryStore(faulty)
	client2 := startDigestRiver(t, pool, svc2)
	defer stopRiver(t, client2)

	rows, err := svc2.NonterminalDeliveries(ctx, later)
	if err != nil {
		t.Fatalf("rediscover: %v", err)
	}
	if !containsAccountDay(rows, account, day) {
		t.Fatalf("owned recovery did not rediscover the abandoned account/day %s/%s; it would be permanently lost",
			account, day.Format(time.DateOnly))
	}
	if _, err := svc2.RecoverNonterminal(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	// The re-driven attempt finalizes the row WITHOUT resending: acceptance could not be
	// established, so it becomes the terminal AMBIGUOUS state, which never claims
	// delivery and is never re-driven again.
	waitFor(t, 20*time.Second, "recovery to finalize the abandoned row", func() bool {
		s := deliveryState(t, pool, account, day)
		return s == notify.DigestStateUnconfirmed || s == notify.DigestStateDelivered
	})

	// THE INVARIANT: exactly one delivery, across the failure, the discard, the
	// restart, the day advancement, and the recovery.
	if got := deliveriesOf(mailer, event); got != 1 {
		t.Fatalf("this account/day was delivered %d times, want exactly 1 (recovery must never resend)", got)
	}

	// And the repaired row is terminal, so no later pass rediscovers it.
	rows, err = svc2.NonterminalDeliveries(ctx, later.Add(time.Hour))
	if err != nil {
		t.Fatalf("re-rediscover: %v", err)
	}
	if containsAccountDay(rows, account, day) {
		t.Fatal("the repaired account/day is still nonterminal; recovery would loop forever")
	}
}

// TestRiver_PerAccountJobIsBoundedAndUniquePerAccountDay pins the durability seam the
// isolation guarantee rests on: per-account digest work runs on its OWN bounded queue
// with its OWN bounded retry budget, and duplicate fan-outs collapse to ONE in-flight
// job per (account, business_day).
func TestRiver_PerAccountJobIsBoundedAndUniquePerAccountDay(t *testing.T) {
	ctx := context.Background()
	pool := riverPool(t)
	_, q := newPool(t)
	account := seedAccount(t, q)
	day := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	// Insert-only client: nothing works the jobs, so they stay observable in-flight.
	client, err := jobs.NewClient(pool, nil, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	for range 4 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := jobs.EnqueueDigestAccountTx(ctx, client, tx, account, day); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	var count int
	var queue string
	var maxAttempts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), min(queue), min(max_attempts) FROM river_job
		 WHERE kind = $1 AND args->>'account' = $2 AND args->>'business_day' = $3`,
		jobs.DigestAccountArgs{}.Kind(), account.String(), day.Format(time.DateOnly),
	).Scan(&count, &queue, &maxAttempts); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if count != 1 {
		t.Fatalf("four duplicate fan-outs produced %d in-flight jobs, want 1 per (account, business_day)", count)
	}
	if queue != jobs.QueueDigestAccount {
		t.Fatalf("queue = %q, want %q (digest work must not consume the shared default queue)", queue, jobs.QueueDigestAccount)
	}
	if maxAttempts != jobs.DigestAccountMaxAttempts {
		t.Fatalf("max_attempts = %d, want %d (a bounded per-account retry budget)", maxAttempts, jobs.DigestAccountMaxAttempts)
	}

	// A DIFFERENT day for the same account is DISTINCT work and must not be collapsed —
	// otherwise a delayed day could be swallowed by the current one.
	other := day.AddDate(0, 0, 1)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := jobs.EnqueueDigestAccountTx(ctx, client, tx, account, other); err != nil {
		t.Fatalf("enqueue other day: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'account' = $2`,
		jobs.DigestAccountArgs{}.Kind(), account.String()).Scan(&count); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if count != 2 {
		t.Fatalf("two distinct business days produced %d jobs, want 2 (a pinned day is its own unit of work)", count)
	}
}

// deliveriesOf counts captured messages that carry the given SHARED event id — i.e. how
// many times that account/day's digest was actually delivered.
func deliveriesOf(m *captureMailer, event uuid.UUID) int {
	n := 0
	for _, msg := range m.messages() {
		if strings.Contains(msg.Body, event.String()) {
			n++
		}
	}
	return n
}

// containsAccountDay reports whether the recovery set names this (account, day).
func containsAccountDay(rows []notify.DigestDelivery, account uuid.UUID, day time.Time) bool {
	for _, r := range rows {
		if r.Account == account && r.BusinessDay.Equal(day) {
			return true
		}
	}
	return false
}

// waitFor polls cond until it holds or the budget elapses.
func waitFor(t *testing.T, budget time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", budget, what)
}
