package notify_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// Issue #124 acceptance tests, driven through REAL River + REAL PostgreSQL.
//
// The defect: the fan-out processed accounts in a stable created_at order and returned
// on the FIRST account error, so a permanently unsendable account blocked every later
// account on EVERY retry — a persistent, not probabilistic, cross-tenant outage.
//
// The guarantee now under test: each account/day is its own durable row, its own job,
// its own retry budget, and its own slot on a separately bounded queue.

// scriptedMailer routes per-account behaviour off the recipient address, so poison and
// healthy tenants can be mixed in one fan-out. It never contacts a real relay.
type scriptedMailer struct {
	mu       sync.Mutex
	sent     []notify.Message
	poison   map[string]bool // recipient → fail permanently
	blocking map[string]bool // recipient → block until the attempt deadline elapses
	failing  map[string]bool // recipient → fail transiently (heals when removed)
	attempts map[string]int
	// inflight/peakInflight measure ACTUAL concurrent delivery attempts. The river_job
	// `running` row count is not a usable concurrency measure: it transiently shows an
	// extra row while one attempt is being finalized and its successor is being claimed.
	inflight     int
	peakInflight int
}

func newScriptedMailer() *scriptedMailer {
	return &scriptedMailer{
		poison:   map[string]bool{},
		blocking: map[string]bool{},
		failing:  map[string]bool{},
		attempts: map[string]int{},
	}
}

func (m *scriptedMailer) Send(ctx context.Context, msg notify.Message) error {
	m.mu.Lock()
	m.attempts[msg.To]++
	m.inflight++
	if m.inflight > m.peakInflight {
		m.peakInflight = m.inflight
	}
	poison, blocking, failing := m.poison[msg.To], m.blocking[msg.To], m.failing[msg.To]
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.inflight--
		m.mu.Unlock()
	}()

	if blocking {
		// A hanging relay. It releases only when the attempt's bounded work deadline
		// elapses — which is exactly what stops it holding a worker slot forever.
		<-ctx.Done()
		return &notify.SendError{Reason: notify.ReasonSMTPTimeout}
	}
	if poison {
		// A DEFINITIVE permanent rejection (relay 5xx): never delivered, never retried
		// into success, and quarantined on the account's own row.
		return &notify.SendError{Reason: notify.ReasonSMTPPermanentRejection, Code: 550}
	}
	if failing {
		return &notify.SendError{Reason: notify.ReasonSMTPTransientRejection, Code: 451}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *scriptedMailer) delivered(to string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, msg := range m.sent {
		if msg.To == to {
			n++
		}
	}
	return n
}

func (m *scriptedMailer) attemptCount(to string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts[to]
}

func (m *scriptedMailer) peakConcurrency() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peakInflight
}

func (m *scriptedMailer) set(field map[string]bool, to string, v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	field[to] = v
}

// perAccountResolver gives each account a distinct recipient so the scripted mailer can
// tell tenants apart. The address is derived from the account id.
type perAccountResolver struct{}

func (perAccountResolver) Resolve(_ context.Context, account uuid.UUID) (notify.Target, error) {
	return notify.Target{
		Email:       recipientFor(account),
		Locale:      "en",
		BriefingURL: "https://app/briefing",
	}, nil
}

func recipientFor(account uuid.UUID) string { return account.String() + "@tenant.test" }

// quiesceDigestWork retires the digest work of the accounts a test CREATED, once that
// test is done with it. The suite shares one scratch database and the OWNED recovery
// pass is deliberately global (it re-drives every nonterminal row in the database), so
// without this a finished test's poison backlog would occupy the digest queue and
// masquerade as the next test's starvation.
//
// It is SCOPED to the named accounts on purpose. A blanket UPDATE over every pending or
// sending delivery row — and a blanket DELETE of every digest job — would mutate rows
// this test never created, including other packages' state under a parallel
// `go test ./...`, and could mask a real stranded row as tidy cleanup.
func quiesceDigestWork(t *testing.T, pool *pgxpool.Pool, accounts ...uuid.UUID) {
	t.Helper()
	if len(accounts) == 0 {
		return
	}
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`UPDATE notification_digest_deliveries
		    SET delivery_state = 'skipped', finalized_at = now(), updated_at = now()
		  WHERE delivery_state IN ('pending', 'sending')
		    AND marketplace_account_id = ANY($1)`, accounts); err != nil {
		t.Fatalf("quiesce delivery rows: %v", err)
	}
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.String())
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM river_job WHERE kind = $1 AND args->>'account' = ANY($2)`,
		jobs.DigestAccountArgs{}.Kind(), ids); err != nil {
		t.Fatalf("quiesce digest jobs: %v", err)
	}
}

// fanoutFixture seeds n accounts, each with one eligible notification on `day`.
func fanoutFixture(t *testing.T, pool *pgxpool.Pool, day time.Time, n int) []uuid.UUID {
	t.Helper()
	_, q := newPool(t)
	ids := make([]uuid.UUID, 0, n)
	for range n {
		a := seedAccount(t, q)
		insertNotifAt(t, pool, a, uuid.New(), "fan-"+uuid.NewString(), "v1", day.Add(3*time.Hour))
		ids = append(ids, a)
	}
	return ids
}

// newFanoutService builds a digest service over the scripted mailer and per-account
// resolver, pinned to `at`.
func newFanoutService(pool *pgxpool.Pool, m notify.Mailer, at time.Time) *notify.DigestService {
	return notify.NewDigestService(pool, m, perAccountResolver{}).WithClock(fixedClock(at))
}

// startFanoutRiver starts a real River client with a configurable per-account work
// deadline, so the bounded-fan-out behaviour is exercised rather than asserted.
func startFanoutRiver(t *testing.T, pool *pgxpool.Pool, svc *notify.DigestService, timeout time.Duration, accounts ...uuid.UUID) *jobs.Client {
	t.Helper()
	client := startDigestOnlyRiver(t, pool, svc, timeout)
	t.Cleanup(func() {
		stopRiver(t, client)
		quiesceDigestWork(t, pool, accounts...)
	})
	return client
}

// TestFanOut_PoisonFirstAccountDoesNotBlockLaterAccounts is the issue's reproduction,
// inverted into an assertion. Account A is created FIRST and is permanently
// unsendable; accounts B and C are healthy. Under the old serial pass every retry
// failed on A before reaching B, so B never received its digest. Now A's failure is
// confined to A's own row and job.
func TestFanOut_PoisonFirstAccountDoesNotBlockLaterAccounts(t *testing.T) {
	ctx := context.Background()
	pool := riverPool(t)

	at := time.Date(2026, 5, 6, 8, 0, 0, 0, time.UTC)
	day := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	ids := fanoutFixture(t, pool, day, 3)
	poison, healthyB, healthyC := ids[0], ids[1], ids[2]

	m := newScriptedMailer()
	m.set(m.poison, recipientFor(poison), true)

	svc := newFanoutService(pool, m, at)
	startFanoutRiver(t, pool, svc, jobs.DigestAccountMinTimeout, ids...)

	// Enqueue in creation order: the poison account is FIRST, exactly as reported.
	for _, id := range ids {
		if _, err := svc.EnsureDelivery(ctx, id, day, true); err != nil {
			t.Fatalf("ensure %s: %v", id, err)
		}
	}

	waitFor(t, 45*time.Second, "both healthy accounts to receive their digest", func() bool {
		return m.delivered(recipientFor(healthyB)) == 1 && m.delivered(recipientFor(healthyC)) == 1
	})
	if got := m.delivered(recipientFor(poison)); got != 0 {
		t.Fatalf("the permanently unsendable account delivered %d messages, want 0 (no false delivery)", got)
	}

	// The poison account quarantines on ITS OWN row — observable, terminal, and not
	// marked delivered.
	waitFor(t, 30*time.Second, "the poison account to reach its terminal dead-letter state", func() bool {
		return deliveryState(t, pool, poison, day) == notify.DigestStateDeadLetter
	})
	// The terminal write lands just after the send returns, so wait for the durable
	// state rather than racing it.
	for _, ok := range []uuid.UUID{healthyB, healthyC} {
		waitFor(t, 30*time.Second, "the healthy account's row to reach its terminal delivered state", func() bool {
			return deliveryState(t, pool, ok, day) == notify.DigestStateDelivered
		})
	}
}

// TestFanOut_PoisonAccountsBeyondQueueCapacityCannotStarveWork proves the bounded
// fan-out: MORE simultaneously hanging tenants than the digest queue has workers can
// neither block healthy digest accounts nor consume capacity belonging to unrelated
// work on the shared default queue (the load-shedding order — the digest is advisory
// UI and must never squeeze the approval, audit-adjacent, and urgent paths).
func TestFanOut_PoisonAccountsBeyondQueueCapacityCannotStarveWork(t *testing.T) {
	ctx := context.Background()
	pool := riverPool(t)

	at := time.Date(2026, 5, 13, 8, 0, 0, 0, time.UTC)
	day := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	// Five hanging tenants against a digest queue bounded well below that.
	poisonIDs := fanoutFixture(t, pool, day, 5)
	healthyIDs := fanoutFixture(t, pool, day, 2)
	if len(poisonIDs) <= jobs.DigestAccountMaxConcurrency {
		t.Fatalf("test setup: %d poison accounts does not exceed the queue bound %d; starvation would not be exercised",
			len(poisonIDs), jobs.DigestAccountMaxConcurrency)
	}

	m := newScriptedMailer()
	for _, p := range poisonIDs {
		m.set(m.blocking, recipientFor(p), true)
	}

	svc := newFanoutService(pool, m, at)
	startFanoutRiver(t, pool, svc, jobs.DigestAccountMinTimeout, append(append([]uuid.UUID{}, poisonIDs...), healthyIDs...)...)

	// Every poison account is enqueued FIRST, so they hold every digest slot.
	for _, id := range append(append([]uuid.UUID{}, poisonIDs...), healthyIDs...) {
		if _, err := svc.EnsureDelivery(ctx, id, day, true); err != nil {
			t.Fatalf("ensure %s: %v", id, err)
		}
	}

	// The healthy digest accounts still deliver behind a >capacity poison backlog:
	// every hanging attempt releases its slot on the bounded work deadline instead of
	// holding it indefinitely.
	waitFor(t, 120*time.Second, "healthy digest accounts to deliver behind the poison backlog", func() bool {
		for _, h := range healthyIDs {
			if m.delivered(recipientFor(h)) != 1 {
				return false
			}
		}
		return true
	})

	// BOUNDED CONCURRENCY: however many tenants hang simultaneously, concurrent digest
	// attempts never exceed the digest queue's own bound. (How MUCH concurrency is
	// actually reached depends on River's fetch timing and machine load, so only the
	// upper bound is asserted — the lower bound would be a flake, not a guarantee.)
	if peak := m.peakConcurrency(); peak > jobs.DigestAccountMaxConcurrency {
		t.Fatalf("peak concurrent digest attempts = %d, want at most %d (bounded fan-out)", peak, jobs.DigestAccountMaxConcurrency)
	}

	// CROSS-QUEUE ISOLATION: no digest job ever lands on the shared default queue, so
	// digest work can never consume capacity the approval, audit-adjacent, and
	// urgent-email paths depend on (the load-shedding order — the digest is advisory UI).
	var onDefault int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE kind = $1 AND queue = 'default'`,
		jobs.DigestAccountArgs{}.Kind()).Scan(&onDefault); err != nil {
		t.Fatalf("query river_job queues: %v", err)
	}
	if onDefault != 0 {
		t.Fatalf("%d digest jobs landed on the shared default queue; digest work must stay on its own bounded queue", onDefault)
	}
	// The hanging tenants are quarantined on their OWN rows, never marked delivered.
	for _, p := range poisonIDs {
		if got := m.delivered(recipientFor(p)); got != 0 {
			t.Fatalf("hanging tenant %s recorded %d deliveries, want 0", p, got)
		}
	}

	// DURABLE OUTCOME, not just the absence of a message. A relay that hangs until the
	// attempt's work deadline elapses is a DEFINITIVE non-acceptance — nothing was
	// transmitted — so the tenant must KEEP its retry budget and, once exhausted, reach
	// the observable dead-letter state. Reaching `unconfirmed` here would mean the row
	// claims "we may have delivered" for a provable non-delivery AND is never retried:
	// the silent, terminal, first-hang non-delivery this test previously could not see.
	for _, p := range poisonIDs {
		switch state := deliveryState(t, pool, p, day); state {
		case notify.DigestStateUnconfirmed:
			t.Fatalf("hanging tenant %s was finalized %q; a deadline-timed-out send is DEFINITIVELY not delivered and must stay retryable",
				p, state)
		case notify.DigestStateDelivered:
			t.Fatalf("hanging tenant %s was marked %q; nothing was ever transmitted", p, state)
		case notify.DigestStatePending, notify.DigestStateSending, notify.DigestStateDeadLetter:
			// Retrying on its own budget, mid-attempt, or terminally quarantined.
		default:
			t.Fatalf("hanging tenant %s is in unexpected state %q", p, state)
		}
	}
}

// TestDeliverAccountDay_HangingRelayOnTheFinalAttemptDeadLettersNeverUnconfirmed is F3's
// terminal-state assertion, isolated from River's backoff schedule: once the bounded
// retry budget is spent, a tenant whose relay hangs until the work deadline reaches the
// OBSERVABLE dead-letter state — definitively not delivered — and never the ambiguous
// terminal state, which would claim a possible delivery and forbid every future retry.
func TestDeliverAccountDay_HangingRelayOnTheFinalAttemptDeadLettersNeverUnconfirmed(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)

	day := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	at := day.Add(30 * time.Hour)
	insertNotifAt(t, pool, account, uuid.New(), "hang-"+uuid.NewString(), "v1", day.Add(2*time.Hour))

	svc := digestFor(pool, newHangingMailer(), at)
	if _, err := svc.EnsureDelivery(context.Background(), account, day, false); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	outcome, err := svc.DeliverAccountDay(ctx, account, day, true) // the FINAL attempt
	if err != nil {
		t.Fatalf("the final attempt must finish terminally, not error: %v", err)
	}
	if outcome != notify.OutcomeDeadLetter {
		t.Fatalf("outcome = %q, want %q", outcome, notify.OutcomeDeadLetter)
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStateDeadLetter {
		t.Fatalf("state = %q, want %q (never %q — nothing was transmitted)",
			state, notify.DigestStateDeadLetter, notify.DigestStateUnconfirmed)
	}
}

// TestFanOut_RestartDuringAnInFlightSendLeavesNoSilentNonDelivery closes the gap the
// existing restart test left: it restarted with NO worker running, so it only proved the
// UNCLAIMED case. A real deploy stops River while attempts are IN FLIGHT, hard-cancelling
// their contexts. The tenant caught mid-send must still record a durable, definitive
// outcome, stay recoverable, and deliver exactly once after the restart — never be
// stranded in `sending` and silently written off.
func TestFanOut_RestartDuringAnInFlightSendLeavesNoSilentNonDelivery(t *testing.T) {
	ctx := context.Background()
	pool := riverPool(t)

	at := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	day := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	ids := fanoutFixture(t, pool, day, 1)
	account := ids[0]
	t.Cleanup(func() { quiesceDigestWork(t, pool, ids...) })

	// "Before the restart": the relay hangs, so the attempt is genuinely in flight when
	// the process stops.
	hang := newHangingMailer()
	svc := newFanoutService(pool, hang, at)
	client := startDigestOnlyRiver(t, pool, svc, jobs.DigestAccountMaxTimeout)
	if _, err := svc.EnsureDelivery(ctx, account, day, true); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	hang.waitEntered(t)

	// The restart, at its WORST: StopAndCancel is River's immediate hard stop, which
	// cancels the in-flight job context outright — precisely what cancelling the Start
	// context did on every SIGTERM before SoftStopTimeout was configured. Correctness may
	// not depend on the drain window, so the hard case is the one asserted.
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStop()
	if err := client.StopAndCancel(stopCtx); err != nil {
		t.Logf("stop and cancel: %v", err)
	}

	// The durable outcome must be recorded despite the cancellation: the row is released
	// for retry, NOT stranded in `sending` and NOT written off as unconfirmed.
	waitFor(t, 30*time.Second, "the interrupted attempt to record its definitive outcome", func() bool {
		return deliveryState(t, pool, account, day) == notify.DigestStatePending
	})

	// "After the restart", with a healthy relay: the work is still outstanding and
	// delivers exactly once for its ORIGINAL pinned day.
	healthy := newScriptedMailer()
	svc2 := newFanoutService(pool, healthy, at)
	startFanoutRiver(t, pool, svc2, jobs.DigestAccountMinTimeout, ids...)
	if _, err := svc2.RecoverNonterminal(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	waitFor(t, 45*time.Second, "the restarted process to deliver the interrupted tenant", func() bool {
		return healthy.delivered(recipientFor(account)) == 1
	})
	waitFor(t, 30*time.Second, "the pinned day's row to reach its terminal delivered state", func() bool {
		return deliveryState(t, pool, account, day) == notify.DigestStateDelivered
	})
	if got := healthy.delivered(recipientFor(account)); got != 1 {
		t.Fatalf("the interrupted tenant was delivered %d times, want exactly 1", got)
	}
}

// TestFanOut_OrderingDoesNotChangeDeliveryCompleteness proves completeness is
// order-independent: the same account set delivers completely whether the poison
// account is first, last, or in the middle. Stable created_at ordering was what made
// the original outage persistent rather than probabilistic.
func TestFanOut_OrderingDoesNotChangeDeliveryCompleteness(t *testing.T) {
	ctx := context.Background()
	pool := riverPool(t)

	// Each position runs as its own subtest so its River client is stopped before the
	// next starts. Two concurrently running clients would both consume the digest queue,
	// and a round's jobs could be worked by the previous round's service — an artifact
	// of the harness, not of the fan-out.
	for i, poisonIdx := range []int{0, 1, 2} {
		t.Run("poison_at_position_"+strconv.Itoa(poisonIdx), func(t *testing.T) {
			at := time.Date(2026, 6, 2+i, 8, 0, 0, 0, time.UTC)
			day := normalizeDay(at.AddDate(0, 0, -1))
			ids := fanoutFixture(t, pool, day, 3)

			m := newScriptedMailer()
			m.set(m.poison, recipientFor(ids[poisonIdx]), true)

			svc := newFanoutService(pool, m, at)
			startFanoutRiver(t, pool, svc, jobs.DigestAccountMinTimeout, ids...)
			for _, id := range ids {
				if _, err := svc.EnsureDelivery(ctx, id, day, true); err != nil {
					t.Fatalf("ensure: %v", err)
				}
			}

			waitFor(t, 45*time.Second, "every healthy account to deliver regardless of poison position", func() bool {
				for j, id := range ids {
					if j == poisonIdx {
						continue
					}
					if m.delivered(recipientFor(id)) != 1 {
						return false
					}
				}
				return true
			})
			if got := m.delivered(recipientFor(ids[poisonIdx])); got != 0 {
				t.Fatalf("the poison account delivered %d messages, want 0", got)
			}
		})
	}
}

// normalizeDay reduces an instant to its UTC calendar day.
func normalizeDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// TestFanOut_FailedAccountRetriesWithoutResendingSuccessfulOnes proves independent
// retry: a transiently failing account keeps retrying on its own budget and eventually
// delivers, while the accounts that already succeeded are never re-sent.
func TestFanOut_FailedAccountRetriesWithoutResendingSuccessfulOnes(t *testing.T) {
	ctx := context.Background()
	pool := riverPool(t)

	at := time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC)
	day := time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)
	ids := fanoutFixture(t, pool, day, 2)
	flapping, healthy := ids[0], ids[1]

	m := newScriptedMailer()
	m.set(m.failing, recipientFor(flapping), true)

	svc := newFanoutService(pool, m, at)
	startFanoutRiver(t, pool, svc, jobs.DigestAccountMinTimeout, ids...)
	for _, id := range ids {
		if _, err := svc.EnsureDelivery(ctx, id, day, true); err != nil {
			t.Fatalf("ensure: %v", err)
		}
	}

	waitFor(t, 30*time.Second, "the healthy account to deliver", func() bool {
		return m.delivered(recipientFor(healthy)) == 1
	})
	waitFor(t, 30*time.Second, "the flapping account to record a failed attempt", func() bool {
		return m.attemptCount(recipientFor(flapping)) >= 1
	})

	// Heal the flapping account and re-drive it. Its own retry completes; the already
	// delivered account is NOT touched.
	m.set(m.failing, recipientFor(flapping), false)
	waitFor(t, 60*time.Second, "the healed account to deliver on its own retry", func() bool {
		if _, err := svc.RecoverNonterminal(ctx); err != nil {
			t.Fatalf("recover: %v", err)
		}
		return m.delivered(recipientFor(flapping)) == 1
	})
	if got := m.delivered(recipientFor(healthy)); got != 1 {
		t.Fatalf("the already-delivered account was sent %d times, want exactly 1 (retries must never resend successful accounts)", got)
	}
	if got := m.attemptCount(recipientFor(healthy)); got != 1 {
		t.Fatalf("the already-delivered account was ATTEMPTED %d times, want 1 (a terminal row is never re-driven)", got)
	}
}

// TestFanOut_RestartPreservesOutstandingWorkAndPinnedDay proves durability across a
// process boundary AND across day advancement: work enqueued before a restart is still
// outstanding after it, and it still finalizes its ORIGINAL business day rather than
// whichever day is current when it finally runs.
func TestFanOut_RestartPreservesOutstandingWorkAndPinnedDay(t *testing.T) {
	ctx := context.Background()
	pool := riverPool(t)

	at := time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC)
	day := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	ids := fanoutFixture(t, pool, day, 1)
	account := ids[0]

	// "Before the restart": record the durable work with NO worker running at all.
	m := newScriptedMailer()
	svc := newFanoutService(pool, m, at)
	insertOnly, err := jobs.NewClient(pool, nil, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	svc.SetAccountEnqueuer(notify.NewDigestAccountDispatcher(insertOnly))
	if _, err := svc.EnsureDelivery(ctx, account, day, true); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got := deliveryState(t, pool, account, day); got != notify.DigestStatePending {
		t.Fatalf("outstanding work state = %q, want %q", got, notify.DigestStatePending)
	}

	// "After the restart", three days later. The current finalized day is now 2026-07-05,
	// yet the outstanding work must still finalize 2026-07-02.
	later := at.AddDate(0, 0, 3)
	svc2 := newFanoutService(pool, m, later)
	startFanoutRiver(t, pool, svc2, jobs.DigestAccountMinTimeout, ids...)
	if svc2.FinalizedBusinessDay().Equal(day) {
		t.Fatal("test setup: the current finalized day must differ from the pinned day")
	}
	if _, err := svc2.RecoverNonterminal(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	waitFor(t, 45*time.Second, "the restarted process to deliver the ORIGINAL business day", func() bool {
		return m.delivered(recipientFor(account)) == 1
	})
	// The terminal write lands just after the send returns, so wait for the durable
	// state rather than racing it.
	waitFor(t, 30*time.Second, "the pinned day's delivery row to reach its terminal state", func() bool {
		return deliveryState(t, pool, account, day) == notify.DigestStateDelivered
	})
	// And nothing was written against the CURRENT day for this account: the window
	// identity survived the restart intact.
	if got := deliveryState(t, pool, account, svc2.FinalizedBusinessDay()); got == notify.DigestStateDelivered {
		t.Fatal("the delayed delivery finalized the CURRENT day instead of its own; the original window would be abandoned")
	}
}

// TestDeliverAccountDay_AttemptsTerminalFailuresAndLagAreObservable proves the
// observability requirement: every per-account attempt reports its outcome, a BOUNDED
// reason, and a lag measured against its OWN pinned business day — so a delivery two
// days late reports ~48h, not a figure recomputed from the current midnight.
func TestDeliverAccountDay_AttemptsTerminalFailuresAndLagAreObservable(t *testing.T) {
	ctx := context.Background()
	pool, q := newPool(t)
	account := seedAccount(t, q)

	day := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	// The attempt runs two days after the day closed (day+24h), so lag ≈ 48h.
	at := day.Add(72 * time.Hour)
	insertNotifAt(t, pool, account, uuid.New(), "obs-"+uuid.NewString(), "v1", day.Add(2*time.Hour))

	type record struct {
		day     time.Time
		outcome string
		reason  string
		lag     time.Duration
	}
	var seen []record
	m := newScriptedMailer()
	m.set(m.poison, recipientFor(account), true) // permanent rejection → terminal failure

	svc := newFanoutService(pool, m, at).WithAttemptObserver(
		func(_ context.Context, _ uuid.UUID, d time.Time, outcome, reason string, lag time.Duration) {
			seen = append(seen, record{d, outcome, reason, lag})
		})
	if _, err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := svc.DeliverAccountDay(ctx, account, day, false); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	if len(seen) != 1 {
		t.Fatalf("attempt observer fired %d times, want 1 (every attempt is observable)", len(seen))
	}
	got := seen[0]
	if !got.day.Equal(day) {
		t.Fatalf("observed day = %s, want the PINNED %s", got.day.Format(time.DateOnly), day.Format(time.DateOnly))
	}
	if got.outcome != notify.OutcomeDeadLetter {
		t.Fatalf("outcome = %q, want %q (a permanent relay rejection is a terminal failure)", got.outcome, notify.OutcomeDeadLetter)
	}
	if got.reason != string(notify.DigestReasonUnsendableTarget) &&
		got.reason != string(notify.ReasonSMTPPermanentRejection) {
		t.Fatalf("reason = %q, want a bounded send-failure token", got.reason)
	}
	// Lag is measured from day+24h, so ~48h — NOT the ~0 a current-midnight calculation
	// would report.
	if got.lag < 47*time.Hour || got.lag > 49*time.Hour {
		t.Fatalf("lag = %s, want ≈48h measured from the PINNED day's close (day+24h)", got.lag)
	}
	// The terminal failure is durable and does NOT claim delivery.
	if state := deliveryState(t, pool, account, day); state != notify.DigestStateDeadLetter {
		t.Fatalf("state = %q, want %q (observable terminal state, never 'delivered')", state, notify.DigestStateDeadLetter)
	}
	if m.delivered(recipientFor(account)) != 0 {
		t.Fatal("a dead-lettered account must never be recorded as delivered")
	}
}

// TestDigestReasons_AreAClosedBoundedSet pins the persisted/logged reason vocabulary:
// short lower-snake LTR technical tokens only. An open-ended reason would reintroduce
// unbounded third-party free text into durable state and telemetry.
func TestDigestReasons_AreAClosedBoundedSet(t *testing.T) {
	for _, r := range notify.DigestReasons() {
		s := string(r)
		if s == "" || len(s) > 40 {
			t.Fatalf("reason %q is not a short bounded token", s)
		}
		for _, c := range s {
			if (c < 'a' || c > 'z') && c != '_' {
				t.Fatalf("reason %q contains %q; reasons are lower-snake LTR technical tokens only", s, c)
			}
		}
		if strings.Contains(s, "@") {
			t.Fatalf("reason %q looks like an address; recipients are never reasons", s)
		}
	}
	// Every token an OPERATOR is instructed to write must be in the same closed set.
	// runbooks/digest-delivery.md re-opens a fixed dead_letter row with this reason, and
	// the column has no CHECK — so the vocabulary, not the database, is what keeps the
	// closed-set claim true.
	if !slices.Contains(notify.DigestReasons(), notify.DigestReasonOperatorRedrive) {
		t.Fatalf("%q is written by the runbook's recovery step but is outside DigestReasons(); the runbook would contradict the closed-set claim",
			notify.DigestReasonOperatorRedrive)
	}
}
