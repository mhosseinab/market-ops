package connector

import (
	"context"
	"fmt"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// interruptibleStore wraps a fakeStore and forces SetConnectorCapabilityStatus to
// fail after failAfter successful calls, deterministically interrupting the probe
// persistence loop of a reconnect. It reproduces issue #13's step 3 ("interrupt
// probing after only some capabilities are evaluated") without a database.
type interruptibleStore struct {
	*fakeStore
	failAfter int
	setCalls  int
	setErr    error
}

func (s *interruptibleStore) SetConnectorCapabilityStatus(ctx context.Context, arg db.SetConnectorCapabilityStatusParams) (db.ConnectorCapability, error) {
	s.setCalls++
	if s.setCalls > s.failAfter {
		return db.ConnectorCapability{}, s.setErr
	}
	return s.fakeStore.SetConnectorCapabilityStatus(ctx, arg)
}

// TestReconnectInterruptedProbeLeavesUnprobedUnknown is the issue #13 regression
// (S9-STALE-CAPABILITY-RECONNECT). A first connect makes every capability
// Supported. A reconnect then interrupts probing after k capabilities are
// persisted — for EVERY k (interruption after each probe). Every capability NOT
// reprobed in the new generation must read Unknown, never a stale
// previous-generation Supported. Against the pre-fix code (seed uses ON CONFLICT
// DO NOTHING and nothing resets prior rows) the unprobed capabilities keep their
// old Supported state and this fails.
func TestReconnectInterruptedProbeLeavesUnprobedUnknown(t *testing.T) {
	order := AllCapabilities() // deterministic probe/persist order
	for k := 0; k < len(order); k++ {
		k := k
		t.Run(fmt.Sprintf("interrupt_after_%d_probes", k), func(t *testing.T) {
			srv := mockdk.NewServer(mockdk.DefaultConfig())
			defer srv.Close()

			base := newFakeStore()
			org, acct := newAccount(base)

			// First generation: connect happy so every read capability is Supported.
			if _, err := newTestService(t, base, srv.URL).Connect(context.Background(), org, acct, "auth-1"); err != nil {
				t.Fatalf("initial connect: %v", err)
			}
			for _, c := range []Capability{CatalogRead, OwnedOfferRead, StockRead, BuyboxRead, BoundaryRead, CommissionRead, SalesContextRead, ChangeFeed} {
				if st := base.caps[acct][string(c)]; st.Status != string(Supported) {
					t.Fatalf("precondition: %s = %s, want supported", c, st.Status)
				}
			}

			// Second generation: reconnect but interrupt after exactly k probes persist.
			interrupt := &interruptibleStore{fakeStore: base, failAfter: k, setErr: context.Canceled}
			svc2 := newTestService(t, interrupt, srv.URL)
			if _, err := svc2.Connect(context.Background(), org, acct, "auth-2"); err == nil {
				t.Fatal("interrupted reconnect should surface the persist error, got nil")
			}

			// Capabilities at index >= k were never reprobed this generation. Each
			// must be Unknown (the atomic invalidation), not a stale first-generation
			// Supported, whether inspected in storage or through a reader.
			snap, err := svc2.Status(context.Background(), org, acct)
			if err != nil {
				t.Fatalf("status after interrupted reconnect: %v", err)
			}
			for _, c := range order[k:] {
				row := base.caps[acct][string(c)]
				if row.Status != string(Unknown) {
					t.Errorf("unprobed %s = %s, want unknown (no stale supported across generations)", c, row.Status)
				}
				if row.LastVerifiedAt.Valid {
					t.Errorf("unprobed %s retained a last_verified_at across generations", c)
				}
				if snap.Registry.IsSupported(c) {
					t.Errorf("reader saw stale supported %s from a previous generation", c)
				}
			}
		})
	}
}

// TestReconnectReducedPermissionsDropsStaleSupported covers the acceptance
// "reduced permissions" case: a fully-Supported account is reconnected against
// credentials whose scope no longer grants some capabilities. Those capabilities
// must reflect the new generation (Unsupported), never a stale Supported.
func TestReconnectReducedPermissionsDropsStaleSupported(t *testing.T) {
	// First generation: everything Supported.
	happy := mockdk.NewServer(mockdk.DefaultConfig())
	defer happy.Close()
	store := newFakeStore()
	org, acct := newAccount(store)
	svc := newTestService(t, store, happy.URL)
	if _, err := svc.Connect(context.Background(), org, acct, "auth-1"); err != nil {
		t.Fatalf("initial connect: %v", err)
	}

	// Second generation: reduced scope — catalog_read and stock_read now 403.
	reduced := mockdk.DefaultConfig()
	reduced.PerCap[string(CatalogRead)] = mockdk.ModeForbidden
	reduced.PerCap[string(StockRead)] = mockdk.ModeForbidden
	reducedSrv := mockdk.NewServer(reduced)
	defer reducedSrv.Close()
	svc2 := newTestService(t, store, reducedSrv.URL)

	snap, err := svc2.Refresh(context.Background(), org, acct)
	if err != nil {
		t.Fatalf("reconnect refresh: %v", err)
	}
	for _, c := range []Capability{CatalogRead, StockRead} {
		if st := snap.Registry.Status(c); st.State != Unsupported {
			t.Errorf("reduced %s = %s, want unsupported (no stale supported)", c, st.State)
		}
		if snap.Registry.IsSupported(c) {
			t.Errorf("reduced %s still reads Supported after scope drop", c)
		}
	}
	// Still-granted capabilities remain Supported in the new generation.
	if !snap.Registry.IsSupported(OwnedOfferRead) {
		t.Error("owned_offer_read should stay supported under the new generation")
	}
}

// TestReconnectFullSuccessPopulatesActiveGeneration proves a successful reconnect
// leaves a complete, current generation: every read capability Supported with a
// fresh last-verified time, none left Unknown by the atomic invalidation.
func TestReconnectFullSuccessPopulatesActiveGeneration(t *testing.T) {
	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	store := newFakeStore()
	org, acct := newAccount(store)
	svc := newTestService(t, store, srv.URL)
	if _, err := svc.Connect(context.Background(), org, acct, "auth-1"); err != nil {
		t.Fatalf("initial connect: %v", err)
	}
	snap, err := svc.Refresh(context.Background(), org, acct)
	if err != nil {
		t.Fatalf("reconnect refresh: %v", err)
	}
	for _, c := range []Capability{CatalogRead, OwnedOfferRead, StockRead, BuyboxRead, BoundaryRead, CommissionRead, SalesContextRead, ChangeFeed} {
		st := snap.Registry.Status(c)
		if st.State != Supported {
			t.Errorf("%s after full reconnect = %s, want supported", c, st.State)
		}
		if st.LastVerified == nil {
			t.Errorf("%s supported without a fresh last-verified time", c)
		}
	}
}
