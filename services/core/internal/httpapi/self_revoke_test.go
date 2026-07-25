package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	gateway "github.com/mhosseinab/market-ops/gen/go"
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
//
// It answers 503 (the status the contract advertises for an unconfigured
// pairing plane), NOT 401. The contract binds 401 to "the credential is not
// valid at the authority", which a client treats as a CONFIRMED revocation;
// answering 401 because this instance has no pairing plane would tell the
// extension a still-live credential was killed (issue #149's exact impact).
func TestSelfRevokeUnavailablePairingPlaneFailsClosed(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(newFakeAuth()), WithCookieSecure(false))
	req := selfRevokeReq("")
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("self-revoke with no pairing plane = %d, want 503 (fail closed, NOT a confirmed revocation)", rec.Code)
	}
}

// selfRevokeOutcomes collects the kill-switch counter's datapoints keyed by
// their `outcome` label, so a test can assert the boundary's telemetry rather
// than merely its status code.
func selfRevokeOutcomes(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	out := map[string]int64{}
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
				out[v.AsString()] += dp.Value
			}
		}
	}
	return out
}

// withManualMeter installs a manual-reader MeterProvider for the duration of a
// test and returns the reader.
func withManualMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	return reader
}

// TestTransientCredentialResolveFailureIsNeverAConfirmedRevocation is the F1
// regression for issue #149. The contract states a client MUST treat 401 on the
// self-revoke route as CONFIRMED revocation, and the extension implements
// exactly that. So 401 may ONLY be returned when the credential is genuinely
// not valid at the authority.
//
// Before the fix the middleware collapsed EVERY ResolveCredential error to 401:
// pairing.ResolveCredential returns ErrInvalidCredential only for pgx.ErrNoRows
// and wraps every other failure (DB outage, pool exhaustion, statement timeout).
// A transient store failure therefore answered 401 with the credential row still
// LIVE — the extension discarded its credential, cleared the pending marker and
// reported the kill switch complete, while a copied credential kept uploading
// for the remaining 30-day TTL. That is the #149 impact statement verbatim.
//
// A transient failure is NOT an authoritative statement about the credential, so
// it must render as 5xx (which the extension maps to `pending` and retries).
func TestTransientCredentialResolveFailureIsNeverAConfirmedRevocation(t *testing.T) {
	transient := errors.New("pairing: resolve credential: conn busy: another query is in progress")

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"self-revoke (a false 401 here is a false CONFIRMED revocation)", http.MethodPost, selfRevokePath},
		{"owned-targets (the same credential-scoped resolve seam)", http.MethodGet, "/ext/owned-targets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := &fakePairing{
				account:      uuid.New(),
				credential:   "live-capture-credential",
				credentialID: uuid.New(),
				resolveErr:   transient,
			}
			srv := NewServer(":0", BuildInfo{}, testLogger(),
				WithAuth(newFakeAuth()), WithCookieSecure(false), WithPairing(fp))

			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer live-capture-credential")
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			if fp.resolveCalls == 0 {
				t.Fatal("the middleware never reached the pairing plane — the probe proves nothing")
			}
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("a TRANSIENT store failure answered 401; a client reads that as CONFIRMED revocation while the credential row is still live (issue #149)")
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("transient resolve failure = %d, want 500", rec.Code)
			}
			if fp.revokedCredentials != nil {
				t.Fatalf("nothing was revoked, yet credentials were mutated: %v", fp.revokedCredentials)
			}
		})
	}
}

// TestSelfRevokeNeverReportsSuccessOnAFailedRevocation is the F2 regression: the
// handler's "NEVER report success on a failed revocation" branch. A store
// failure inside RevokeCredentialByID must render as a NON-2xx (so the extension
// keeps capture disabled, retains the credential and retries) and must be
// OBSERVABLE as outcome="error" — never silently absorbed into a 204.
func TestSelfRevokeNeverReportsSuccessOnAFailedRevocation(t *testing.T) {
	reader := withManualMeter(t)
	fp := &fakePairing{
		account:       uuid.New(),
		credential:    "live-capture-credential",
		credentialID:  uuid.New(),
		revokeByIDErr: errors.New("pairing: revoke credential: write failed"),
	}
	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(newFakeAuth()), WithCookieSecure(false), WithPairing(fp))

	req := selfRevokeReq("")
	req.Header.Set("Authorization", "Bearer live-capture-credential")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code/100 == 2 {
		t.Fatalf("a FAILED revocation answered %d — the extension would treat it as confirmed and discard a live credential", rec.Code)
	}
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("a failed revocation answered 401, which the contract defines as CONFIRMED revocation")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed revocation = %d, want 500", rec.Code)
	}
	if got := selfRevokeOutcomes(t, reader)["error"]; got != 1 {
		t.Fatalf(`%s{outcome="error"} = %d, want 1 — a failed kill switch must be observable`, pairingSelfRevokeMetric, got)
	}
}

// TestSelfRevokeUnavailableAndNoIdentityOutcomesAreObservable exercises the
// handler's two remaining fail-closed branches directly. The middleware now
// refuses an unconfigured pairing plane with 503 before the handler runs, so the
// handler's own nil-pairing guard is defence in depth — it still must never
// report a revocation that did not happen, and both refusals must be visible in
// telemetry (CLAUDE.md: a fallback engaging without an emitted event is a bug).
func TestSelfRevokeUnavailableAndNoIdentityOutcomesAreObservable(t *testing.T) {
	t.Run("no pairing plane emits outcome=unavailable and 503", func(t *testing.T) {
		reader := withManualMeter(t)
		gs := &gatewayServer{logger: testLogger(), pairingTelemetry: newPairingTelemetry()}
		resp, err := gs.SelfRevokeCapturePairing(context.Background(), gateway.SelfRevokeCapturePairingRequestObject{})
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if _, ok := resp.(gateway.SelfRevokeCapturePairing503JSONResponse); !ok {
			t.Fatalf("no-pairing-plane response = %T, want 503", resp)
		}
		if got := selfRevokeOutcomes(t, reader)["unavailable"]; got != 1 {
			t.Fatalf(`%s{outcome="unavailable"} = %d, want 1`, pairingSelfRevokeMetric, got)
		}
	})

	t.Run("no credential identity emits outcome=no_credential_identity and 401", func(t *testing.T) {
		reader := withManualMeter(t)
		fp := &fakePairing{account: uuid.New(), credential: "live-capture-credential", credentialID: uuid.New()}
		gs := &gatewayServer{logger: testLogger(), pairing: fp, pairingTelemetry: newPairingTelemetry()}
		// No middleware ran, so the context carries NO credential identity. The
		// handler must refuse rather than invent one or fall back to the
		// account-wide kill switch (identity quarantine, §4.6).
		resp, err := gs.SelfRevokeCapturePairing(context.Background(), gateway.SelfRevokeCapturePairingRequestObject{})
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if _, ok := resp.(gateway.SelfRevokeCapturePairing401JSONResponse); !ok {
			t.Fatalf("no-identity response = %T, want 401", resp)
		}
		if fp.revokedCredentials != nil {
			t.Fatalf("a request with no credential identity revoked %v", fp.revokedCredentials)
		}
		if fp.revokeCalls != 0 {
			t.Fatal("a request with no credential identity fell back to the account-wide kill switch")
		}
		if got := selfRevokeOutcomes(t, reader)["no_credential_identity"]; got != 1 {
			t.Fatalf(`%s{outcome="no_credential_identity"} = %d, want 1`, pairingSelfRevokeMetric, got)
		}
	})
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

// TestCredentialScoped401CarriesPositiveProofOnlyWhenAuthoritative is the issue
// #149 fix-3 regression for POSITIVE PROOF of revocation.
//
// A generic 401 is not evidence of anything. An unmounted route, a reverse
// proxy, a WAF, or a gateway build that predates this route all answer 401
// {"code":"NO_SESSION"} — byte-identical to what an authoritative "this
// credential is dead" used to look like. A client that reads that as CONFIRMED
// destroys its credential material while the server row stays LIVE, which is
// issue #149 verbatim.
//
// So the ONLY 401 that evidences revocation is the one the pairing plane itself
// authoritatively produced (pairing.ErrInvalidCredential), and it carries a
// DISTINCT machine-readable code. Every other 401 — above all the one for an
// ABSENT bearer, which says nothing about any credential — keeps the generic
// code. 503 (unconfigured plane) and 500 (transient store failure) never carry
// it either: they are not statements about the credential at all.
func TestCredentialScoped401CarriesPositiveProofOnlyWhenAuthoritative(t *testing.T) {
	live := "live-capture-credential"
	newPairing := func() *fakePairing {
		return &fakePairing{account: uuid.New(), credential: live, credentialID: uuid.New()}
	}

	codeOf := func(t *testing.T, rec *httptest.ResponseRecorder) string {
		t.Helper()
		var env gateway.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("response body is not an ErrorEnvelope: %v (body=%s)", err, rec.Body.String())
		}
		return env.Code
	}

	for _, route := range []struct {
		name   string
		method string
		path   string
	}{
		{"self-revoke", http.MethodPost, selfRevokePath},
		{"owned-targets (the same credential-scoped resolve seam)", http.MethodGet, "/ext/owned-targets"},
	} {
		t.Run(route.name, func(t *testing.T) {
			do := func(t *testing.T, fp *fakePairing, bearer string) *httptest.ResponseRecorder {
				t.Helper()
				var opts []Option
				opts = append(opts, WithAuth(newFakeAuth()), WithCookieSecure(false))
				if fp != nil {
					opts = append(opts, WithPairing(fp))
				}
				srv := NewServer(":0", BuildInfo{}, testLogger(), opts...)
				req := httptest.NewRequest(route.method, route.path, nil)
				if bearer != "" {
					req.Header.Set("Authorization", "Bearer "+bearer)
				}
				rec := httptest.NewRecorder()
				srv.Handler.ServeHTTP(rec, req)
				return rec
			}

			t.Run("an AUTHORITATIVE invalid credential carries the positive-proof code", func(t *testing.T) {
				rec := do(t, newPairing(), "some-other-credential")
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("unknown credential = %d, want 401", rec.Code)
				}
				if got := codeOf(t, rec); got != captureCredentialInvalidCode {
					t.Fatalf("authoritative-invalid 401 code = %q, want %q — without a distinct code a client cannot tell this from a proxy 401 (issue #149)", got, captureCredentialInvalidCode)
				}
			})

			t.Run("an ABSENT bearer is NOT an authoritative statement about a credential", func(t *testing.T) {
				rec := do(t, newPairing(), "")
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("missing bearer = %d, want 401", rec.Code)
				}
				if got := codeOf(t, rec); got == captureCredentialInvalidCode {
					t.Fatalf("missing-bearer 401 carries %q; absence of a credential is not proof any credential was revoked", got)
				}
				if got := codeOf(t, rec); got != "NO_SESSION" {
					t.Fatalf("missing-bearer 401 code = %q, want NO_SESSION (unchanged)", got)
				}
			})

			t.Run("an unconfigured pairing plane stays 503 and never carries the proof code", func(t *testing.T) {
				rec := do(t, nil, live)
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("unconfigured plane = %d, want 503", rec.Code)
				}
				if got := codeOf(t, rec); got == captureCredentialInvalidCode {
					t.Fatalf("unconfigured plane carries the revocation-proof code %q", got)
				}
			})

			t.Run("a TRANSIENT store failure stays 500 and never carries the proof code", func(t *testing.T) {
				fp := newPairing()
				fp.resolveErr = errors.New("pairing: resolve credential: conn busy")
				rec := do(t, fp, live)
				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("transient store failure = %d, want 500", rec.Code)
				}
				if got := codeOf(t, rec); got == captureCredentialInvalidCode {
					t.Fatalf("transient store failure carries the revocation-proof code %q", got)
				}
			})
		})
	}
}
