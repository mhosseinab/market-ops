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

// validRecommendationRow is a minimal, well-formed db.Recommendation with
// valid required money fields — the base every corrupt-JSON test below
// mutates one field of.
func validRecommendationRow() db.Recommendation {
	return db.Recommendation{
		ID:                   uuid.New(),
		MarketplaceAccountID: uuid.New(),
		VariantID:            uuid.New(),
		LineageID:            uuid.New(),
		Version:              1,
		Objective:            "maximize_contribution",
		CurrentPriceMantissa: 100000,
		CurrentPriceCurrency: "IRR",
		CurrentPriceExponent: 0,
		Readiness:            "complete",
		EvidenceQuality:      "verified",
		Assumptions:          []byte(`["a"]`),
		Blockers:             []byte(`[]`),
		Inputs:               []byte(`[]`),
	}
}

// TestGetRecommendationDetail_CorruptJSONFailsClosed is the money/evidence
// silent-swallow fix: corrupt persisted JSONB for a field the wire
// RecommendationDetail schema marks `required` (assumptions, blockers,
// contributionDeductions) must return a 500 (actionable error), NEVER a
// 200-with-silently-emptied field. A degraded-but-200 response would present
// an incomplete PRC-001 record as complete.
func TestGetRecommendationDetailCorruptJSONFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*db.Recommendation)
	}{
		{"corrupt assumptions", func(r *db.Recommendation) { r.Assumptions = []byte(`not-json`) }},
		{"corrupt blockers", func(r *db.Recommendation) { r.Blockers = []byte(`{"not":`) }},
		{"corrupt contribution deductions (inputs)", func(r *db.Recommendation) { r.Inputs = []byte(`[{"Amount":`) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := validRecommendationRow()
			c.mutate(&row)
			srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(&fakeApproval{rec: row}))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/recommendations/detail?recommendationId="+row.ID.String(), nil)
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 (actionable error, not a silently-emptied 200), body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestGetRecommendationDetail_WellFormedJSONStillSucceeds is the positive
// control for the fix above: valid JSON in every field still returns 200.
func TestGetRecommendationDetailWellFormedJSONStillSucceeds(t *testing.T) {
	row := validRecommendationRow()
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(&fakeApproval{rec: row}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/recommendations/detail?recommendationId="+row.ID.String(), nil)
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

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
			`{"marketplaceAccountId":"` + uuid.New().String() + `","settings":{"contributionFloor":{"mantissa":"100","currency":"USD","exponent":-2},"movementCapBasisPoints":500,"cooldownSeconds":3600,"strategy":"match","strategyEnabled":true}}`,
		},
		{
			"edit-price", http.MethodPost, "/approvals/card/edit-price",
			`{"cardId":"` + uuid.New().String() + `","newPrice":{"mantissa":"100","currency":"USD","exponent":-2}}`,
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

// TestMachinePrincipalCannotReadRoutesWithoutADeclaringTypedTool pins the
// corrected §12.3 never-cut envelope after issue #26: the machine credential's
// authority must NOT exceed the typed tool registry manifest. The consolidated
// S37 reads /recommendations/detail (read.recommendation_detail), /guardrails
// (read.guardrails), and /watchlist (read.watchlist) are gated on L1 read
// actions that NO typed model-visible tool declares (the registry's read tools
// map onto exactly connector.inspect, read.connection_status, read.cost_readiness
// and read.current_strategy — services/llm/.../registry.py). Previously the
// machine grant set was computed from EVERY L1 Matrix action, so these routes
// were reachable by the machine token even though no reviewed LLM tool declared
// the capability — the exact over-grant issue #26 identifies. They must now be
// DENIED (403) at the wire for the machine principal, while remaining L1 reads
// for human sessions. Restoring a machine read of any such route requires adding
// a typed read tool that declares its perm_action AND regenerating
// contracts/llm_gateway_envelope.json (the cross-language drift test enforces
// exact equality).
func TestMachinePrincipalCannotReadRoutesWithoutADeclaringTypedTool(t *testing.T) {
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
		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s with the machine bearer token = %d, want 403 (no typed tool declares this read action — issue #26)", path, rec.Code)
		}
	}
}
