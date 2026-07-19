package connector

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// fakeSyncEnqueuer counts enqueue calls so a test can assert exactly-one / zero
// enqueue (issue #76 acceptance) without any job infrastructure.
type fakeSyncEnqueuer struct {
	calls  atomic.Int64
	err    error
	runID  uuid.UUID
	lastOr uuid.UUID
	lastAc uuid.UUID
}

func (f *fakeSyncEnqueuer) EnqueueIncrementalSync(_ context.Context, org, acc uuid.UUID) (uuid.UUID, error) {
	f.calls.Add(1)
	f.lastOr, f.lastAc = org, acc
	if f.err != nil {
		return uuid.Nil, f.err
	}
	if f.runID == uuid.Nil {
		return uuid.New(), nil
	}
	return f.runID, nil
}

// TestSyncCatalogUnsupportedCapabilityEnqueuesNothing is the never-cut negative:
// while catalog_read is NOT Supported (Unknown/Unsupported/Degraded), SyncCatalog
// fails closed with ErrCapabilityNotSupported and issues ZERO enqueue requests
// (§15.2 "Unknown never enables dependent logic"; issue #76 acceptance).
func TestSyncCatalogUnsupportedCapabilityEnqueuesNothing(t *testing.T) {
	for _, st := range []State{Unknown, Unsupported, Degraded} {
		t.Run(string(st), func(t *testing.T) {
			store := newFakeStore()
			cipher := newCipher(t)
			org, acct := uuid.New(), uuid.New()
			seedConnected(t, store, cipher, org, acct, map[Capability]State{CatalogRead: st})

			enq := &fakeSyncEnqueuer{}
			svc := newTestService(t, store, "http://dk.invalid")
			svc.SetSyncEnqueuer(enq)

			_, err := svc.SyncCatalog(context.Background(), org, acct)
			if !errors.Is(err, ErrCapabilityNotSupported) {
				t.Fatalf("SyncCatalog err = %v, want ErrCapabilityNotSupported", err)
			}
			if got := enq.calls.Load(); got != 0 {
				t.Fatalf("enqueue calls = %d, want 0 (fail closed)", got)
			}
		})
	}
}

// TestSyncCatalogSupportedEnqueuesOnce proves the happy path: catalog_read
// Supported with no in-flight run issues EXACTLY ONE idempotent enqueue and
// returns the reconciled status.
func TestSyncCatalogSupportedEnqueuesOnce(t *testing.T) {
	store := newFakeStore()
	cipher := newCipher(t)
	org, acct := uuid.New(), uuid.New()
	seedConnected(t, store, cipher, org, acct, map[Capability]State{CatalogRead: Supported})

	enq := &fakeSyncEnqueuer{}
	svc := newTestService(t, store, "http://dk.invalid")
	svc.SetSyncEnqueuer(enq)

	snap, err := svc.SyncCatalog(context.Background(), org, acct)
	if err != nil {
		t.Fatalf("SyncCatalog: %v", err)
	}
	if got := enq.calls.Load(); got != 1 {
		t.Fatalf("enqueue calls = %d, want 1", got)
	}
	if enq.lastOr != org || enq.lastAc != acct {
		t.Fatalf("enqueue scope = (%s,%s), want (%s,%s)", enq.lastOr, enq.lastAc, org, acct)
	}
	// Snapshot carries a durable sync-state view (never nil for a connected acct).
	if snap.CatalogSync == nil {
		t.Fatal("snapshot CatalogSync is nil, want a durable state")
	}
}

// TestSyncCatalogIdempotentWhileInFlight proves idempotency (PRD §9.1 never-cut):
// a sync already running is NEVER duplicated — SyncCatalog enqueues nothing and
// reports the current running state.
func TestSyncCatalogIdempotentWhileInFlight(t *testing.T) {
	store := newFakeStore()
	cipher := newCipher(t)
	org, acct := uuid.New(), uuid.New()
	seedConnected(t, store, cipher, org, acct, map[Capability]State{CatalogRead: Supported})
	store.setLatestSyncRun(acct, db.CatalogSyncRun{
		ID: uuid.New(), MarketplaceAccountID: acct, Kind: "incremental",
		Status: "running", StartedAt: time.Now().UTC(),
	})

	enq := &fakeSyncEnqueuer{}
	svc := newTestService(t, store, "http://dk.invalid")
	svc.SetSyncEnqueuer(enq)

	snap, err := svc.SyncCatalog(context.Background(), org, acct)
	if err != nil {
		t.Fatalf("SyncCatalog: %v", err)
	}
	if got := enq.calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0 (idempotent: in-flight run)", got)
	}
	if snap.CatalogSync == nil || snap.CatalogSync.State != SyncRunning {
		t.Fatalf("CatalogSync = %+v, want state running", snap.CatalogSync)
	}
}

// TestSyncCatalogTerminalRunReenqueues proves a completed/failed run is not
// in-flight: a fresh sync is enqueued exactly once.
func TestSyncCatalogTerminalRunReenqueues(t *testing.T) {
	for _, st := range []string{"completed", "failed"} {
		t.Run(st, func(t *testing.T) {
			store := newFakeStore()
			cipher := newCipher(t)
			org, acct := uuid.New(), uuid.New()
			seedConnected(t, store, cipher, org, acct, map[Capability]State{CatalogRead: Supported})
			store.setLatestSyncRun(acct, db.CatalogSyncRun{
				ID: uuid.New(), MarketplaceAccountID: acct, Kind: "incremental",
				Status: st, StartedAt: time.Now().UTC(),
			})

			enq := &fakeSyncEnqueuer{}
			svc := newTestService(t, store, "http://dk.invalid")
			svc.SetSyncEnqueuer(enq)

			if _, err := svc.SyncCatalog(context.Background(), org, acct); err != nil {
				t.Fatalf("SyncCatalog: %v", err)
			}
			if got := enq.calls.Load(); got != 1 {
				t.Fatalf("enqueue calls = %d, want 1 (terminal run is not in-flight)", got)
			}
		})
	}
}

// TestSyncCatalogUnwiredEnqueuerFailsClosed proves the fail-closed default: with
// no enqueuer wired, SyncCatalog returns ErrSyncUnavailable rather than
// pretending a sync was queued.
func TestSyncCatalogUnwiredEnqueuerFailsClosed(t *testing.T) {
	store := newFakeStore()
	cipher := newCipher(t)
	org, acct := uuid.New(), uuid.New()
	seedConnected(t, store, cipher, org, acct, map[Capability]State{CatalogRead: Supported})

	svc := newTestService(t, store, "http://dk.invalid") // no SetSyncEnqueuer
	_, err := svc.SyncCatalog(context.Background(), org, acct)
	if !errors.Is(err, ErrSyncUnavailable) {
		t.Fatalf("SyncCatalog err = %v, want ErrSyncUnavailable", err)
	}
}

// TestSyncCatalogForeignAccountNotFound proves the ownership guard runs before
// any capability read or enqueue (S8-AUTHZ-001): a cross-org account yields
// ErrAccountNotFound with zero enqueue.
func TestSyncCatalogForeignAccountNotFound(t *testing.T) {
	store := newFakeStore()
	cipher := newCipher(t)
	org, acct := uuid.New(), uuid.New()
	seedConnected(t, store, cipher, org, acct, map[Capability]State{CatalogRead: Supported})

	enq := &fakeSyncEnqueuer{}
	svc := newTestService(t, store, "http://dk.invalid")
	svc.SetSyncEnqueuer(enq)

	foreignOrg := uuid.New()
	_, err := svc.SyncCatalog(context.Background(), foreignOrg, acct)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("SyncCatalog err = %v, want ErrAccountNotFound", err)
	}
	if got := enq.calls.Load(); got != 0 {
		t.Fatalf("enqueue calls = %d, want 0 (foreign account)", got)
	}
}

// TestStatusMapsLatestSyncRun proves Status reports the durable run state as
// EVIDENCE — a completed run maps to SyncCompleted, distinct from capability
// support (issue #76: onboarding advances only on durable completion).
func TestStatusMapsLatestSyncRun(t *testing.T) {
	store := newFakeStore()
	cipher := newCipher(t)
	org, acct := uuid.New(), uuid.New()
	seedConnected(t, store, cipher, org, acct, map[Capability]State{CatalogRead: Supported})
	started := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	store.setLatestSyncRun(acct, db.CatalogSyncRun{
		ID: uuid.New(), MarketplaceAccountID: acct, Kind: "incremental",
		Status: "completed", StartedAt: started,
	})

	svc := newTestService(t, store, "http://dk.invalid")
	snap, err := svc.Status(context.Background(), org, acct)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if snap.CatalogSync == nil || snap.CatalogSync.State != SyncCompleted {
		t.Fatalf("CatalogSync = %+v, want state completed", snap.CatalogSync)
	}
	if snap.CatalogSync.LastRunAt == nil || !snap.CatalogSync.LastRunAt.Equal(started) {
		t.Fatalf("LastRunAt = %v, want %v", snap.CatalogSync.LastRunAt, started)
	}
}

// TestStatusNoRunReportsNone proves a connected account with NO sync run reads as
// SyncNone — capability support alone is never reported as a completed sync.
func TestStatusNoRunReportsNone(t *testing.T) {
	store := newFakeStore()
	cipher := newCipher(t)
	org, acct := uuid.New(), uuid.New()
	seedConnected(t, store, cipher, org, acct, map[Capability]State{CatalogRead: Supported})

	svc := newTestService(t, store, "http://dk.invalid")
	snap, err := svc.Status(context.Background(), org, acct)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if snap.CatalogSync == nil || snap.CatalogSync.State != SyncNone {
		t.Fatalf("CatalogSync = %+v, want state none", snap.CatalogSync)
	}
}
