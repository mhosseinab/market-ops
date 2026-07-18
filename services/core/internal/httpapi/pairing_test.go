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

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/pairing"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// fakePairing is a PairingService stub for the handler + middleware tests.
type fakePairing struct {
	account       uuid.UUID
	credential    string // the one credential that resolves
	mintErr       error
	claimErr      error
	revoked       bool
	revokeCalls   int
	lastMintOrg   uuid.UUID
	lastRevokeOrg uuid.UUID
}

func (f *fakePairing) MintCode(_ context.Context, org uuid.UUID) (pairing.Code, error) {
	f.lastMintOrg = org
	if f.mintErr != nil {
		return pairing.Code{}, f.mintErr
	}
	return pairing.Code{Code: "code-123", MarketplaceAccountID: f.account, ExpiresAt: time.Now().Add(time.Minute).UTC()}, nil
}

func (f *fakePairing) Claim(_ context.Context, raw string) (pairing.Credential, error) {
	if f.claimErr != nil {
		return pairing.Credential{}, f.claimErr
	}
	if raw != "code-123" {
		return pairing.Credential{}, pairing.ErrInvalidCode
	}
	return pairing.Credential{
		Credential: f.credential, CredentialID: uuid.New(),
		MarketplaceAccountID: f.account, ExpiresAt: time.Now().Add(time.Hour).UTC(),
	}, nil
}

func (f *fakePairing) ResolveCredential(_ context.Context, raw string) (pairing.Resolved, error) {
	if f.revoked || raw == "" || raw != f.credential {
		return pairing.Resolved{}, pairing.ErrInvalidCredential
	}
	return pairing.Resolved{CredentialID: uuid.New(), MarketplaceAccountID: f.account}, nil
}

func (f *fakePairing) RevokeForOrganization(_ context.Context, org uuid.UUID) error {
	f.revokeCalls++
	f.lastRevokeOrg = org
	f.revoked = true
	return nil
}

// fakeObservation is a minimal ObservationService that records the captures it
// ingested so tests can assert the capture route reached ingestion.
type fakeObservation struct {
	ingested []observation.Capture
}

func (f *fakeObservation) ListTargets(context.Context, uuid.UUID) ([]db.ObservationTarget, error) {
	return nil, nil
}
func (f *fakeObservation) ListObservedOffers(context.Context, uuid.UUID) ([]db.ObservedOffer, error) {
	return nil, nil
}
func (f *fakeObservation) ListObservations(context.Context, uuid.UUID, int32) ([]db.Observation, error) {
	return nil, nil
}
func (f *fakeObservation) Ingest(_ context.Context, c observation.Capture) (observation.IngestResult, error) {
	f.ingested = append(f.ingested, c)
	return observation.IngestResult{ObservationID: uuid.New(), Quality: "verified"}, nil
}
func (f *fakeObservation) ListConflictedObservedOffers(context.Context, uuid.UUID) ([]db.ObservedOffer, error) {
	return nil, nil
}

func serverWithPairing(t *testing.T, fa *fakeAuth, fp *fakePairing, fo *fakeObservation) *http.Server {
	t.Helper()
	return NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithCookieSecure(false), WithPairing(fp), WithObservation(fo))
}

func captureBody(account, target uuid.UUID) string {
	b, _ := json.Marshal(map[string]any{
		"marketplaceAccountId": account,
		"targetId":             target,
		"nativeVariantId":      12345,
		"subRoute":             "passive",
		"sourceType":           "public-web-endpoint",
		"parserVersion":        "dk-product@1.0.0",
		"evidenceRef":          "ev-1",
		"availabilityStatus":   "in_stock",
		"capturedAt":           time.Now().UTC().Format(time.RFC3339Nano),
		"confidence":           "verified",
	})
	return string(b)
}

// TestCaptureCredentialAuthorizesUpload proves a paired capture credential (Bearer)
// authenticates the capture route with NO human session — the EXT-001 pairing seam.
func TestCaptureCredentialAuthorizesUpload(t *testing.T) {
	account := uuid.New()
	fp := &fakePairing{account: account, credential: "cred-abc"}
	fo := &fakeObservation{}
	srv := serverWithPairing(t, newFakeAuth(), fp, fo)

	req := httptest.NewRequest(http.MethodPost, "/observation/capture", strings.NewReader(captureBody(account, uuid.New())))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer cred-abc")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("capture with credential = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if len(fo.ingested) != 1 {
		t.Fatalf("ingested %d captures, want 1", len(fo.ingested))
	}
}

// TestRevokedCredentialFailsClosed proves a revoked credential yields 401 on the
// next upload (EXT-001/EXT-009 kill switch — the extension surfaces this as a
// visibly disabled state).
func TestRevokedCredentialFailsClosed(t *testing.T) {
	account := uuid.New()
	fp := &fakePairing{account: account, credential: "cred-abc", revoked: true}
	fo := &fakeObservation{}
	srv := serverWithPairing(t, newFakeAuth(), fp, fo)

	req := httptest.NewRequest(http.MethodPost, "/observation/capture", strings.NewReader(captureBody(account, uuid.New())))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer cred-abc")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked capture = %d, want 401", rec.Code)
	}
	if len(fo.ingested) != 0 {
		t.Fatal("a revoked credential reached ingestion — must fail closed before the handler")
	}
}

// TestCaptureCredentialCrossAccountForbidden proves a capture credential scoped to
// one account cannot upload for a different account (cross-account containment).
func TestCaptureCredentialCrossAccountForbidden(t *testing.T) {
	credAccount := uuid.New()
	otherAccount := uuid.New()
	fp := &fakePairing{account: credAccount, credential: "cred-abc"}
	fo := &fakeObservation{}
	srv := serverWithPairing(t, newFakeAuth(), fp, fo)

	req := httptest.NewRequest(http.MethodPost, "/observation/capture", strings.NewReader(captureBody(otherAccount, uuid.New())))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer cred-abc")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-account capture = %d, want 403", rec.Code)
	}
	if len(fo.ingested) != 0 {
		t.Fatal("cross-account capture reached ingestion")
	}
}

// TestMintCodeRequiresPairPermission proves minting a code needs a human session
// with extension.pair — Internal (not a pairing actor) is denied.
func TestMintCodeRequiresPairPermission(t *testing.T) {
	fa := newFakeAuth()
	fa.principals["tok-internal"] = principal(perm.RoleInternal)
	fp := &fakePairing{account: uuid.New(), credential: "x"}
	srv := serverWithPairing(t, fa, fp, &fakeObservation{})

	req := httptest.NewRequest(http.MethodPost, "/ext/pairing/code", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-internal"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("internal mint = %d, want 403", rec.Code)
	}
}

// TestMintCodeOwnerSucceeds proves an owner session mints a code bound to its org.
func TestMintCodeOwnerSucceeds(t *testing.T) {
	fa := newFakeAuth()
	p := principal(perm.RoleOwner)
	fa.principals["tok-owner"] = p
	fp := &fakePairing{account: uuid.New(), credential: "x"}
	srv := serverWithPairing(t, fa, fp, &fakeObservation{})

	req := httptest.NewRequest(http.MethodPost, "/ext/pairing/code", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("owner mint = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fp.lastMintOrg != p.OrganizationID {
		t.Fatalf("mint org = %v, want %v", fp.lastMintOrg, p.OrganizationID)
	}
}

// TestClaimIsPublicNoSession proves the claim route carries no session — the
// extension, which is not logged in, exchanges its code without a cookie.
func TestClaimIsPublicNoSession(t *testing.T) {
	fp := &fakePairing{account: uuid.New(), credential: "cred-abc"}
	srv := serverWithPairing(t, newFakeAuth(), fp, &fakeObservation{})

	req := httptest.NewRequest(http.MethodPost, "/ext/pairing/claim", strings.NewReader(`{"code":"code-123"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cred-abc") {
		t.Fatalf("claim body missing credential: %s", rec.Body.String())
	}
}

// TestClaimUnknownCodeUnauthorized proves an unknown code fails closed with 401.
func TestClaimUnknownCodeUnauthorized(t *testing.T) {
	fp := &fakePairing{account: uuid.New(), credential: "cred-abc"}
	srv := serverWithPairing(t, newFakeAuth(), fp, &fakeObservation{})

	req := httptest.NewRequest(http.MethodPost, "/ext/pairing/claim", strings.NewReader(`{"code":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown claim = %d, want 401", rec.Code)
	}
}

// TestRevokeRequiresSession proves revoke is a human-session action bound to the
// caller's org.
func TestRevokeRequiresSession(t *testing.T) {
	fa := newFakeAuth()
	p := principal(perm.RoleOwner)
	fa.principals["tok-owner"] = p
	fp := &fakePairing{account: uuid.New(), credential: "x"}
	srv := serverWithPairing(t, fa, fp, &fakeObservation{})

	// No session → 401.
	req := httptest.NewRequest(http.MethodPost, "/ext/pairing/revoke", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoke without session = %d, want 401", rec.Code)
	}

	// With owner session → 204 and the org is revoked.
	req = httptest.NewRequest(http.MethodPost, "/ext/pairing/revoke", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner revoke = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if fp.revokeCalls != 1 || fp.lastRevokeOrg != p.OrganizationID {
		t.Fatalf("revoke not bound to caller org: calls=%d org=%v", fp.revokeCalls, fp.lastRevokeOrg)
	}
}

// TestCaptureNoCredentialNoSessionUnauthorized proves the capture route fails
// closed when neither a capture credential nor a human session is presented.
func TestCaptureNoCredentialNoSessionUnauthorized(t *testing.T) {
	fp := &fakePairing{account: uuid.New(), credential: "cred-abc"}
	srv := serverWithPairing(t, newFakeAuth(), fp, &fakeObservation{})
	req := httptest.NewRequest(http.MethodPost, "/observation/capture", strings.NewReader(captureBody(uuid.New(), uuid.New())))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous capture = %d, want 401", rec.Code)
	}
}
