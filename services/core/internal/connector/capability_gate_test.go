package connector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// panicTransport fails the test the instant any HTTP request is attempted. It is
// the strongest possible proof that a capability gate blocked catalog sync
// BEFORE any DK contact (and before any token decryption).
type panicTransport struct{ t *testing.T }

func (p panicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p.t.Fatalf("capability gate did not block: DK transport invoked for %s", req.URL)
	return nil, nil
}

// countingTransport records how many HTTP requests were made and serves a valid
// single-page variants envelope, so the positive control can assert the request
// actually reached the transport.
type countingTransport struct{ calls atomic.Int64 }

func (c *countingTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	body := `{"status":"ok","data":{"items":[{"product_id":1,"id":2,"product_variant_id":3,"title":"t","price_sale":100}],"pager":{"page":1,"total_pages":1,"total_rows":1}}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

// seedConnected puts a connected account with VALID sealed tokens and the given
// capability states into the fake store. Every capability starts Unknown; the
// overrides then set specific states. Valid tokens ensure the ONLY thing that
// can block FetchVariantsPage is the capability gate.
func seedConnected(t *testing.T, store *fakeStore, cipher *Cipher, acct uuid.UUID, overrides map[Capability]State) {
	t.Helper()
	access, refresh, err := cipher.SealTokens(TokenSet{AccessToken: "access-tok", RefreshToken: "refresh-tok"})
	if err != nil {
		t.Fatalf("seal tokens: %v", err)
	}
	if _, err := store.UpsertConnectorConnection(context.Background(), db.UpsertConnectorConnectionParams{
		MarketplaceAccountID: acct,
		AccessTokenSealed:    access,
		RefreshTokenSealed:   refresh,
		KeyVersion:           cipher.Version(),
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	for _, c := range AllCapabilities() {
		if err := store.SeedConnectorCapability(context.Background(), db.SeedConnectorCapabilityParams{
			MarketplaceAccountID: acct,
			Capability:           string(c),
		}); err != nil {
			t.Fatalf("seed capability %s: %v", c, err)
		}
	}
	for c, st := range overrides {
		if _, err := store.SetConnectorCapabilityStatus(context.Background(), db.SetConnectorCapabilityStatusParams{
			MarketplaceAccountID: acct,
			Capability:           string(c),
			Status:               string(st),
		}); err != nil {
			t.Fatalf("set capability %s: %v", c, err)
		}
	}
}

func newDKClientWithTransport(t *testing.T, rt http.RoundTripper) *DKClient {
	t.Helper()
	dk, err := NewDKClient("http://dk.invalid", &http.Client{Transport: rt})
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	return dk
}

func newCipher(t *testing.T) *Cipher {
	t.Helper()
	cipher, err := NewCipherFromEnv(func(string) string { return testKeyB64() })
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return cipher
}

// TestFetchVariantsPageCapabilityGateBlocks proves the never-cut capability
// invariant on catalog sync: when EITHER required capability (OwnedOfferRead or
// CatalogRead) is in any non-Supported state, FetchVariantsPage fails closed
// with ErrCapabilityNotSupported and makes ZERO HTTP requests. A panic-on-use
// transport guarantees the gate runs before any DK contact or token decryption.
func TestFetchVariantsPageCapabilityGateBlocks(t *testing.T) {
	nonSupported := []State{Unknown, Unsupported, Degraded}
	required := []Capability{OwnedOfferRead, CatalogRead}

	for _, cap := range required {
		for _, st := range nonSupported {
			name := string(cap) + "_" + string(st)
			t.Run(name, func(t *testing.T) {
				store := newFakeStore()
				cipher := newCipher(t)
				acct := uuid.New()

				// Both required capabilities Supported, then knock ONE down to the
				// non-supported state under test.
				overrides := map[Capability]State{
					OwnedOfferRead: Supported,
					CatalogRead:    Supported,
				}
				overrides[cap] = st
				seedConnected(t, store, cipher, acct, overrides)

				dk := newDKClientWithTransport(t, panicTransport{t: t})
				svc := NewService(store, cipher, dk)

				_, err := svc.FetchVariantsPage(context.Background(), acct, 1, 50)
				if !errors.Is(err, ErrCapabilityNotSupported) {
					t.Fatalf("FetchVariantsPage err = %v, want ErrCapabilityNotSupported", err)
				}
			})
		}
	}
}

// TestFetchVariantsPageCapabilityGateAllows is the positive control: when BOTH
// required capabilities are Supported, the gate opens, the DK transport is
// invoked, and a page comes back.
func TestFetchVariantsPageCapabilityGateAllows(t *testing.T) {
	store := newFakeStore()
	cipher := newCipher(t)
	acct := uuid.New()

	seedConnected(t, store, cipher, acct, map[Capability]State{
		OwnedOfferRead: Supported,
		CatalogRead:    Supported,
	})

	rt := &countingTransport{}
	dk := newDKClientWithTransport(t, rt)
	svc := NewService(store, cipher, dk)

	page, err := svc.FetchVariantsPage(context.Background(), acct, 1, 50)
	if err != nil {
		t.Fatalf("FetchVariantsPage err = %v, want nil", err)
	}
	if rt.calls.Load() == 0 {
		t.Fatal("expected DK transport to be invoked when both capabilities Supported")
	}
	if len(page.Items) != 1 {
		t.Fatalf("page items = %d, want 1", len(page.Items))
	}
}
