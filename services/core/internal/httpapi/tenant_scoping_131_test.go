package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/briefing"
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// This file is the issue #131 transport-boundary proof for the three read surfaces
// that trusted a request-supplied marketplaceAccountId / variantId / batchId / targetId
// WITHOUT an organization-ownership predicate: observation reads, cost reads, and the
// daily briefing. Each handler now derives the tenant scope from the AUTHENTICATED
// principal's organization (never the request param) and returns a UNIFORM not-found
// for a foreign resource (no existence oracle). The seam is proven at the mounted
// gateway router with the real permission middleware armed; the real organization-join
// is proven by the package-level DB suites (deferred to CI / postgres).

// expectNotFoundBody asserts a response is a fixed no-oracle not-found envelope.
func expectNotFoundBody(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	var env struct{ Code, Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != code {
		t.Fatalf("not-found code = %q, want %q (uniform no-oracle body)", env.Code, code)
	}
}

func getWithSession(srv *http.Server, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// --- Surface: observation reads (issue #131 unique contribution) -------------

// TestObservationReadsRejectForeignScope proves the three /observation read handlers
// (a) always pass the AUTHENTICATED organization to the service — never the request
// param — and (b) map a foreign/org-less scope to a uniform 404, while a same-tenant
// read succeeds. Org A can never read Org B's targets/offers/evidence by naming B's
// account or a target id.
func TestObservationReadsRejectForeignScope(t *testing.T) {
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	foreignAccount := uuid.New()
	foreignTarget := uuid.New()

	// Foreign account / org-less → the service reports ErrAccountNotFound; the handler
	// maps it to the uniform NOT_FOUND. A foreign+local cross-combo (a foreign account
	// with a real-looking target id) is covered by the observations case below.
	t.Run("foreign-account-targets", func(t *testing.T) {
		fo := &fakeObservation{scopeErr: observation.ErrAccountNotFound}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithObservation(fo))
		rec := getWithSession(srv, "/observation/targets?marketplaceAccountId="+foreignAccount.String())
		expectNotFoundBody(t, rec, "NOT_FOUND")
		if fo.lastOrg != orgID {
			t.Fatalf("service org = %v, want authenticated org %v (never request input)", fo.lastOrg, orgID)
		}
	})

	t.Run("foreign-account-observed-offers", func(t *testing.T) {
		fo := &fakeObservation{scopeErr: observation.ErrAccountNotFound}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithObservation(fo))
		rec := getWithSession(srv, "/observation/observed-offers?marketplaceAccountId="+foreignAccount.String())
		expectNotFoundBody(t, rec, "NOT_FOUND")
		if fo.lastOrg != orgID {
			t.Fatalf("service org = %v, want authenticated org %v", fo.lastOrg, orgID)
		}
	})

	// Observation evidence is a target-keyed list: a foreign target under the caller's
	// own account matches nothing and returns an EMPTY list (uniform, no oracle). The
	// handler must still pass the authenticated org and the target selector.
	t.Run("foreign-target-observations-empty", func(t *testing.T) {
		fo := &fakeObservation{} // no rows, no error → empty
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithObservation(fo))
		rec := getWithSession(srv, "/observation/observations?targetId="+foreignTarget.String())
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 empty list, body=%s", rec.Code, rec.Body.String())
		}
		var list gateway.ObservationList
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(list.Items) != 0 {
			t.Fatalf("foreign target must yield no evidence, got %d", len(list.Items))
		}
		if fo.lastOrg != orgID || fo.lastTarget != foreignTarget {
			t.Fatalf("org/target passthrough wrong: org=%v (want %v) target=%v (want %v)", fo.lastOrg, orgID, fo.lastTarget, foreignTarget)
		}
	})

	// Positive control: a same-tenant read returns the rows.
	t.Run("same-tenant-succeeds", func(t *testing.T) {
		fo := &fakeObservation{targets: []db.ObservationTarget{{ID: uuid.New(), MarketplaceAccountID: uuid.New(), Active: true}}}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithObservation(fo))
		rec := getWithSession(srv, "/observation/targets?marketplaceAccountId="+uuid.New().String())
		if rec.Code != http.StatusOK {
			t.Fatalf("same-tenant read status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var list gateway.ObservationTargetList
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(list.Items) != 1 {
			t.Fatalf("same-tenant targets = %d, want 1", len(list.Items))
		}
	})
}

// --- Surface: cost reads -----------------------------------------------------

// TestCostReadsRejectForeignScope proves the /cost/profiles, /cost/readiness, and
// /cost/import read handlers derive scope from the authenticated organization and
// return a uniform not-found for a foreign variant/batch — a foreign variant/batch is
// indistinguishable from a genuinely-unknown one (no existence oracle) — while a
// same-tenant read succeeds.
func TestCostReadsRejectForeignScope(t *testing.T) {
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	foreignVariant := uuid.New()
	foreignBatch := uuid.New()

	t.Run("foreign-variant-readiness", func(t *testing.T) {
		fc := &fakeCost{err: cost.ErrVariantNotFound}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithCost(fc))
		rec := getWithSession(srv, "/cost/readiness?variantId="+foreignVariant.String())
		expectNotFoundBody(t, rec, "VARIANT_NOT_FOUND")
		if fc.lastOrg != orgID {
			t.Fatalf("service org = %v, want authenticated org %v", fc.lastOrg, orgID)
		}
	})

	t.Run("foreign-batch-preview", func(t *testing.T) {
		fc := &fakeCost{err: cost.ErrBatchNotFound}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithCost(fc))
		rec := getWithSession(srv, "/cost/import?batchId="+foreignBatch.String())
		expectNotFoundBody(t, rec, "BATCH_NOT_FOUND")
		if fc.lastOrg != orgID {
			t.Fatalf("service org = %v, want authenticated org %v", fc.lastOrg, orgID)
		}
	})

	// Cost profiles is a variant-keyed list: a foreign variant matches nothing and
	// returns an EMPTY list (uniform, no oracle). The org must be the authenticated one.
	t.Run("foreign-variant-profiles-empty", func(t *testing.T) {
		fc := &fakeCost{} // no rows, no error → empty
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithCost(fc))
		rec := getWithSession(srv, "/cost/profiles?variantId="+foreignVariant.String())
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 empty list; body=%s", rec.Code, rec.Body.String())
		}
		var list gateway.CostProfileList
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(list.Items) != 0 {
			t.Fatalf("foreign variant must yield no cost profiles, got %d", len(list.Items))
		}
		if fc.lastOrg != orgID || fc.lastVariant != foreignVariant {
			t.Fatalf("org/variant passthrough wrong: org=%v (want %v) variant=%v (want %v)", fc.lastOrg, orgID, fc.lastVariant, foreignVariant)
		}
	})

	// Org-less caller (degenerate) fails closed as a uniform not-found on every read.
	t.Run("org-less-readiness-fails-closed", func(t *testing.T) {
		fc := &fakeCost{err: cost.ErrAccountNotFound}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithCost(fc))
		rec := getWithSession(srv, "/cost/readiness?variantId="+uuid.New().String())
		expectNotFoundBody(t, rec, "NOT_FOUND")
	})

	t.Run("same-tenant-readiness-succeeds", func(t *testing.T) {
		fc := &fakeCost{readiness: db.MarginReadiness{VariantID: uuid.New(), MarketplaceAccountID: uuid.New(), State: "missing"}}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithCost(fc))
		rec := getWithSession(srv, "/cost/readiness?variantId="+uuid.New().String())
		if rec.Code != http.StatusOK {
			t.Fatalf("same-tenant readiness status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// --- Surface: daily briefing (issue #131 unique contribution) ----------------

// TestBriefingRejectsForeignScope proves GET /briefing derives scope from the
// authenticated organization and returns the SAME uniform not-found for a foreign
// account that a genuinely-missing briefing returns — so a foreign account is
// indistinguishable from an own account with no briefing (no existence oracle) — while
// a same-tenant read succeeds.
func TestBriefingRejectsForeignScope(t *testing.T) {
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	foreignAccount := uuid.New()

	// Foreign account → briefing.ErrAccountNotFound → uniform NOT_FOUND.
	fbForeign := &fakeBriefing{err: briefing.ErrAccountNotFound}
	srvForeign := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithBriefing(fbForeign))
	recForeign := getWithSession(srvForeign, "/briefing?marketplaceAccountId="+foreignAccount.String()+"&businessDay=2026-07-18")
	expectNotFoundBody(t, recForeign, "NOT_FOUND")
	if fbForeign.lastOrg != orgID {
		t.Fatalf("service org = %v, want authenticated org %v (never request input)", fbForeign.lastOrg, orgID)
	}

	// Same-tenant read returns the briefing.
	eventID := uuid.New()
	fbOwn := &fakeBriefing{b: briefing.Briefing{
		AccountID: uuid.New(),
		Events:    []briefing.Event{{Rank: 1, EventID: eventID, EventType: "winning_state", Severity: "critical"}},
	}}
	srvOwn := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithBriefing(fbOwn))
	recOwn := getWithSession(srvOwn, "/briefing?marketplaceAccountId="+uuid.New().String()+"&businessDay=2026-07-18")
	if recOwn.Code != http.StatusOK {
		t.Fatalf("same-tenant briefing status = %d, want 200; body=%s", recOwn.Code, recOwn.Body.String())
	}
	var got gateway.DailyBriefing
	if err := json.Unmarshal(recOwn.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].EventId != eventID {
		t.Fatalf("same-tenant briefing events = %+v", got.Events)
	}
}

func TestLatestBriefingRejectsForeignScope(t *testing.T) {
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	foreignAccount := uuid.New()
	fb := &fakeBriefing{latestErr: briefing.ErrAccountNotFound}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithBriefing(fb))

	rec := getWithSession(
		srv,
		"/briefing/latest?marketplaceAccountId="+foreignAccount.String()+"&beforeBusinessDay=2026-07-20",
	)
	expectNotFoundBody(t, rec, "NOT_FOUND")
	if fb.lastOrg != orgID {
		t.Fatalf("latest-briefing service org = %v, want authenticated org %v", fb.lastOrg, orgID)
	}
}
