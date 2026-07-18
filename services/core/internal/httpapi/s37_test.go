package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
)

// fakeGuardrail is a GuardrailService stub for transport tests.
type fakeGuardrail struct {
	view guardrail.ConfigView
	err  error
}

func (f *fakeGuardrail) Get(context.Context, uuid.UUID) (guardrail.ConfigView, error) {
	return f.view, f.err
}
func (f *fakeGuardrail) Set(context.Context, uuid.UUID, audit.Actor, guardrail.Settings) (guardrail.ConfigView, error) {
	return f.view, f.err
}

// fakeWatchlist is a WatchlistService stub for transport tests.
type fakeWatchlist struct {
	entries []db.WatchlistEntry
	entry   db.WatchlistEntry
	err     error
}

func (f *fakeWatchlist) List(context.Context, uuid.UUID) ([]db.WatchlistEntry, error) {
	return f.entries, f.err
}
func (f *fakeWatchlist) Add(context.Context, uuid.UUID, uuid.UUID, audit.Actor) (db.WatchlistEntry, error) {
	return f.entry, f.err
}

// TestMachinePrincipalCannotWriteGuardrailsEditPriceOrBulkMint is the S37
// end-to-end (transport-level) twin of
// perm.TestGatewayCannotWriteGuardrailsEditPriceOrBulkMint: the read/Draft-only
// LLM machine credential, presented as a Bearer token against the SCREENS
// routes (not the dedicated /chat/cards/* Draft routes), must be REFUSED on
// guardrail write, edit-price, and bulk-preview/mint — never a 200, never a
// silent partial success. The selection-set version is always server-minted;
// there is nothing here for the machine plane to reach even if it tried.
func TestMachinePrincipalCannotWriteGuardrailsEditPriceOrBulkMint(t *testing.T) {
	fa := newFakeAuth()
	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithCookieSecure(false), WithGatewayToken(testGatewayToken),
		WithGuardrail(&fakeGuardrail{}), WithApproval(&fakeApproval{}))

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			"guardrail write", http.MethodPost, "/guardrails",
			`{"marketplaceAccountId":"` + uuid.New().String() + `","settings":{"contributionFloor":{"mantissa":100,"currency":"USD","exponent":-2},"movementCapBasisPoints":500,"cooldownSeconds":3600,"strategy":"match","strategyEnabled":true}}`,
		},
		{
			"edit-price", http.MethodPost, "/approvals/card/edit-price",
			`{"cardId":"` + uuid.New().String() + `","newPrice":{"mantissa":100,"currency":"USD","exponent":-2}}`,
		},
		{
			"bulk-preview/mint", http.MethodPost, "/selection-sets/preview",
			`{"marketplaceAccountId":"` + uuid.New().String() + `","name":"n","members":[{"variantId":"` + uuid.New().String() + `","recommendationId":"` + uuid.New().String() + `"}]}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+testGatewayToken)
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with the machine bearer token = %d, want 403 Forbidden (body=%s)",
					c.method, c.path, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestMachinePrincipalCanReadRecommendationDetailGuardrailsAndWatchlist proves
// the machine plane's L1 read envelope DOES cover the new consolidated reads —
// the containment is specifically on the three writes above, not a blanket
// denial of the new surface.
func TestMachinePrincipalCanReadRecommendationDetailGuardrailsAndWatchlist(t *testing.T) {
	fa := newFakeAuth()
	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithCookieSecure(false), WithGatewayToken(testGatewayToken),
		WithGuardrail(&fakeGuardrail{}), WithWatchlist(&fakeWatchlist{}), WithApproval(&fakeApproval{}))

	for _, path := range []string{
		"/recommendations/detail?recommendationId=" + uuid.New().String(),
		"/guardrails?marketplaceAccountId=" + uuid.New().String(),
		"/watchlist?marketplaceAccountId=" + uuid.New().String(),
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+testGatewayToken)
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Fatalf("GET %s with the machine bearer token = %d, want a permitted (non-401/403) status", path, rec.Code)
		}
	}
}
