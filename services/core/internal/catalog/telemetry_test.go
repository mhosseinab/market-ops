package catalog

import (
	"context"
	"fmt"
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
		v := tel.recordSyncResult(ctx, acct, d)
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
		tel.recordSyncResult(ctx, acct, SyncHTTP5xx),
		tel.recordSyncResult(ctx, acct, SyncTransport),
		tel.recordSyncResult(ctx, acct, SyncTyped),
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

	tel.recordSyncResult(ctx, acct, SyncHTTP5xx)
	tel.recordSyncResult(ctx, acct, SyncHTTP4xx)
	if got := tel.streakFor(acct); got != 2 {
		t.Fatalf("precondition: streak should be 2, got %d", got)
	}
	if got := tel.recordSyncResult(ctx, acct, SyncSuccess); got != 0 {
		t.Fatalf("a success must reset the streak to 0, got %d", got)
	}
}

// TestSyncStreak_PerAccountIsolation proves one account's failures never move
// another account's streak (per-account/connector scoping).
func TestSyncStreak_PerAccountIsolation(t *testing.T) {
	tel := newSyncTelemetry(nil)
	a, b := uuid.New(), uuid.New()
	ctx := context.Background()

	tel.recordSyncResult(ctx, a, SyncHTTP5xx)
	tel.recordSyncResult(ctx, a, SyncHTTP5xx)
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
	rows := []SyncRunOutcome{
		// account a: two trailing failures since the last completed run => streak 2.
		{Account: a, Status: "failed", HasError: true},
		{Account: a, Status: "running", HasError: true}, // an interrupted retry counts as a failure
		{Account: a, Status: "completed", HasError: false},
		{Account: a, Status: "failed", HasError: true},
		// account b: most recent run completed => streak 0.
		{Account: b, Status: "completed", HasError: false},
		{Account: b, Status: "failed", HasError: true},
		// account c: a clean in-flight run over an older failure => the failure still
		// counts (unresolved), the clean running row is neutral => streak 1.
		{Account: c, Status: "running", HasError: false},
		{Account: c, Status: "failed", HasError: true},
		{Account: c, Status: "completed", HasError: false},
	}
	got := deriveStreaks(rows)
	want := map[uuid.UUID]int64{a: 2, b: 0, c: 1}
	for acct, w := range want {
		if got[acct] != w {
			t.Fatalf("deriveStreaks account %s = %d, want %d", acct, got[acct], w)
		}
	}
}

// TestSeed_RestoresStreakAcrossRestart proves seeding the tracker with derived
// streaks makes a subsequent failure continue the durable streak rather than
// starting from zero.
func TestSeed_RestoresStreakAcrossRestart(t *testing.T) {
	tel := newSyncTelemetry(nil)
	acct := uuid.New()
	ctx := context.Background()

	tel.seed(map[uuid.UUID]int64{acct: 2})
	if got := tel.streakFor(acct); got != 2 {
		t.Fatalf("seed should restore streak 2, got %d", got)
	}
	if got := tel.recordSyncResult(ctx, acct, SyncHTTP5xx); got != 3 {
		t.Fatalf("a failure after a seeded streak of 2 should reach 3, got %d", got)
	}
}
