package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// Issue #124 — bounds on the per-account digest job.
//
// "Bounded fan-out" is only true if the bound cannot be configured away. Without an
// enforced MAXIMUM work deadline, any positive configured timeout would be accepted,
// so a single tenant could legitimately retain a worker slot for an arbitrarily long
// period and the advertised bound would be decorative.

func TestClampDigestAccountTimeout_Bounds(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero takes the default", 0, jobs.DigestAccountDefaultTimeout},
		{"negative takes the default", -time.Second, jobs.DigestAccountDefaultTimeout},
		{"below the minimum clamps up", time.Millisecond, jobs.DigestAccountMinTimeout},
		{"exactly the minimum is honoured", jobs.DigestAccountMinTimeout, jobs.DigestAccountMinTimeout},
		{"in range is honoured", 42 * time.Second, 42 * time.Second},
		{"exactly the maximum is honoured", jobs.DigestAccountMaxTimeout, jobs.DigestAccountMaxTimeout},
		{"above the maximum clamps down", 24 * time.Hour, jobs.DigestAccountMaxTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jobs.ClampDigestAccountTimeout(c.in); got != c.want {
				t.Fatalf("ClampDigestAccountTimeout(%s) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

// TestDigestAccountWorker_TimeoutIsAlwaysBounded proves the worker never advertises an
// unbounded (or absurdly long) attempt deadline, whatever it was constructed with. A
// zero Timeout would mean "no per-job deadline" in River, which is exactly how a poison
// account could hold a slot forever.
func TestDigestAccountWorker_TimeoutIsAlwaysBounded(t *testing.T) {
	for _, in := range []time.Duration{0, -time.Hour, time.Nanosecond, 30 * time.Second, 365 * 24 * time.Hour} {
		w := jobs.NewDigestAccountWorker(nil, in, nil)
		got := w.Timeout(&river.Job[jobs.DigestAccountArgs]{})
		if got <= 0 {
			t.Fatalf("timeout for input %s is %s; a non-positive deadline is unbounded", in, got)
		}
		if got > jobs.DigestAccountMaxTimeout {
			t.Fatalf("timeout for input %s is %s, above the enforced maximum %s", in, got, jobs.DigestAccountMaxTimeout)
		}
		if got < jobs.DigestAccountMinTimeout {
			t.Fatalf("timeout for input %s is %s, below the enforced minimum %s", in, got, jobs.DigestAccountMinTimeout)
		}
	}
}

// TestDigestAccountWorker_NoRunnerFailsClosed proves a missing consumer never silently
// completes a committed delivery row: the intent parks and retries instead of being
// dropped.
func TestDigestAccountWorker_NoRunnerFailsClosed(t *testing.T) {
	w := jobs.NewDigestAccountWorker(nil, 0, nil)
	err := w.Work(context.Background(), &river.Job[jobs.DigestAccountArgs]{
		Args: jobs.DigestAccountArgs{Account: uuid.New(), BusinessDay: "2026-03-11"},
	})
	if err == nil {
		t.Fatal("a nil runner completed the job; a committed delivery row must never be silently dropped")
	}
}

// TestDigestAccountWorker_UnparseableDayFailsClosed proves the pinned business day is
// never silently replaced. Substituting "today" for an unreadable pinned day would
// finalize the WRONG window and abandon the original one.
func TestDigestAccountWorker_UnparseableDayFailsClosed(t *testing.T) {
	called := false
	w := jobs.NewDigestAccountWorker(
		func(context.Context, uuid.UUID, time.Time, bool) (string, error) {
			called = true
			return "delivered", nil
		}, 0, nil)
	err := w.Work(context.Background(), &river.Job[jobs.DigestAccountArgs]{
		Args: jobs.DigestAccountArgs{Account: uuid.New(), BusinessDay: "not-a-date"},
	})
	if err == nil {
		t.Fatal("an unparseable pinned business day was accepted; the wrong window would be finalized")
	}
	if called {
		t.Fatal("the runner was invoked with an unreadable pinned day")
	}
}

// TestDigestAccountWorker_TerminalUnpersistedSnoozesInsteadOfDiscarding proves the
// correlated-failure contract at the worker boundary: when a terminal decision could
// not be persisted, the attempt is PARKED (which does not consume the retry budget)
// rather than returned verbatim, which on an exhausted attempt would let River DISCARD
// the intent while no terminal signal was ever truthfully emitted.
func TestDigestAccountWorker_TerminalUnpersistedSnoozesInsteadOfDiscarding(t *testing.T) {
	w := jobs.NewDigestAccountWorker(
		func(context.Context, uuid.UUID, time.Time, bool) (string, error) {
			return "terminal_unpersisted", errFmt(jobs.ErrDigestTerminalUnpersisted)
		}, 0, nil)

	err := w.Work(context.Background(), &river.Job[jobs.DigestAccountArgs]{
		Args: jobs.DigestAccountArgs{Account: uuid.New(), BusinessDay: "2026-03-11"},
	})
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("worker returned %v, want a JobSnooze so the exhausted attempt is re-driven, never discarded", err)
	}
	if snooze.Duration <= 0 {
		t.Fatalf("snooze duration = %s, want a bounded positive park", snooze.Duration)
	}
}

// TestDigestAccountWorker_SnoozeIsCappedSoAStuckAccountReleasesItsSlot is the bound on
// the park above. river.JobSnooze does NOT consume an attempt, so an uncapped re-drive
// would re-run every backoff forever, holding one of the few digest slots for a single
// stuck account/day and hiding a permanent failure as ordinary queue latency. Past the
// bounded window the worker stops snoozing and returns a DISTINCT bounded failure, so the
// per-account retry budget applies and the job is ultimately discarded — while the
// durable row stays nonterminal for the owned recovery pass.
func TestDigestAccountWorker_SnoozeIsCappedSoAStuckAccountReleasesItsSlot(t *testing.T) {
	w := jobs.NewDigestAccountWorker(
		func(context.Context, uuid.UUID, time.Time, bool) (string, error) {
			return "terminal_unpersisted", errFmt(jobs.ErrDigestTerminalUnpersisted)
		}, 0, nil)

	// A job created well beyond the bounded re-drive window.
	job := &river.Job[jobs.DigestAccountArgs]{
		JobRow: &rivertype.JobRow{
			ID:        7,
			CreatedAt: time.Now().Add(-2 * jobs.DigestTerminalRedriveWindow),
		},
		Args: jobs.DigestAccountArgs{Account: uuid.New(), BusinessDay: "2026-03-11"},
	}
	err := w.Work(context.Background(), job)
	var snooze *river.JobSnoozeError
	if errors.As(err, &snooze) {
		t.Fatal("the worker snoozed past the bounded re-drive window; one stuck account/day would hold a digest slot forever")
	}
	if !errors.Is(err, jobs.ErrDigestTerminalRedriveWindowElapsed) {
		t.Fatalf("worker returned %v, want the distinct bounded %v", err, jobs.ErrDigestTerminalRedriveWindowElapsed)
	}
	// The original cause is still reachable, so the failing seam stays diagnosable.
	if !errors.Is(err, jobs.ErrDigestTerminalUnpersisted) {
		t.Fatal("the capped failure dropped its cause; the failing seam must stay nameable")
	}
}

// TestDigestAccountWorker_SnoozesInsideTheRedriveWindow is the negative half: a FRESH
// job still parks, so a transient database blip is ridden out rather than burning the
// account's bounded retry budget.
func TestDigestAccountWorker_SnoozesInsideTheRedriveWindow(t *testing.T) {
	w := jobs.NewDigestAccountWorker(
		func(context.Context, uuid.UUID, time.Time, bool) (string, error) {
			return "terminal_unpersisted", errFmt(jobs.ErrDigestTerminalUnpersisted)
		}, 0, nil)

	job := &river.Job[jobs.DigestAccountArgs]{
		JobRow: &rivertype.JobRow{ID: 8, CreatedAt: time.Now()},
		Args:   jobs.DigestAccountArgs{Account: uuid.New(), BusinessDay: "2026-03-11"},
	}
	var snooze *river.JobSnoozeError
	if err := w.Work(context.Background(), job); !errors.As(err, &snooze) {
		t.Fatalf("a fresh job returned %v, want a JobSnooze inside the bounded re-drive window", err)
	}
}

// TestNewClient_SoftStopGivesRunningJobsABoundedDrainWindow proves the graceful-stop
// contract at the client boundary. Cancelling the context passed to Start — exactly what
// SIGTERM does on every routine deploy — must NOT hard-cancel running job contexts
// immediately; it must open a BOUNDED drain window first, so an attempt already talking
// to the relay can finish instead of being killed mid-send. Without SoftStopTimeout
// River treats a cancelled Start context as StopAndCancel (river v0.40 client.go).
func TestNewClient_SoftStopGivesRunningJobsABoundedDrainWindow(t *testing.T) {
	if jobs.StopGrace <= jobs.SoftStopTimeout {
		t.Fatalf("StopGrace %s must exceed SoftStopTimeout %s, or the caller cuts the drain window short",
			jobs.StopGrace, jobs.SoftStopTimeout)
	}
	ctx := context.Background()
	pool := newPool(t)

	entered := make(chan struct{})
	survived := make(chan bool, 1)
	workers, err := jobs.NewWorkers(nil, jobs.ExecutionRunners{
		DigestAccount: func(c context.Context, _ uuid.UUID, _ time.Time, _ bool) (string, error) {
			close(entered)
			// Outlive an IMMEDIATE hard cancel, then report whether the context was
			// still live — i.e. whether a drain window actually existed.
			select {
			case <-c.Done():
				survived <- false
			case <-time.After(jobs.SoftStopTimeout / 2):
				survived <- true
			}
			return "noop", nil
		},
		DigestAccountTimeout: jobs.DigestAccountMaxTimeout,
	})
	if err != nil {
		t.Fatalf("workers: %v", err)
	}
	client, err := jobs.NewClient(pool, workers, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	startCtx, cancelStart := context.WithCancel(ctx)
	defer cancelStart()
	if err := client.Start(startCtx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), jobs.StopGrace)
		defer cancel()
		_ = client.Stop(stopCtx)
	})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := jobs.EnqueueDigestAccountTx(ctx, client, tx, uuid.New(), time.Now().UTC()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the digest attempt never started")
	}
	cancelStart() // the SIGTERM

	select {
	case ok := <-survived:
		if !ok {
			t.Fatal("cancelling the Start context hard-cancelled the running attempt immediately; a deploy would kill in-flight sends with no drain window")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the attempt never reported whether it survived the stop")
	}
}

// TestDigestAccountArgs_PinnedDayRoundTrips proves the pinned day survives the JSON
// wire format exactly — the property that keeps a delayed delivery on its ORIGINAL
// window instead of whichever day is current when it finally runs.
func TestDigestAccountArgs_PinnedDayRoundTrips(t *testing.T) {
	want := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)
	args := jobs.DigestAccountArgs{Account: uuid.New(), BusinessDay: want.Format(jobs.BusinessDayFormat)}
	got, err := args.Day()
	if err != nil {
		t.Fatalf("parse pinned day: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("pinned day round-tripped to %s, want %s", got, want)
	}
}

// TestDigestAccountArgs_InsertOptsIsolateTheQueue pins the isolation contract: digest
// work is queued away from the shared default queue, carries a bounded retry budget,
// and is unique per (account, business_day) only while IN FLIGHT — completed and
// discarded jobs must never block the owned recovery pass from re-enqueueing an
// account/day whose durable row is still nonterminal.
func TestDigestAccountArgs_InsertOptsIsolateTheQueue(t *testing.T) {
	opts := jobs.DigestAccountArgs{}.InsertOpts()
	if opts.Queue != jobs.QueueDigestAccount || opts.Queue == river.QueueDefault {
		t.Fatalf("queue = %q, want the dedicated %q", opts.Queue, jobs.QueueDigestAccount)
	}
	if opts.MaxAttempts != jobs.DigestAccountMaxAttempts || opts.MaxAttempts <= 0 {
		t.Fatalf("max attempts = %d, want the bounded %d", opts.MaxAttempts, jobs.DigestAccountMaxAttempts)
	}
	if !opts.UniqueOpts.ByArgs {
		t.Fatal("uniqueness must be by args so it is per (account, business_day)")
	}
	for _, s := range opts.UniqueOpts.ByState {
		if s == "completed" || s == "discarded" || s == "cancelled" {
			t.Fatalf("unique states include %q; a finished job would block the recovery re-enqueue of a still-nonterminal row", s)
		}
	}
}

// errFmt wraps the sentinel the way the digest service does.
func errFmt(sentinel error) error {
	return errors.Join(errors.New("notify: digest terminal state unpersisted"), sentinel)
}
