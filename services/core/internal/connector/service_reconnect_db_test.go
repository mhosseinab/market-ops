package connector_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// failingSetStore wraps *db.Queries and fails SetConnectorCapabilityStatus after
// failAfter successful calls, deterministically interrupting the probe-persist
// loop against a REAL Postgres. *db.Queries satisfies connector.Store; only the
// one method is overridden so every other query hits the database unchanged.
type failingSetStore struct {
	*db.Queries
	failAfter int
	n         int
	err       error
}

func (s *failingSetStore) SetConnectorCapabilityStatus(ctx context.Context, arg db.SetConnectorCapabilityStatusParams) (db.ConnectorCapability, error) {
	s.n++
	if s.n > s.failAfter {
		return db.ConnectorCapability{}, s.err
	}
	return s.Queries.SetConnectorCapabilityStatus(ctx, arg)
}

// TestReconnectInterruptedProbeLeavesUnprobedUnknownDB is the DB-backed issue #13
// reproduction (S9-STALE-CAPABILITY-RECONNECT), the distinguishing case: a first
// connect makes every capability Supported in Postgres; a reconnect then fails
// after the first probe persists. Against the real org-scoped SQL, every
// capability NOT reprobed in the new generation must read 'unknown' — the atomic
// invalidation (ResetConnectorCapability) — never a stale previous-generation
// 'supported' left behind by seed's ON CONFLICT DO NOTHING.
func TestReconnectInterruptedProbeLeavesUnprobedUnknownDB(t *testing.T) {
	q, _ := newDBQueries(t)
	cipher := newCipher(t)
	org, acct := seedAccount(t, q)
	ctx := context.Background()

	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}

	// First generation: connect happy — every read capability Supported in DB.
	if _, err := connector.NewService(q, cipher, dk).Connect(ctx, org, acct, "auth-1"); err != nil {
		t.Fatalf("initial connect: %v", err)
	}

	// Second generation: reconnect but fail after the first probe persists.
	interrupt := &failingSetStore{Queries: q, failAfter: 1, err: context.Canceled}
	if _, err := connector.NewService(interrupt, cipher, dk).Connect(ctx, org, acct, "auth-2"); err == nil {
		t.Fatal("interrupted reconnect should surface the persist error, got nil")
	}

	rows, err := q.ListConnectorCapabilities(ctx, db.ListConnectorCapabilitiesParams{MarketplaceAccountID: acct, OrganizationID: org})
	if err != nil {
		t.Fatalf("list caps: %v", err)
	}
	// The first probe (catalog_read, deterministic order) persisted for the new
	// generation; everything else must be 'unknown', never a stale 'supported'.
	supported := 0
	for _, r := range rows {
		switch r.Status {
		case "supported":
			supported++
		case "unknown":
			if r.LastVerifiedAt.Valid {
				t.Errorf("%s unknown but retained a last_verified_at across generations", r.Capability)
			}
		default:
			// unsupported/degraded from this generation are acceptable states.
		}
	}
	if supported > 1 {
		t.Errorf("%d capabilities Supported after interrupted reconnect; only the reprobed one may be — the rest carried stale supported", supported)
	}
}

// TestReconnectConcurrentReadsStayConsistentDB covers the acceptance "concurrent
// reads" case: while a reconnect drops scope for a capability, readers running
// Status in parallel must never regress from a new-generation state back to the
// previous generation's Supported. Each reader observes a monotonic view — once
// it sees catalog_read leave Supported, it never sees Supported again — and the
// final state is Unsupported.
func TestReconnectConcurrentReadsStayConsistentDB(t *testing.T) {
	q, _ := newDBQueries(t)
	cipher := newCipher(t)
	org, acct := seedAccount(t, q)
	ctx := context.Background()

	// First generation: everything Supported.
	happy := mockdk.NewServer(mockdk.DefaultConfig())
	defer happy.Close()
	dkHappy, err := connector.NewDKClient(happy.URL, nil)
	if err != nil {
		t.Fatalf("dk client happy: %v", err)
	}
	if _, err := connector.NewService(q, cipher, dkHappy).Connect(ctx, org, acct, "auth-1"); err != nil {
		t.Fatalf("initial connect: %v", err)
	}

	// Second generation: reduced scope — catalog_read now 403 (Unsupported).
	reduced := mockdk.DefaultConfig()
	reduced.PerCap[string(connector.CatalogRead)] = mockdk.ModeForbidden
	reducedSrv := mockdk.NewServer(reduced)
	defer reducedSrv.Close()
	dkReduced, err := connector.NewDKClient(reducedSrv.URL, nil)
	if err != nil {
		t.Fatalf("dk client reduced: %v", err)
	}
	svc2 := connector.NewService(q, cipher, dkReduced)

	const readers = 6
	var wg sync.WaitGroup
	regressions := make([]bool, readers)
	stop := make(chan struct{})
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			leftSupported := false
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap, err := svc2.Status(ctx, org, acct)
				if err != nil {
					continue
				}
				if snap.Registry.IsSupported(connector.CatalogRead) {
					if leftSupported {
						regressions[idx] = true // regressed to a stale generation
					}
				} else {
					leftSupported = true
				}
			}
		}(i)
	}

	// Perform the scope-dropping reconnect while readers observe.
	snap, err := svc2.Refresh(ctx, org, acct)
	if err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("reconnect refresh: %v", err)
	}
	close(stop)
	wg.Wait()

	for i, r := range regressions {
		if r {
			t.Errorf("reader %d observed catalog_read regress to a stale Supported generation", i)
		}
	}
	if snap.Registry.IsSupported(connector.CatalogRead) {
		t.Fatal("catalog_read still Supported after scope-dropping reconnect")
	}
	if st := snap.Registry.Status(connector.CatalogRead); st.State != connector.Unsupported {
		t.Fatalf("catalog_read final state = %s, want unsupported", st.State)
	}
}
