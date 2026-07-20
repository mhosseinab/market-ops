package catalog_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
)

// fourItems is the issue #8 reproduction fixture: the SAME four offers a run
// fetches over two pages of size two. Two runs for the same account fetching this
// unchanged catalog must never make one another look incomplete.
func fourItems() []connector.VariantItem {
	return []connector.VariantItem{
		item(200, 2000, 10, 100000),
		item(200, 2001, 11, 200000),
		item(201, 2002, 12, 300000),
		item(201, 2003, 13, 400000),
	}
}

// TestConcurrentSameAccountSyncSecondRunDeferred is the issue #8 reconciliation
// never-cut (§4.6) regression, exercised through the PRODUCTION DB queries with an
// explicit page interleaving — not goroutine timing.
//
// Reproduction (issue #8): two runs A & B, same account, same four offers in two
// pages, interleaved A-p1, (attempt B), finish A. Under the shared-marker design
// WITHOUT an account-scoped serialization boundary, B's pages would overwrite
// owned_offers.last_seen_run_id and make A's completed run report drift_count=2
// even though both fetched the same unchanged catalog.
//
// The DB-enforced serialization model (uq_catalog_sync_runs_inflight, migration
// 0027) makes the reconciliation run-safe by construction: the second same-account
// run is DEFERRED EXPLICITLY at creation (connector.ErrSyncAlreadyInFlight) and can
// never interleave its marker writes, so A's drift describes only A's own complete
// fetch. This test asserts the second run is deferred and A completes with
// drift_count=0 (the serialization-model assertion permitted by issue #8's TDD note).
func TestConcurrentSameAccountSyncSecondRunDeferred(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	// Run A begins and applies its first page (offers 2000, 2001) — it is now the
	// account's in-flight run, mid-fetch.
	sA := catalog.NewSyncer(pool, newFakeSource(fourItems(), 2), 2)
	runA, err := sA.Start(ctx, account, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := sA.ResumeN(ctx, account, runA, 1); err != nil {
		t.Fatalf("A page 1: %v", err)
	}

	// Run B tries to start for the SAME account while A is still running (the
	// interleaving point B-p1/B-p2). It must be DEFERRED EXPLICITLY — never allowed
	// to create a second in-flight run whose page writes would corrupt A's marker.
	sB := catalog.NewSyncer(pool, newFakeSource(fourItems(), 2), 2)
	_, errB := sB.Start(ctx, account, catalog.KindInitial)
	if !errors.Is(errB, connector.ErrSyncAlreadyInFlight) {
		t.Fatalf("second same-account Start err=%v, want connector.ErrSyncAlreadyInFlight (deferred, not a raw fault)", errB)
	}

	// A finishes its complete fetch. Because B could never interleave, A's
	// reconciliation sees exactly A's own four offers → zero false drift.
	if err := sA.Resume(ctx, account, runA); err != nil {
		t.Fatalf("A resume to completion: %v", err)
	}
	rowA, _ := q.GetCatalogSyncRun(ctx, runA)
	if rowA.Status != "completed" || rowA.DriftCount != 0 {
		t.Fatalf("run A status=%s drift=%d, want completed/0 (no false drift from a concurrent same-account run)", rowA.Status, rowA.DriftCount)
	}

	// Exactly one run row exists for the account: the deferred B created nothing.
	latest, _ := q.GetLatestCatalogSyncRun(ctx, account)
	if latest.ID != runA {
		t.Fatalf("latest run=%s, want A=%s (deferred B must not have created a run)", latest.ID, runA)
	}
	if _, v, _, o, _ := counts(t, q, account); v != 4 || o != 4 {
		t.Fatalf("variants=%d offers=%d, want 4/4", v, o)
	}
}

// TestConcurrentDifferentAccountsRunConcurrently proves the serialization boundary
// is ACCOUNT-SCOPED, not global: two different accounts each hold an in-flight run
// at the same time, and both reconcile to zero drift. A global lock would fail this.
func TestConcurrentDifferentAccountsRunConcurrently(t *testing.T) {
	pool, q := newPool(t)
	acct1 := seedAccount(t, q)
	acct2 := seedAccount(t, q)
	ctx := context.Background()

	s1 := catalog.NewSyncer(pool, newFakeSource(fourItems(), 2), 2)
	run1, err := s1.Start(ctx, acct1, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start acct1: %v", err)
	}
	// acct1's run is running; acct2 must still be able to start concurrently.
	s2 := catalog.NewSyncer(pool, newFakeSource(fourItems(), 2), 2)
	run2, err := s2.Start(ctx, acct2, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start acct2 while acct1 running must NOT be serialized: %v", err)
	}
	if run1 == run2 {
		t.Fatalf("cross-account runs collapsed to one id %s", run1)
	}

	// Both are simultaneously in-flight, then both complete with zero drift.
	if err := s1.Resume(ctx, acct1, run1); err != nil {
		t.Fatalf("resume acct1: %v", err)
	}
	if err := s2.Resume(ctx, acct2, run2); err != nil {
		t.Fatalf("resume acct2: %v", err)
	}
	for _, tc := range []struct {
		acct uuid.UUID
		run  uuid.UUID
	}{{acct1, run1}, {acct2, run2}} {
		row, _ := q.GetCatalogSyncRun(ctx, tc.run)
		if row.Status != "completed" || row.DriftCount != 0 {
			t.Fatalf("account run %s status=%s drift=%d, want completed/0", tc.run, row.Status, row.DriftCount)
		}
	}
}

// TestRetryDoesNotBypassSerialization proves a retry/resume cannot bypass the
// account boundary: while a run is mid-flight, every fresh claim for the account is
// deferred, and the in-flight run's own retried Resume completes against a stable
// account/run view (drift 0) without creating a second run.
func TestRetryDoesNotBypassSerialization(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	sA := catalog.NewSyncer(pool, newFakeSource(fourItems(), 2), 2)
	runA, err := sA.Start(ctx, account, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	// A applies page 1 then is "interrupted" (still running).
	if err := sA.ResumeN(ctx, account, runA, 1); err != nil {
		t.Fatalf("A page 1: %v", err)
	}

	// Two independent retry attempts try to claim the account afresh — both deferred,
	// neither creates a bypassing run.
	for i := 0; i < 2; i++ {
		sRetry := catalog.NewSyncer(pool, newFakeSource(fourItems(), 2), 2)
		if _, e := sRetry.Start(ctx, account, catalog.KindIncremental); !errors.Is(e, connector.ErrSyncAlreadyInFlight) {
			t.Fatalf("retry %d Start err=%v, want ErrSyncAlreadyInFlight (retry must not bypass the boundary)", i, e)
		}
	}

	// A's OWN resume (the legitimate retry of the same run) completes without a new
	// run and reconciles to zero.
	if err := sA.Resume(ctx, account, runA); err != nil {
		t.Fatalf("A resume: %v", err)
	}
	rowA, _ := q.GetCatalogSyncRun(ctx, runA)
	if rowA.Status != "completed" || rowA.DriftCount != 0 {
		t.Fatalf("run A status=%s drift=%d, want completed/0", rowA.Status, rowA.DriftCount)
	}
	latest, _ := q.GetLatestCatalogSyncRun(ctx, account)
	if latest.ID != runA {
		t.Fatalf("latest run=%s, want A=%s (no bypassing run created)", latest.ID, runA)
	}
}
