package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

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

func securityAlternatives(reqs openapi3.SecurityRequirements) map[string]bool {
	alternatives := make(map[string]bool, len(reqs))
	for _, requirement := range reqs {
		if len(requirement) == 0 {
			alternatives["public"] = true
			continue
		}
		if len(requirement) != 1 {
			alternatives["compound"] = true
			continue
		}
		for name := range requirement {
			alternatives[name] = true
		}
	}
	return alternatives
}

// TestContractSecurityMatchesRuntimeRoutePolicies keeps the authored/generated
// auth contract exactly aligned with middleware.go. Protected routes accept a
// human cookie and accept the machine bearer only when GatewayCan grants their
// exact action. Connector writes therefore remain cookie-only, while connector
// status and every other allowlisted machine read truthfully expose cookie OR
// bearer as separate OpenAPI alternatives (never a compound AND requirement).
func TestContractSecurityMatchesRuntimeRoutePolicies(t *testing.T) {
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

	for _, policy := range routePolicies {
		pathItem := doc.Paths.Find(policy.path)
		if pathItem == nil {
			t.Errorf("contract has no path %q", policy.path)
			continue
		}
		op := pathItem.GetOperation(policy.method)
		if op == nil {
			t.Errorf("contract has no %s operation on %q", policy.method, policy.path)
			continue
		}
		got := securityAlternatives(effectiveSecurity(doc, op))
		want := map[string]bool{}
		switch policy.kind {
		case kindPublic:
			// An empty security array means the operation is public.
		case kindSessionOptional:
			want["public"] = true
			want["cookieAuth"] = true
		case kindGatewayDraft:
			want["bearerAuth"] = true
		case kindCapture:
			want["captureAuth"] = true
			want["cookieAuth"] = true
		case kindCaptureRead:
			want["captureAuth"] = true
		case kindProtected:
			want["cookieAuth"] = true
			if perm.GatewayCan(policy.action) {
				want["bearerAuth"] = true
			}
		default:
			t.Fatalf("unclassified runtime route kind %d", policy.kind)
		}

		if len(got) != len(want) {
			t.Errorf("%s %s: contract security alternatives = %v, runtime requires %v", policy.method, policy.path, got, want)
			continue
		}
		for alternative := range want {
			if !got[alternative] {
				t.Errorf("%s %s: contract security alternatives = %v, runtime requires %v", policy.method, policy.path, got, want)
				break
			}
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
	// bearerAuth for connector writes.
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
