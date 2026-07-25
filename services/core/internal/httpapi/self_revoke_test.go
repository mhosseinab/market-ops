package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// Issue #149: the extension's Revoke deleted only its LOCAL credential, so the
// server-side credential hash stayed valid until its own expiry — a copied
// credential kept uploading after the user believed access was revoked.
// PD-4 disposition (B): a captureAuth-scoped SELF-revoke route, so the
// credential can invalidate itself at the authority that verifies it, WITHOUT a
// human web session or an MV3 cross-origin cookie.
//
// These tests are the transport half of that seam. NEGATIVE FIRST (§4.6:
// identity quarantine + fail closed).

const selfRevokePath = "/ext/pairing/self-revoke"

func selfRevokeReq(body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(http.MethodPost, selfRevokePath, nil)
	}
	r := httptest.NewRequest(http.MethodPost, selfRevokePath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// TestSelfRevokeRequiresACaptureCredential is the fail-closed negative: the
// self-revoke route is authenticated ONLY by a live capture credential. No
// credential, a human session cookie, and the LLM machine gateway bearer are all
// refused with 401 — the route is never a human or machine surface, so the
// credential identity can never be borrowed from another principal.
func TestSelfRevokeRequiresACaptureCredential(t *testing.T) {
	const gatewayToken = "test-gateway-token-self-revoke"
	fa := newFakeAuth()
	ownerTok := "tok-owner-self-revoke"
	fa.principals[ownerTok] = principal(perm.RoleOwner)
	fp := &fakePairing{account: uuid.New(), credential: "live-capture-credential"}

	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithCookieSecure(false), WithGatewayToken(gatewayToken), WithPairing(fp))

	cases := []struct {
		name    string
		prepare func(*http.Request)
	}{
		{"no credential at all", func(*http.Request) {}},
		{"a human session cookie is not a capture credential", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ownerTok})
		}},
		{"the LLM machine gateway bearer is never accepted", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+gatewayToken)
		}},
		{"an unknown/expired/revoked capture credential", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer not-the-live-credential")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := selfRevokeReq("")
			tc.prepare(req)
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("self-revoke = %d, want 401 (fail closed); body=%s", rec.Code, rec.Body.String())
			}
			if fp.revokedCredentials != nil {
				t.Fatalf("an unauthenticated self-revoke reached the pairing service: %v", fp.revokedCredentials)
			}
		})
	}
}

// TestSelfRevokeIdentityIsCredentialDerivedNotCallerSupplied is the identity-
// quarantine negative (§4.6, the #131 systemic concern): the credential the
// route revokes comes SOLELY from the presented capture credential. A body,
// query, or path selector naming another credential/account can never redirect
// the revocation — the contract declares no parameters and the handler reads
// only the middleware-injected credential identity.
func TestSelfRevokeIdentityIsCredentialDerivedNotCallerSupplied(t *testing.T) {
	victimCredential := uuid.New()
	fp := &fakePairing{
		account:      uuid.New(),
		credential:   "live-capture-credential",
		credentialID: uuid.New(),
	}
	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(newFakeAuth()), WithCookieSecure(false), WithPairing(fp))

	// A hostile caller presents ITS OWN valid credential but tries to name
	// somebody else's credential/account in a body AND a query parameter.
	body, _ := json.Marshal(map[string]any{
		"credentialId":         victimCredential,
		"marketplaceAccountId": uuid.New(),
	})
	req := httptest.NewRequest(http.MethodPost,
		selfRevokePath+"?credentialId="+victimCredential.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer live-capture-credential")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	// The route mounts an EXACT path; a query string does not change the path, so
	// the request authenticates and revokes — but only the presenter's own id.
	if rec.Code != http.StatusNoContent {
		t.Fatalf("self-revoke with a hostile selector = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(fp.revokedCredentials) != 1 {
		t.Fatalf("revoked credentials = %v, want exactly the presenter's own", fp.revokedCredentials)
	}
	if fp.revokedCredentials[0] != fp.credentialID {
		t.Fatalf("revoked credential = %v, want the credential-derived %v (never caller-supplied)",
			fp.revokedCredentials[0], fp.credentialID)
	}
	if fp.revokedCredentials[0] == victimCredential {
		t.Fatal("a caller-supplied selector redirected the revocation — identity quarantine breached")
	}
	// It must NOT fall back to the account-wide human kill switch either.
	if fp.revokeCalls != 0 {
		t.Fatalf("self-revoke invoked the account-wide RevokeForOrganization %d times; it must revoke ONLY the presented credential", fp.revokeCalls)
	}
}

// TestSelfRevokeIsIdempotent: the first call on a live credential returns 204;
// every later call presents an ALREADY-REVOKED credential, which fails closed
// with 401 before the handler. Neither outcome is ambiguous, and the end state
// is identical — a repeated revoke is idempotent in effect. Clients treat the
// 401 as CONFIRMED revocation (documented in the contract).
func TestSelfRevokeIsIdempotent(t *testing.T) {
	fp := &fakePairing{account: uuid.New(), credential: "live-capture-credential", credentialID: uuid.New()}
	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(newFakeAuth()), WithCookieSecure(false), WithPairing(fp))

	post := func() int {
		req := selfRevokeReq("")
		req.Header.Set("Authorization", "Bearer live-capture-credential")
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := post(); got != http.StatusNoContent {
		t.Fatalf("first self-revoke = %d, want 204", got)
	}
	for i := 0; i < 3; i++ {
		if got := post(); got != http.StatusUnauthorized {
			t.Fatalf("repeat self-revoke #%d = %d, want 401 (already revoked at the authority)", i+1, got)
		}
	}
	if len(fp.revokedCredentials) != 1 {
		t.Fatalf("repeated self-revoke mutated %d times, want exactly 1 (idempotent)", len(fp.revokedCredentials))
	}
}

// TestSelfRevokeUnavailablePairingPlaneFailsClosed: with no pairing service the
// credential cannot be authenticated at all, so the route is refused — it never
// reports a revocation that did not happen.
func TestSelfRevokeUnavailablePairingPlaneFailsClosed(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(newFakeAuth()), WithCookieSecure(false))
	req := selfRevokeReq("")
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("self-revoke with no pairing plane = %d, want 401 (fail closed)", rec.Code)
	}
}

// TestSelfRevokeEmitsObservability: a kill switch engaging without an emitted,
// traced, audited event is a bug (CLAUDE.md). The revocation boundary emits a
// structured log with STABLE keys and a counter labeled by outcome — and NEVER
// the raw capture credential.
func TestSelfRevokeEmitsObservability(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	fp := &fakePairing{account: uuid.New(), credential: "live-capture-credential", credentialID: uuid.New()}
	srv := NewServer(":0", BuildInfo{}, logger,
		WithAuth(newFakeAuth()), WithCookieSecure(false), WithPairing(fp))

	req := selfRevokeReq("")
	req.Header.Set("Authorization", "Bearer live-capture-credential")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("self-revoke = %d, want 204", rec.Code)
	}

	out := logs.String()
	if !strings.Contains(out, `"route":"`+selfRevokePath+`"`) {
		t.Fatalf("revocation log missing the stable route key; got %s", out)
	}
	if !strings.Contains(out, `"outcome":"revoked"`) {
		t.Fatalf("revocation log missing the stable outcome key; got %s", out)
	}
	if !strings.Contains(out, `"credential_id":"`+fp.credentialID.String()+`"`) {
		t.Fatalf("revocation log missing the credential identity; got %s", out)
	}
	if strings.Contains(out, "live-capture-credential") {
		t.Fatalf("the RAW capture credential leaked into a log: %s", out)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != pairingSelfRevokeMetric {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is %T, want an int64 counter", m.Name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				v, ok := dp.Attributes.Value("outcome")
				if !ok {
					t.Fatalf("%s datapoint has no outcome label", m.Name)
				}
				if v.AsString() != "revoked" {
					t.Fatalf("%s outcome = %q, want %q", m.Name, v.AsString(), "revoked")
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no %s counter datapoint emitted — the kill-switch boundary is unobservable", pairingSelfRevokeMetric)
	}
}
