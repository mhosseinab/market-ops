package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
)

// fakeConnector is a ConnectorService stub returning a fixed snapshot. It records
// the organization id it was called with so tests can assert the handler derives
// scope from the authenticated principal, never from request input (S8-AUTHZ-001).
type fakeConnector struct {
	snap    connector.Snapshot
	err     error
	lastOrg uuid.UUID
	lastAcc uuid.UUID
}

func (f *fakeConnector) Connect(_ context.Context, org, acc uuid.UUID, _ string) (connector.Snapshot, error) {
	f.lastOrg, f.lastAcc = org, acc
	return f.snap, f.err
}
func (f *fakeConnector) Refresh(_ context.Context, org, acc uuid.UUID) (connector.Snapshot, error) {
	f.lastOrg, f.lastAcc = org, acc
	return f.snap, f.err
}
func (f *fakeConnector) Disconnect(_ context.Context, org, acc uuid.UUID) (connector.Snapshot, error) {
	f.lastOrg, f.lastAcc = org, acc
	return f.snap, f.err
}
func (f *fakeConnector) Status(_ context.Context, org, acc uuid.UUID) (connector.Snapshot, error) {
	f.lastOrg, f.lastAcc = org, acc
	return f.snap, f.err
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func serverWithConnector(fa *fakeAuth, fc *fakeConnector) *http.Server {
	return NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithConnector(fc))
}

// TestConnectorStatusMapsAllNineCapabilities proves the status endpoint emits all
// nine §15.2 capabilities and preserves the Unknown default for unprobed ones. It
// also asserts the handler scopes the read to the AUTHENTICATED organization.
func TestConnectorStatusMapsAllNineCapabilities(t *testing.T) {
	acct := uuid.New()
	reg := connector.NewRegistryFrom([]connector.CapabilityStatus{
		{Capability: connector.CatalogRead, State: connector.Supported},
	})
	fc := &fakeConnector{snap: connector.Snapshot{AccountID: acct, Connection: connector.Connected, Registry: reg}}
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	srv := serverWithConnector(fa, fc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/connector/status?marketplaceAccountId="+acct.String(), nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	// The organization the service saw is the session principal's — never input.
	if fc.lastOrg != orgID {
		t.Fatalf("service org = %v, want authenticated org %v", fc.lastOrg, orgID)
	}
	if fc.lastAcc != acct {
		t.Fatalf("service account = %v, want %v", fc.lastAcc, acct)
	}
	var got struct {
		ConnectionState string `json:"connectionState"`
		Capabilities    []struct {
			Capability string `json:"capability"`
			Status     string `json:"status"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ConnectionState != "connected" {
		t.Errorf("connectionState = %q", got.ConnectionState)
	}
	if len(got.Capabilities) != 9 {
		t.Fatalf("expected 9 capabilities, got %d", len(got.Capabilities))
	}
	states := map[string]string{}
	for _, c := range got.Capabilities {
		states[c.Capability] = c.Status
	}
	if states["catalog_read"] != "supported" {
		t.Errorf("catalog_read = %q, want supported", states["catalog_read"])
	}
	// Unprobed capabilities stay Unknown in the emitted status.
	if states["price_write"] != "unknown" {
		t.Errorf("price_write = %q, want unknown", states["price_write"])
	}
}

// TestConnectorEndpointsFailClosedWhenUnwired proves that without a connector
// service, the routes return a structured error and never a healthy status
// (ACC-003: no generic healthy state while unconfigured). The unavailable check
// precedes authentication, so no session is required to observe fail-closed.
func TestConnectorEndpointsFailClosedWhenUnwired(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger()) // no WithConnector, no WithAuth

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/connector/status?marketplaceAccountId="+uuid.NewString(), nil)
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "CONNECTOR_UNAVAILABLE" {
		t.Errorf("code = %q, want CONNECTOR_UNAVAILABLE", env.Code)
	}
}

// TestConnectRejectsEmptyAuthCode proves the connect endpoint surfaces the
// validation error as a 400 INVALID_ARGUMENT (with an authenticated session).
func TestConnectRejectsEmptyAuthCode(t *testing.T) {
	fc := &fakeConnector{err: connector.ErrInvalidAuthCode}
	fa := newFakeAuth()
	ownerSession(fa)
	srv := serverWithConnector(fa, fc)

	rec := httptest.NewRecorder()
	body := `{"marketplaceAccountId":"` + uuid.NewString() + `","authorizationCode":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/connector/connect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// TestConnectorHandlersScopeToAuthenticatedOrg proves every connector handler
// passes the session principal's organization to the service and the account id
// from the request — even for a foreign account UUID the org is the caller's, so
// the org-scoped service rejects it. This is the transport half of S8-AUTHZ-001:
// scope is derived only from authenticated context, never request input.
func TestConnectorHandlersScopeToAuthenticatedOrg(t *testing.T) {
	foreignAccount := uuid.New()
	fc := &fakeConnector{snap: connector.Snapshot{Registry: connector.NewRegistry()}}
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	srv := serverWithConnector(fa, fc)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"connect", http.MethodPost, "/connector/connect", `{"marketplaceAccountId":"` + foreignAccount.String() + `","authorizationCode":"code"}`},
		{"refresh", http.MethodPost, "/connector/refresh", `{"marketplaceAccountId":"` + foreignAccount.String() + `"}`},
		{"disconnect", http.MethodPost, "/connector/disconnect", `{"marketplaceAccountId":"` + foreignAccount.String() + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc.lastOrg, fc.lastAcc = uuid.Nil, uuid.Nil
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d, body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if fc.lastOrg != orgID {
				t.Fatalf("%s: service org = %v, want authenticated org %v (never request input)", tc.name, fc.lastOrg, orgID)
			}
			if fc.lastAcc != foreignAccount {
				t.Fatalf("%s: service account = %v, want %v", tc.name, fc.lastAcc, foreignAccount)
			}
		})
	}
}

// TestConnectorHandlersRequireSession proves every connector route fails closed
// with 401 when no human session is presented — the connector service is never
// reached without an authenticated organization.
func TestConnectorHandlersRequireSession(t *testing.T) {
	fc := &fakeConnector{snap: connector.Snapshot{Registry: connector.NewRegistry()}}
	fa := newFakeAuth()
	ownerSession(fa) // a valid token exists, but requests omit it
	srv := serverWithConnector(fa, fc)

	acct := uuid.NewString()
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/connector/status?marketplaceAccountId=" + acct, ""},
		{http.MethodPost, "/connector/connect", `{"marketplaceAccountId":"` + acct + `","authorizationCode":"c"}`},
		{http.MethodPost, "/connector/refresh", `{"marketplaceAccountId":"` + acct + `"}`},
		{http.MethodPost, "/connector/disconnect", `{"marketplaceAccountId":"` + acct + `"}`},
	}
	for _, tc := range cases {
		fc.lastOrg = uuid.Nil
		rec := httptest.NewRecorder()
		var req *http.Request
		if tc.body == "" {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		} else {
			req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
		}
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without session = %d, want 401", tc.method, tc.path, rec.Code)
		}
		if fc.lastOrg != uuid.Nil {
			t.Fatalf("%s %s reached the connector service without a session", tc.method, tc.path)
		}
	}
}
