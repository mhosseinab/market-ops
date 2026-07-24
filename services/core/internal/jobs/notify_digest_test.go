package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

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
