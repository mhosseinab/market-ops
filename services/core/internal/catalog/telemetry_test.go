package catalog

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
)

// TestSyncStreak_FailureSuccessInterleavedNeverTrips proves the never-cut
// property the old rolling-window alert could not express: a
// failure/success/failure/success/failure sequence must NEVER reach the trip
// threshold, because every success resets the consecutive-failure streak to zero
// (issue #146 acceptance test 1).
func TestSyncStreak_FailureSuccessInterleavedNeverTrips(t *testing.T) {
	tel := newSyncTelemetry(nil)
	acct := uuid.New()
	ctx := context.Background()

	seq := []SyncDisposition{SyncHTTP5xx, SyncSuccess, SyncHTTP5xx, SyncSuccess, SyncHTTP5xx}
	var peak int64
	for _, d := range seq {
		// Each disposition is a DISTINCT run (a new runID), as it is in production.
		v := tel.recordSyncResult(ctx, acct, uuid.New(), d)
		if v > peak {
			peak = v
		}
	}
	if peak >= 3 {
		t.Fatalf("F,S,F,S,F must never reach the trip threshold; peak streak was %d", peak)
	}
	if got := tel.streakFor(acct); got != 1 {
		t.Fatalf("after F,S,F,S,F the current streak should be 1 (one trailing failure), got %d", got)
	}
}

// TestSyncStreak_ThreeConsecutiveReachesThreshold proves three consecutive sync
// failures reach the documented trip threshold (issue #146 acceptance test 2).
func TestSyncStreak_ThreeConsecutiveReachesThreshold(t *testing.T) {
	tel := newSyncTelemetry(nil)
	acct := uuid.New()
	ctx := context.Background()

	got := []int64{
		tel.recordSyncResult(ctx, acct, uuid.New(), SyncHTTP5xx),
		tel.recordSyncResult(ctx, acct, uuid.New(), SyncTransport),
		tel.recordSyncResult(ctx, acct, uuid.New(), SyncTyped),
	}
	want := []int64{1, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("consecutive failure %d: streak = %d, want %d", i+1, got[i], want[i])
		}
	}
}

// TestSyncStreak_SuccessResetsExistingStreak proves a successful sync resets an
// already-accumulated streak to zero (issue #146 acceptance test 3).
func TestSyncStreak_SuccessResetsExistingStreak(t *testing.T) {
	tel := newSyncTelemetry(nil)
	acct := uuid.New()
	ctx := context.Background()

	tel.recordSyncResult(ctx, acct, uuid.New(), SyncHTTP5xx)
	tel.recordSyncResult(ctx, acct, uuid.New(), SyncHTTP4xx)
	if got := tel.streakFor(acct); got != 2 {
		t.Fatalf("precondition: streak should be 2, got %d", got)
	}
	if got := tel.recordSyncResult(ctx, acct, uuid.New(), SyncSuccess); got != 0 {
		t.Fatalf("a success must reset the streak to 0, got %d", got)
	}
}

// TestSyncStreak_SameRunCountedOnce is the blocker-1 regression (issue #146): a
// SINGLE run that fails across MULTIPLE retry attempts must advance the streak by
// exactly ONE, so a run River retries >=3 times can never drive the gauge to the
// page threshold on its own. A different run then advances the streak normally, and
// that run succeeding resets it and clears its per-run guard.
func TestSyncStreak_SameRunCountedOnce(t *testing.T) {
	tel := newSyncTelemetry(nil)
	acct := uuid.New()
	run := uuid.New()
	ctx := context.Background()

	// Same run, three failing attempts (River backoff retries) => streak 1, not 3.
	tel.recordSyncResult(ctx, acct, run, SyncHTTP5xx)
	tel.recordSyncResult(ctx, acct, run, SyncTransport)
	if got := tel.recordSyncResult(ctx, acct, run, SyncTyped); got != 1 {
		t.Fatalf("one run failing across 3 attempts must count once; streak = %d, want 1", got)
	}

	// A DISTINCT run that fails advances the streak.
	other := uuid.New()
	if got := tel.recordSyncResult(ctx, acct, other, SyncHTTP5xx); got != 2 {
		t.Fatalf("a second failed run must advance to 2; got %d", got)
	}

	// That run succeeding resets the streak and clears its guard, so a fresh run id
	// reusing the value (a new run) is counted again from a reset baseline.
	if got := tel.recordSyncResult(ctx, acct, other, SyncSuccess); got != 0 {
		t.Fatalf("a success must reset the streak to 0; got %d", got)
	}
}

// TestSyncStreak_PerAccountIsolation proves one account's failures never move
// another account's streak (per-account/connector scoping).
func TestSyncStreak_PerAccountIsolation(t *testing.T) {
	tel := newSyncTelemetry(nil)
	a, b := uuid.New(), uuid.New()
	ctx := context.Background()

	tel.recordSyncResult(ctx, a, uuid.New(), SyncHTTP5xx)
	tel.recordSyncResult(ctx, a, uuid.New(), SyncHTTP5xx)
	if got := tel.streakFor(b); got != 0 {
		t.Fatalf("account b's streak must be unaffected by account a's failures, got %d", got)
	}
}

// TestClassifySyncFailure_AllDispositions proves every documented non-200/failure
// disposition maps to a failure that increments the streak (issue #146 acceptance
// test 4): 4xx, 5xx, transport, and typed sync failures.
func TestClassifySyncFailure_AllDispositions(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want SyncDisposition
	}{
		{"typed payload failure", &connector.VariantsPayloadError{Page: 2, Reason: "missing items"}, SyncTyped},
		{"http 401", fmt.Errorf("connector: fetch variants page 1: unexpected status %d", 401), SyncHTTP4xx},
		{"http 429", fmt.Errorf("connector: fetch variants page 1: unexpected status %d", 429), SyncHTTP4xx},
		{"http 503", fmt.Errorf("connector: fetch variants page 1: unexpected status %d", 503), SyncHTTP5xx},
		{"transport", fmt.Errorf("connector: fetch variants page 1: dial tcp: connection refused"), SyncTransport},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifySyncFailure(tc.err)
			if got != tc.want {
				t.Fatalf("classifySyncFailure(%v) = %q, want %q", tc.err, got, tc.want)
			}
			if !got.failure() {
				t.Fatalf("disposition %q must be a failure", tc.want)
			}
		})
	}
	if !SyncSuccess.isSuccess() {
		t.Fatalf("SyncSuccess must be a success disposition")
	}
}

// TestDeriveStreaks_FromDurableState proves the restart re-derivation: after a
// process restart the in-memory streak is rebuilt from the durable, ordered
// sync-run state so a real failing streak is never silently zeroed (issue #146
// acceptance test 6). Rows arrive newest-first per account.
func TestDeriveStreaks_FromDurableState(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	aFail1, aRun2 := uuid.New(), uuid.New()
	rows := []SyncRunOutcome{
		// account a: two trailing failures since the last completed run => streak 2.
		{Account: a, RunID: aFail1, Status: "failed", HasError: true},
		{Account: a, RunID: aRun2, Status: "running", HasError: true}, // an interrupted retry counts as a failure
		{Account: a, RunID: uuid.New(), Status: "completed", HasError: false},
		{Account: a, RunID: uuid.New(), Status: "failed", HasError: true},
		// account b: most recent run completed => streak 0.
		{Account: b, RunID: uuid.New(), Status: "completed", HasError: false},
		{Account: b, RunID: uuid.New(), Status: "failed", HasError: true},
		// account c: a clean in-flight run over an older failure => the failure still
		// counts (unresolved), the clean running row is neutral => streak 1.
		{Account: c, RunID: uuid.New(), Status: "running", HasError: false},
		{Account: c, RunID: uuid.New(), Status: "failed", HasError: true},
		{Account: c, RunID: uuid.New(), Status: "completed", HasError: false},
	}
	got, counted, resolved := deriveStreaks(rows)
	want := map[uuid.UUID]int64{a: 2, b: 0, c: 1}
	for acct, w := range want {
		if got[acct] != w {
			t.Fatalf("deriveStreaks account %s = %d, want %d", acct, got[acct], w)
		}
	}
	// The counted set carries the run ids that produced account a's trailing streak
	// (the failed run + the still-retrying running-with-error run), so a restart
	// re-seed of the per-run guard never double-counts that in-flight run.
	if _, ok := counted[aFail1]; !ok {
		t.Fatalf("counted set must include the trailing failed run %s", aFail1)
	}
	if _, ok := counted[aRun2]; !ok {
		t.Fatalf("counted set must include the still-retrying running-with-error run %s", aRun2)
	}
	// Every account here has a 'completed' run within its history, so its streak is
	// authoritative (the walk hit a success and stopped) rather than a bounded-suffix
	// lower bound. resolved must be true for all three.
	for _, acct := range []uuid.UUID{a, b, c} {
		if !resolved[acct] {
			t.Fatalf("account %s reached a completed run; resolved must be true", acct)
		}
	}
}

// TestDeriveStreaks_ResolvedFlag proves the issue #211 authority signal: an account
// whose newest-first walk hits a 'completed' run is resolved (the streak is exact);
// an account whose entire (bounded) suffix is failures never hit a success, so it is
// UNRESOLVED — the streak is a lower bound, not an authoritative value.
func TestDeriveStreaks_ResolvedFlag(t *testing.T) {
	resolvedAcct := uuid.New()
	unresolvedAcct := uuid.New()
	rows := []SyncRunOutcome{
		// resolvedAcct: one failure then a completed run => streak 1, resolved.
		{Account: resolvedAcct, RunID: uuid.New(), Status: "failed", HasError: true},
		{Account: resolvedAcct, RunID: uuid.New(), Status: "completed", HasError: false},
		// unresolvedAcct: an all-failure suffix, no completed run in view => streak 2,
		// UNRESOLVED (the walk ran off the end of the bounded suffix).
		{Account: unresolvedAcct, RunID: uuid.New(), Status: "failed", HasError: true},
		{Account: unresolvedAcct, RunID: uuid.New(), Status: "failed", HasError: true},
	}
	streaks, _, resolved := deriveStreaks(rows)
	if streaks[resolvedAcct] != 1 || !resolved[resolvedAcct] {
		t.Fatalf("resolvedAcct: streak=%d resolved=%v, want 1/true", streaks[resolvedAcct], resolved[resolvedAcct])
	}
	if streaks[unresolvedAcct] != 2 || resolved[unresolvedAcct] {
		t.Fatalf("unresolvedAcct: streak=%d resolved=%v, want 2/false", streaks[unresolvedAcct], resolved[unresolvedAcct])
	}
}

// TestSeedFromOutcomes_BoundExhaustionObservable proves the issue #211 no-silent-
// truncation invariant: when an account's bounded suffix is fully consumed by
// failures (count >= bound && unresolved) the seam emits a WARN with bounded
// technical identifiers, and it does NOT warn for a short-history account whose
// suffix is smaller than the bound (count < bound) — so a genuinely short history is
// never mis-flagged as a truncated streak. The seeded value in the exhausted case is
// the bound-sized lower bound, which itself trips the §20.1 wire (fail-closed).
func TestSeedFromOutcomes_BoundExhaustionObservable(t *testing.T) {
	const bound = 4

	exhausted := uuid.New() // bound failures, no completed => truncated, must warn.
	short := uuid.New()     // fewer than bound rows, no completed => must NOT warn.

	var rows []SyncRunOutcome
	for i := 0; i < bound; i++ {
		rows = append(rows, SyncRunOutcome{Account: exhausted, RunID: uuid.New(), Status: "failed", HasError: true})
	}
	for i := 0; i < bound-2; i++ {
		rows = append(rows, SyncRunOutcome{Account: short, RunID: uuid.New(), Status: "failed", HasError: true})
	}

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	tel := newSyncTelemetry(slog.New(handler))

	tel.seedFromOutcomes(context.Background(), rows, bound)

	logs := buf.String()
	if !strings.Contains(logs, "bound") || !strings.Contains(logs, exhausted.String()) {
		t.Fatalf("exhausted account must emit a bounded WARN with its account_id and bound; logs=%q", logs)
	}
	if strings.Contains(logs, short.String()) {
		t.Fatalf("short-history account (count<bound) must NOT be flagged as truncated; logs=%q", logs)
	}
	// The exhausted account is seeded to the bound-sized lower bound, which trips the
	// §20.1 wire — never presented as a resolved zero/short streak.
	if got := tel.streakFor(exhausted); got != bound {
		t.Fatalf("exhausted account must seed a fail-closed lower bound of %d, got %d", bound, got)
	}
	if got := tel.streakFor(short); got != bound-2 {
		t.Fatalf("short account streak should be its exact %d, got %d", bound-2, got)
	}
}

// TestSeed_RestoresStreakAcrossRestart proves seeding the tracker with derived
// streaks makes a subsequent failure continue the durable streak rather than
// starting from zero.
func TestSeed_RestoresStreakAcrossRestart(t *testing.T) {
	tel := newSyncTelemetry(nil)
	acct := uuid.New()
	ctx := context.Background()

	tel.seed(map[uuid.UUID]int64{acct: 2}, nil)
	if got := tel.streakFor(acct); got != 2 {
		t.Fatalf("seed should restore streak 2, got %d", got)
	}
	// A NEW run (its id was not in the seeded guard) advances the seeded streak.
	if got := tel.recordSyncResult(ctx, acct, uuid.New(), SyncHTTP5xx); got != 3 {
		t.Fatalf("a failure after a seeded streak of 2 should reach 3, got %d", got)
	}
}

// TestSeed_SeededRunNotDoubleCounted proves the blocker-1 restart guard: after a
// restart re-seeds both the streak AND the run ids that produced it, a further
// failing attempt of one of those SAME runs (River still retrying it) does NOT
// advance the streak a second time — the live value stays equal to the durable one.
func TestSeed_SeededRunNotDoubleCounted(t *testing.T) {
	tel := newSyncTelemetry(nil)
	acct := uuid.New()
	inflight := uuid.New()
	ctx := context.Background()

	// Durable state re-derived to streak 3, with `inflight` among the counted runs.
	tel.seed(map[uuid.UUID]int64{acct: 3}, map[uuid.UUID]struct{}{inflight: {}})
	if got := tel.streakFor(acct); got != 3 {
		t.Fatalf("seed should restore streak 3, got %d", got)
	}
	// The same in-flight run failing again post-restart must be idempotent.
	if got := tel.recordSyncResult(ctx, acct, inflight, SyncHTTP5xx); got != 3 {
		t.Fatalf("a retry of an already-counted run must not double-count; got %d, want 3", got)
	}
}
