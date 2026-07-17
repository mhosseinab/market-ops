package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// fakeAuth is an AuthService stub. Tokens map to principals; login returns a
// fixed session for a known credential.
type fakeAuth struct {
	principals map[string]auth.Principal // token -> principal
	loginOK    map[string]auth.Session   // email -> session on correct password
	password   string
	loggedOut  []string
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{principals: map[string]auth.Principal{}, loginOK: map[string]auth.Session{}, password: "pw"}
}

func (f *fakeAuth) Login(_ context.Context, email, password string) (auth.Session, error) {
	if password != f.password {
		return auth.Session{}, auth.ErrInvalidCredentials
	}
	s, ok := f.loginOK[email]
	if !ok {
		return auth.Session{}, auth.ErrInvalidCredentials
	}
	f.principals[s.Token] = s.Principal
	return s, nil
}

func (f *fakeAuth) Resolve(_ context.Context, token string) (auth.Principal, error) {
	p, ok := f.principals[token]
	if !ok {
		return auth.Principal{}, auth.ErrNoSession
	}
	return p, nil
}

func (f *fakeAuth) Logout(_ context.Context, token string) error {
	f.loggedOut = append(f.loggedOut, token)
	delete(f.principals, token)
	return nil
}

func principal(role perm.Role) auth.Principal {
	return auth.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Email:          string(role) + "@x.io",
		Role:           role,
		ExpiresAt:      time.Now().Add(time.Hour).UTC(),
	}
}

// serverWithAuth builds a server with the fake auth wired (middleware armed) and
// cookie Secure off so httptest cookies flow.
func serverWithAuth(t *testing.T, fa *fakeAuth) *http.Server {
	t.Helper()
	return NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false))
}

func TestLoginSetsCookieAndOmitsTokenFromBody(t *testing.T) {
	fa := newFakeAuth()
	p := principal(perm.RoleOwner)
	fa.loginOK["owner@x.io"] = auth.Session{Token: "tok-owner", Principal: p}
	srv := serverWithAuth(t, fa)

	body := `{"email":"owner@x.io","password":"pw"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "tok-owner") {
		t.Fatal("session token leaked into response body — must live only in the cookie")
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie set")
	}
	if cookie.Value != "tok-owner" {
		t.Fatalf("cookie value = %q", cookie.Value)
	}
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("session cookie SameSite not set")
	}
}

func TestLoginBadCredentialsFailsClosed(t *testing.T) {
	fa := newFakeAuth()
	srv := serverWithAuth(t, fa)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"x@x.io","password":"nope"}`))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("failed login must not set a cookie")
	}
}

func TestProtectedRouteRequiresSession(t *testing.T) {
	fa := newFakeAuth()
	srv := serverWithAuth(t, fa)
	acct := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/connector/status?marketplaceAccountId="+acct.String(), nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated protected route = %d, want 401", rec.Code)
	}
}

func TestProtectedRouteEnforcesRolePermission(t *testing.T) {
	fa := newFakeAuth()
	// Operator has a valid session but may NOT connect the account (Owner-only).
	fa.principals["tok-op"] = principal(perm.RoleOperator)
	srv := serverWithAuth(t, fa)

	req := httptest.NewRequest(http.MethodPost, "/connector/connect", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-op"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator connect = %d, want 403", rec.Code)
	}
}

func TestMeReturnsSessionInfo(t *testing.T) {
	fa := newFakeAuth()
	p := principal(perm.RoleInternal)
	fa.principals["tok-int"] = p
	srv := serverWithAuth(t, fa)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-int"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", rec.Code)
	}
	var got struct {
		Role  string `json:"role"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Role != "internal" || got.Email != p.Email {
		t.Fatalf("me body = %+v", got)
	}
}

func TestMeWithoutSessionDenied(t *testing.T) {
	fa := newFakeAuth()
	srv := serverWithAuth(t, fa)
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me without session = %d, want 401", rec.Code)
	}
}

func TestLogoutClearsCookieAndIsIdempotent(t *testing.T) {
	fa := newFakeAuth()
	fa.principals["tok-x"] = principal(perm.RoleOwner)
	srv := serverWithAuth(t, fa)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-x"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rec.Code)
	}
	// Cookie cleared (MaxAge < 0).
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear the session cookie")
	}
	// Logout without a cookie still succeeds (idempotent).
	rec2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/auth/logout", nil))
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("logout without cookie = %d, want 204", rec2.Code)
	}
}

func TestHealthzPublicUnderAuth(t *testing.T) {
	fa := newFakeAuth()
	srv := serverWithAuth(t, fa)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz under auth = %d, want 200", rec.Code)
	}
}

// recordingMux implements gateway.ServeMux and records every route pattern the
// generated router registers, without serving anything. It lets the coverage
// test derive the ACTUAL mounted route set structurally instead of hand-syncing
// a hardcoded list.
type recordingMux struct {
	patterns []string
}

func (m *recordingMux) HandleFunc(pattern string, _ func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
}

func (m *recordingMux) ServeHTTP(http.ResponseWriter, *http.Request) {}

// mountedRoutes returns the set of "METHOD /path" keys the generated router
// mounts, derived by feeding a recordingMux into gateway.HandlerFromMux. The
// generated patterns have the form "METHOD /path" (BaseURL empty).
func mountedRoutes(t *testing.T) map[string]bool {
	t.Helper()
	rec := &recordingMux{}
	_ = gateway.HandlerFromMux(nil, rec)
	routes := make(map[string]bool, len(rec.patterns))
	for _, pat := range rec.patterns {
		parts := strings.SplitN(pat, " ", 2)
		if len(parts) != 2 {
			t.Fatalf("unexpected generated route pattern %q", pat)
		}
		routes[parts[0]+" "+parts[1]] = true
	}
	if len(routes) == 0 {
		t.Fatal("generated router mounted no routes — recordingMux wiring is wrong")
	}
	return routes
}

// TestEveryGatewayRouteHasPolicy asserts the routePolicies key set EQUALS the set
// of routes the generated router actually mounts: every mounted route has a
// policy (no route ships unauthenticated) and there are no stale policy entries.
// It is structural — any future generated route without a policy fails the build.
func TestEveryGatewayRouteHasPolicy(t *testing.T) {
	mounted := mountedRoutes(t)

	policies := make(map[string]bool, len(routePolicies))
	for _, p := range routePolicies {
		policies[p.method+" "+p.path] = true
	}

	for key := range mounted {
		if !policies[key] {
			t.Errorf("mounted route %q has no permission policy — would ship UNAUTHENTICATED", key)
		}
	}
	for key := range policies {
		if !mounted[key] {
			t.Errorf("policy %q references a route not mounted by the generated router (stale entry)", key)
		}
	}
}

// TestUnlistedRouteDefaultDeniedWithoutSession proves the middleware fails closed:
// a request to a path with no routePolicy entry and no session is DENIED (401),
// never passed through to the mux.
func TestUnlistedRouteDefaultDeniedWithoutSession(t *testing.T) {
	fa := newFakeAuth()
	srv := serverWithAuth(t, fa)
	req := httptest.NewRequest(http.MethodGet, "/no/such/route", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unlisted route without session = %d, want 401 (default-deny, not passthrough)", rec.Code)
	}
}

// TestUnlistedRouteDefaultDeniedWithSession proves an authenticated caller hitting
// an unpolicied route is DENIED (403), not served.
func TestUnlistedRouteDefaultDeniedWithSession(t *testing.T) {
	fa := newFakeAuth()
	fa.principals["tok-owner"] = principal(perm.RoleOwner)
	srv := serverWithAuth(t, fa)
	req := httptest.NewRequest(http.MethodGet, "/no/such/route", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unlisted route with session = %d, want 403 (default-deny)", rec.Code)
	}
}

// TestProtectedRoutesReferenceKnownActions guards the policy table: every
// protected route names an action that exists in the shared perm matrix.
func TestProtectedRoutesReferenceKnownActions(t *testing.T) {
	for _, p := range routePolicies {
		if p.kind != kindProtected {
			continue
		}
		if _, ok := perm.Lookup(p.action); !ok {
			t.Errorf("route %s %s references unknown action %q", p.method, p.path, p.action)
		}
	}
}
