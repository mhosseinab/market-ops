package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// connectorOperations is the set of /connector/* operations whose runtime
// (middleware.go routePolicies, all kindProtected) authenticates EXCLUSIVELY
// from the secure httpOnly mo_session cookie via sessionToken(r). The
// Authorization header is never evaluated for a connector caller: the
// connect/refresh/disconnect/catalog.sync actions are L2+ writes outside the
// machine gateway envelope (perm.GatewayCan denies them), and while GET
// /connector/status is additionally reachable by the internal LLM machine
// bearer, its FIRST-PARTY generated-client contract is the cookie. Issue #5:
// the published contract must therefore resolve every connector operation to
// cookieAuth, not the mis-declared root bearerAuth.
var connectorOperations = []struct{ method, path string }{
	{http.MethodPost, "/connector/connect"},
	{http.MethodPost, "/connector/refresh"},
	{http.MethodPost, "/connector/disconnect"},
	{http.MethodPost, "/connector/catalog/sync"},
	{http.MethodGet, "/connector/status"},
}

// effectiveSecurity returns the security requirements that apply to an
// operation: its own if declared, otherwise the document-level default (the
// OpenAPI 3.1 root `security`). A nil operation-level list means "inherit"; an
// explicit empty list means "public" (no requirement) and is returned as-is.
func effectiveSecurity(doc *openapi3.T, op *openapi3.Operation) openapi3.SecurityRequirements {
	if op.Security != nil {
		return *op.Security
	}
	return doc.Security
}

// requirementSchemes lists the security-scheme names referenced anywhere in a
// requirement set (across every OR alternative).
func requirementSchemes(reqs openapi3.SecurityRequirements) map[string]bool {
	out := map[string]bool{}
	for _, req := range reqs {
		for name := range req {
			out[name] = true
		}
	}
	return out
}

// TestConnectorOperationsResolveToCookieAuth is the issue #5 contract-assertion
// (written first as a failing negative against the pre-fix contract, whose root
// `security: [{bearerAuth: []}]` leaves connector operations advertising a
// bearer path the runtime never accepts). It reads the security declared by the
// GENERATED gen/go embedded spec (GetSpec) — which is generated from
// contracts/gateway.openapi.yaml — so it simultaneously proves the contract is
// truthful AND that regeneration carried the correction into the generated
// artifacts. Every connector operation must resolve to cookieAuth and must NOT
// advertise bearerAuth.
func TestConnectorOperationsResolveToCookieAuth(t *testing.T) {
	doc, err := gateway.GetSpec()
	if err != nil {
		t.Fatalf("loading generated embedded gateway spec: %v", err)
	}

	// The cookieAuth scheme itself must be defined and describe the mo_session
	// httpOnly session cookie (PRD §8/§12.3), not something else.
	if doc.Components == nil || doc.Components.SecuritySchemes == nil {
		t.Fatal("contract defines no securitySchemes")
	}
	cookieRef, ok := doc.Components.SecuritySchemes["cookieAuth"]
	if !ok || cookieRef.Value == nil {
		t.Fatal("contract defines no cookieAuth security scheme (PRD §8/§12.3 mo_session)")
	}
	if got := cookieRef.Value; got.Type != "apiKey" || got.In != "cookie" || got.Name != SessionCookieName {
		t.Fatalf("cookieAuth must be apiKey in cookie %q; got type=%q in=%q name=%q",
			SessionCookieName, got.Type, got.In, got.Name)
	}

	for _, co := range connectorOperations {
		pathItem := doc.Paths.Find(co.path)
		if pathItem == nil {
			t.Errorf("contract has no path %q", co.path)
			continue
		}
		op := pathItem.GetOperation(co.method)
		if op == nil {
			t.Errorf("contract has no %s operation on %q", co.method, co.path)
			continue
		}
		reqs := effectiveSecurity(doc, op)
		if len(reqs) == 0 {
			t.Errorf("%s %s: connector operation resolves to NO security (must be cookieAuth)", co.method, co.path)
			continue
		}
		schemes := requirementSchemes(reqs)
		if !schemes["cookieAuth"] {
			t.Errorf("%s %s: effective security %v does not include cookieAuth (runtime authenticates the connector caller by the mo_session cookie only)", co.method, co.path, schemes)
		}
		if schemes["bearerAuth"] {
			t.Errorf("%s %s: effective security %v advertises bearerAuth, but the runtime never validates a bearer for a first-party connector caller (issue #5)", co.method, co.path, schemes)
		}
	}
}

// TestConnectorAuthenticatesByCookieNotBearer is the issue #5 runtime proof that
// mirrors the corrected contract: a connector call authenticates from the
// mo_session session cookie WITHOUT any Authorization bearer header, while a
// bearer-only caller (the LLM machine gateway token) cannot authenticate a
// connector WRITE. It drives the REAL mounted generated router. A full
// gen/python AuthenticatedClient round-trip needs a live server and is the
// CI-deferred gate; this in-process proof plus the static contract assertion
// above stand alone in the sandbox.
func TestConnectorAuthenticatesByCookieNotBearer(t *testing.T) {
	const gatewayToken = "test-gateway-token-connector"

	fa := newFakeAuth()
	// A valid Owner session — the role that holds every connector action.
	ownerTok := "tok-owner-connector"
	fa.principals[ownerTok] = principal(perm.RoleOwner)

	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithCookieSecure(false), WithGatewayToken(gatewayToken))

	isAuthRejected := func(code int) bool {
		return code == http.StatusUnauthorized || code == http.StatusForbidden
	}

	// 1) Cookie only, no Authorization header: authentication MUST pass. The
	// connector service is unwired in this suite, so the handler fails past
	// authorization (e.g. 500/503) — anything but 401/403 proves the cookie
	// authenticated the connector call without a bearer.
	t.Run("cookie authenticates connector without bearer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/connector/connect", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ownerTok})
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if isAuthRejected(rec.Code) {
			t.Fatalf("cookie-authenticated connector call was rejected at the auth boundary: status=%d (want NOT 401/403)", rec.Code)
		}
		if h := req.Header.Get("Authorization"); h != "" {
			t.Fatalf("test bug: request carried an Authorization header %q", h)
		}
	})

	// 2) Bearer machine gateway token only, no cookie: a connector WRITE is a
	// non-Draft L2+ action outside the machine envelope (perm.GatewayCan denies
	// it), so it MUST be rejected. This is why the contract may not advertise
	// bearerAuth for connector operations.
	t.Run("bearer does not authenticate a connector write", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/connector/connect", nil)
		req.Header.Set("Authorization", "Bearer "+gatewayToken)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if !isAuthRejected(rec.Code) {
			t.Fatalf("bearer-only connector write was NOT rejected: status=%d (want 401/403)", rec.Code)
		}
	})

	// 3) Neither cookie nor bearer: fail closed with 401.
	t.Run("no credential fails closed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/connector/connect", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("no-credential connector call: status=%d (want 401)", rec.Code)
		}
	})
}
