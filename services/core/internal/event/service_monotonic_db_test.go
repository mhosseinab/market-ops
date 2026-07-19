package event_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/event"
)

// winCand builds a winning-state candidate for a fixed variant with an explicit
// detection instant and evidence ref, so a test can drive replay ordering by hand.
func winCand(t *testing.T, variant uuid.UUID, at time.Time, quality event.Quality, ref string) event.Candidate {
	t.Helper()
	c, ok := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: quality, Ref: ref},
		Now:      at, TTL: time.Hour,
	})
	if !ok {
		t.Fatal("winning-state detector did not fire")
	}
	return c
}

// openRowCount returns the number of open|updated market_events rows for a variant.
func openRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, variant uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1 AND state IN ('open','updated')`, variant).Scan(&n); err != nil {
		t.Fatalf("count open: %v", err)
	}
	return n
}

// TestReplayOlderEvidenceDoesNotRegress is the issue #68 DEFECT A reproduction:
// record a candidate at T2 (newer), then replay the SAME dedup key at T1 (older).
// The older replay must NOT regress the open event — last_evidence_at and the
// cited evidence stay at T2 — it produces ZERO new rows, and RecordFor returns
// SUCCESS (an ignored older replay is idempotent, not an error).
func TestReplayOlderEvidenceDoesNotRegress(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)

	t1 := time.Now().UTC().Truncate(time.Second)
	t2 := t1.Add(10 * time.Minute)

	// Record the NEWER evidence first (T2).
	rNew, err := svc.RecordFor(ctx, account, winCand(t, variant, t2, event.QualityVerified, "r2"))
	if err != nil {
		t.Fatalf("record newer: %v", err)
	}
	if rNew.Deduped {
		t.Fatal("first record must OPEN a new event")
	}

	// Replay the SAME dedup key with strictly-OLDER evidence (T1).
	rOld, err := svc.RecordFor(ctx, account, winCand(t, variant, t1, event.QualitySupported, "r1"))
	if err != nil {
		t.Fatalf("older replay must be SUCCESS (idempotent), got error: %v", err)
	}

	// The returned event must be the T2 event, unregressed.
	if rOld.Event.ID != rNew.Event.ID {
		t.Fatalf("older replay must reference the SAME open event: %v vs %v", rOld.Event.ID, rNew.Event.ID)
	}
	if !rOld.Event.LastEvidenceAt.Equal(t2) {
		t.Fatalf("last_evidence_at REGRESSED: got %v, want T2 %v", rOld.Event.LastEvidenceAt, t2)
	}
	if rOld.Event.EvidenceRef != "r2" {
		t.Fatalf("evidence REGRESSED: got ref %q, want r2", rOld.Event.EvidenceRef)
	}
	if rOld.Event.EvidenceQuality != string(event.QualityVerified) {
		t.Fatalf("evidence quality REGRESSED: got %q, want verified", rOld.Event.EvidenceQuality)
	}

	// Still exactly ONE open row and ZERO extra events rows for the variant.
	if got := openRowCount(t, ctx, pool, variant); got != 1 {
		t.Fatalf("older replay must keep exactly one open row, got %d", got)
	}
	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variant).Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if total != 1 {
		t.Fatalf("older replay must create ZERO new events rows; found %d", total)
	}
}

// TestReplayNewerEvidenceAdvances proves the forward direction still works: record
// at T1, then a NEWER replay at T2 advances the open event to T2's evidence, marks
// it 'updated', bumps evidence_update_count, and keeps exactly one row.
func TestReplayNewerEvidenceAdvances(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)

	t1 := time.Now().UTC().Truncate(time.Second)
	t2 := t1.Add(10 * time.Minute)

	rOld, err := svc.RecordFor(ctx, account, winCand(t, variant, t1, event.QualitySupported, "r1"))
	if err != nil {
		t.Fatalf("record older: %v", err)
	}
	rNew, err := svc.RecordFor(ctx, account, winCand(t, variant, t2, event.QualityVerified, "r2"))
	if err != nil {
		t.Fatalf("record newer: %v", err)
	}
	if !rNew.Deduped {
		t.Fatal("a newer replay of the same key must DEDUP-update, not open a new event")
	}
	if rNew.Event.ID != rOld.Event.ID {
		t.Fatalf("newer replay must update the SAME event")
	}
	if rNew.Event.State != string(event.LifecycleUpdated) {
		t.Fatalf("newer replay must mark state 'updated', got %q", rNew.Event.State)
	}
	if rNew.Event.EvidenceUpdateCount != 1 {
		t.Fatalf("evidence_update_count = %d, want 1", rNew.Event.EvidenceUpdateCount)
	}
	if !rNew.Event.LastEvidenceAt.Equal(t2) || rNew.Event.EvidenceRef != "r2" {
		t.Fatalf("newer replay must advance to T2/r2, got %v/%q", rNew.Event.LastEvidenceAt, rNew.Event.EvidenceRef)
	}
	if got := openRowCount(t, ctx, pool, variant); got != 1 {
		t.Fatalf("want exactly one open row, got %d", got)
	}
}

// TestResolveThenRecordOpensFreshDeterministic proves the freed-key path under the
// single-statement upsert (issue #68 DEFECT B, deterministic ordering): once the
// open event is resolved out of the open|updated predicate, the very next record of
// the same dedup key OPENS a fresh event rather than being lost or deduped onto the
// terminal row.
func TestResolveThenRecordOpensFreshDeterministic(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	r1, err := svc.RecordFor(ctx, account, winCand(t, variant, now, event.QualitySupported, "r1"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := svc.Resolve(ctx, r1.Event.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Key is freed; a recurrence must open a NEW event, not be lost.
	r2, err := svc.RecordFor(ctx, account, winCand(t, variant, now.Add(time.Minute), event.QualitySupported, "r2"))
	if err != nil {
		t.Fatalf("reopen must not error (occurrence must not be lost): %v", err)
	}
	if r2.Deduped {
		t.Fatal("after resolution a recurrence must OPEN a fresh event, not dedup")
	}
	if r2.Event.ID == r1.Event.ID {
		t.Fatal("the fresh event must have a new id")
	}
	if got := openRowCount(t, ctx, pool, variant); got != 1 {
		t.Fatalf("want exactly one open row after reopen, got %d", got)
	}
}

// TestConcurrentSameKeyRecordsExactlyOneOpen proves race-safety under contention
// (issue #68 DEFECT B, -race): many goroutines record the SAME dedup key with
// varying detection instants concurrently. The atomic upsert must converge to
// EXACTLY ONE open row (no lost occurrence, no duplicate), no RecordFor errors, and
// the surviving row must carry the MAX (newest) evidence — monotonicity holds under
// concurrency, not just serial replay.
func TestConcurrentSameKeyRecordsExactlyOneOpen(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	base := time.Now().UTC().Truncate(time.Second)

	const n = 24
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Interleave older and newer instants around base.
			at := base.Add(time.Duration(i-n/2) * time.Minute)
			if _, err := svc.RecordFor(ctx, account, winCand(t, variant, at, event.QualitySupported, "r")); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent record must never error (no lost occurrence): %v", err)
	}
	if got := openRowCount(t, ctx, pool, variant); got != 1 {
		t.Fatalf("concurrent same-key records must converge to exactly ONE open row, got %d", got)
	}
	// The surviving row carries the newest evidence instant (base + (n-1-n/2) min).
	want := base.Add(time.Duration(n-1-n/2) * time.Minute)
	var last time.Time
	if err := pool.QueryRow(ctx, `SELECT last_evidence_at FROM market_events WHERE variant_id=$1 AND state IN ('open','updated')`, variant).Scan(&last); err != nil {
		t.Fatalf("read last_evidence_at: %v", err)
	}
	if !last.Equal(want) {
		t.Fatalf("surviving open row must hold the MAX evidence instant %v, got %v", want, last)
	}
}

// TestConcurrentRecordWithResolveNoLostError proves the insert→resolve race window
// is closed (issue #68 DEFECT B): a RecordFor of a key racing a concurrent Resolve
// of that key must never surface a lost-row error — it either updates the open event
// or opens a fresh one, deterministically, but is never dropped as an error.
func TestConcurrentRecordWithResolveNoLostError(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	// Seed an initial open event so a resolve has a target to race against.
	seed, err := svc.RecordFor(ctx, account, winCand(t, variant, now, event.QualitySupported, "seed"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		// Resolve the seeded event, freeing the key mid-flight.
		if _, err := svc.Resolve(ctx, seed.Event.ID); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		// Record the same key concurrently — must not be lost.
		if _, err := svc.RecordFor(ctx, account, winCand(t, variant, now.Add(time.Minute), event.QualityVerified, "race")); err != nil {
			errCh <- err
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("record racing resolve must never error (no lost occurrence): %v", err)
	}
}
