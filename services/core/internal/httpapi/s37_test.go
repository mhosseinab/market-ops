package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
	"github.com/mhosseinab/market-ops/services/core/internal/watchlist"
)

// postEditPrice drives the edit-price seam with a fake ApprovalService returning
// editErr and the given wire newPrice body, returning the recorder.
func postEditPrice(t *testing.T, editErr error, newPriceJSON string) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(&fakeApproval{editErr: editErr}))
	body := fmt.Sprintf(`{"cardId":%q,"newPrice":%s}`, uuid.New(), newPriceJSON)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approvals/card/edit-price", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// TestEditApprovalCardPrice_CrossUnitRejectionMapsTo409 is the issue #306
// regression: the six-stage re-check refuses a cross-unit/cross-currency edited
// price. The DOMAIN now folds that edited-VALUE rejection into the single
// declined-edit class (recommendation.ErrEditedPriceRejected), so the transport
// keys on ONE decision class and maps it to a structured 409, never a 500 server
// fault. Fail-closed is unchanged: the recheck mints no card/parameter version.
func TestEditApprovalCardPriceCrossUnitRejectionMapsTo409(t *testing.T) {
	rec := postEditPrice(t, recommendation.ErrEditedPriceRejected, `{"mantissa":"1010","currency":"USD","exponent":0}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cross-unit edited price: status = %d, want 409 (declined edit, not a 500), body=%s", rec.Code, rec.Body.String())
	}
}

// TestEditApprovalCardPrice_ZeroValueRejectionMapsTo409 is the issue #306
// fix-cycle regression at the transport seam: a zero-priced edited value
// ("mantissa":"0") is accepted by moneyFromGateway (not a 400) and reaches the
// re-check, which declines it as an edited-VALUE rejection (the domain folds
// policy.ErrMissingReference into recommendation.ErrEditedPriceRejected). The
// seam MUST map that DECLINED edit to a structured 409, never fall through to a
// 500 server fault. The body carries the static declined-edit sentinel only — no
// free text (§4.6 free-text containment).
func TestEditApprovalCardPriceZeroValueRejectionMapsTo409(t *testing.T) {
	rec := postEditPrice(t, recommendation.ErrEditedPriceRejected, `{"mantissa":"0","currency":"USD","exponent":0}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("zero-value edited price: status = %d, want 409 (declined edit, not a 500), body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), recommendation.ErrEditedPriceRejected.Error()) {
		t.Fatalf("409 body must carry the static declined-edit sentinel; body=%s", rec.Body.String())
	}
}

// TestEditApprovalCardPrice_RawPolicySentinelStaysServerFault proves the other
// half of the issue #306 reconciliation: a RAW policy sentinel reaching the
// transport is NOT an edited-value rejection — under the domain classification it
// can only originate from resolving STORED, versioned config (a genuine
// server-data-integrity fault once a live rechecker is wired). It MUST remain a
// 500 and never be misclassified as a 409 declined edit. This is why the transport
// keys on the declined-edit class alone and no longer on shared policy sentinels.
func TestEditApprovalCardPriceRawPolicySentinelStaysServerFault(t *testing.T) {
	for _, sentinel := range []error{policy.ErrMissingReference, policy.ErrReferenceUnitMismatch} {
		rec := postEditPrice(t, sentinel, `{"mantissa":"1010","currency":"USD","exponent":0}`)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("raw policy sentinel %v: status = %d, want 500 (stored-config fault, never a 409)", sentinel, rec.Code)
		}
	}
}

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

// fakeGuardrail is a GuardrailService stub for transport tests (issue #237: the
// gateway depends only on the org-scoped methods).
type fakeGuardrail struct {
	view guardrail.ConfigView
	err  error
}

func (f *fakeGuardrail) GetForOrg(context.Context, uuid.UUID, uuid.UUID) (guardrail.ConfigView, error) {
	return f.view, f.err
}
func (f *fakeGuardrail) SetForOrg(context.Context, uuid.UUID, uuid.UUID, audit.Actor, guardrail.Settings, int64) (guardrail.ConfigView, error) {
	return f.view, f.err
}

// fakeWatchlist is a WatchlistService stub for transport tests (issue #237:
// org-scoped methods only).
type fakeWatchlist struct {
	entries []db.WatchlistEntry
	entry   db.WatchlistEntry
	err     error
}

func (f *fakeWatchlist) ListForOrg(context.Context, uuid.UUID, uuid.UUID) ([]db.WatchlistEntry, error) {
	return f.entries, f.err
}
func (f *fakeWatchlist) AddForOrg(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, audit.Actor) (db.WatchlistEntry, error) {
	return f.entry, f.err
}

// TestS37HandlersMapForeignAccountToUniform404 is the issue #237 transport-level
// proof (no database): when the org-scoping service reports the caller's org does
// not own the requested account (ErrAccountNotFound), every S37 handler — the two
// guardrail money/policy routes, both watchlist routes, and the market-conflict read
// — returns a uniform 404, never a 500 and never a 200 disclosure. It complements the
// DB-backed cross-tenant proof (tenant_scoping_s37_db_test.go) by pinning the
// handler's error-to-status mapping deterministically. An authenticated Owner is used
// (perm passes) so the 404 is the ownership guard's, not an auth rejection.
func TestS37HandlersMapForeignAccountToUniform404(t *testing.T) {
	acct := uuid.New().String()
	body := `{"marketplaceAccountId":"` + acct + `","settings":{"contributionFloor":{"mantissa":"100","currency":"USD","exponent":-2},"movementCapBasisPoints":500,"cooldownSeconds":3600,"strategy":"match","strategyEnabled":true}}`
	addBody := `{"marketplaceAccountId":"` + acct + `","variantId":"` + uuid.New().String() + `"}`

	cases := []struct {
		name    string
		method  string
		path    string
		reqBody string
		opt     Option
	}{
		{"GetGuardrails", http.MethodGet, "/guardrails?marketplaceAccountId=" + acct, "", WithGuardrail(&fakeGuardrail{err: guardrail.ErrAccountNotFound})},
		{"SetGuardrails", http.MethodPost, "/guardrails", body, WithGuardrail(&fakeGuardrail{err: guardrail.ErrAccountNotFound})},
		{"ListWatchlist", http.MethodGet, "/watchlist?marketplaceAccountId=" + acct, "", WithWatchlist(&fakeWatchlist{err: watchlist.ErrAccountNotFound})},
		{"AddWatchlistEntry", http.MethodPost, "/watchlist", addBody, WithWatchlist(&fakeWatchlist{err: watchlist.ErrAccountNotFound})},
		{"ListMarketConflicts", http.MethodGet, "/market/conflicts?marketplaceAccountId=" + acct, "", WithObservation(&fakeObservation{conflictErr: observation.ErrAccountNotFound})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, tok := systemOwnerServerForOrg(t, uuid.New(), c.opt)
			var rec *httptest.ResponseRecorder
			if c.method == http.MethodGet {
				rec = getJSON(t, srv, tok, c.path, nil)
			} else {
				rec = postJSON(t, srv, tok, c.path, c.reqBody)
			}
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s with a foreign account = %d, want 404 (uniform not-found, no existence oracle); body=%s", c.method, c.path, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestListMarketConflictsSurfacesPerRouteEvidence is the issue #94 transport proof:
// GET /market/conflicts surfaces each conflicted offer's per-route disagreeing
// evidence on the wire. An offer with two in-window routes reports state `available`
// with both routes' raw values; an offer whose comparison evidence is no longer
// inspectable reports the EXPLICIT fail-closed `unavailable` state with no routes —
// never a fabricated complete panel and never client-side inference.
func TestListMarketConflictsSurfacesPerRouteEvidence(t *testing.T) {
	acct := uuid.New()
	fo := &fakeObservation{conflicts: []observation.ConflictView{
		{
			Offer: db.ObservedOffer{ID: uuid.New(), TargetID: uuid.New(), MarketplaceAccountID: acct, OfferIdentity: "nv-1:seller-9", Quality: "conflicted", AvailabilityStatus: "in_stock"},
			Evidence: observation.ConflictEvidence{Available: true, Routes: []observation.ConflictRouteEvidence{
				{Route: "route_c", Value: "100", Unit: "IRR-rial", AvailabilityStatus: "in_stock"},
				{Route: "route_a", Value: "200", Unit: "IRR-rial", AvailabilityStatus: "in_stock"},
			}},
		},
		{
			Offer:    db.ObservedOffer{ID: uuid.New(), TargetID: uuid.New(), MarketplaceAccountID: acct, OfferIdentity: "nv-2:seller-9", Quality: "conflicted", AvailabilityStatus: "in_stock"},
			Evidence: observation.ConflictEvidence{Available: false},
		},
	}}
	srv, tok := systemOwnerServerForOrg(t, uuid.New(), WithObservation(fo))
	rec := getJSON(t, srv, tok, "/market/conflicts?marketplaceAccountId="+acct.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /market/conflicts = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var list gateway.ObservedOfferList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("want 2 conflicted offers, got %d", len(list.Items))
	}
	avail := list.Items[0]
	if avail.ConflictEvidence == nil || avail.ConflictEvidence.State != gateway.ConflictEvidenceStateAvailable {
		t.Fatalf("first offer must carry available conflictEvidence, got %+v", avail.ConflictEvidence)
	}
	if len(avail.ConflictEvidence.Routes) != 2 {
		t.Fatalf("available evidence must list 2 routes, got %d", len(avail.ConflictEvidence.Routes))
	}
	if avail.ConflictEvidence.Routes[0].Value != "100" || avail.ConflictEvidence.Routes[1].Value != "200" {
		t.Fatalf("per-route values not surfaced verbatim: %+v", avail.ConflictEvidence.Routes)
	}
	unavail := list.Items[1]
	if unavail.ConflictEvidence == nil || unavail.ConflictEvidence.State != gateway.ConflictEvidenceStateUnavailable {
		t.Fatalf("second offer must carry the explicit unavailable state, got %+v", unavail.ConflictEvidence)
	}
	if len(unavail.ConflictEvidence.Routes) != 0 {
		t.Fatalf("unavailable evidence must list no routes, got %d", len(unavail.ConflictEvidence.Routes))
	}
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
