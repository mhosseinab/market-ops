package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/diagnostics"
)

// fakeDiagnostics is a DiagnosticsService stub recording the org it was called
// with, so a test can assert the handler derives scope from the authenticated
// principal, never from request input.
type fakeDiagnostics struct {
	report  diagnostics.Report
	err     error
	lastOrg uuid.UUID
	lastAcc uuid.UUID
	lastVar uuid.UUID
}

func (f *fakeDiagnostics) GetVariantDiagnostics(_ context.Context, org, acc, variant uuid.UUID) (diagnostics.Report, error) {
	f.lastOrg, f.lastAcc, f.lastVar = org, acc, variant
	return f.report, f.err
}

// TestDiagnosticsRouteFailsClosedWhenUnwired asserts GET /catalog/product-diagnostics
// returns a structured 503 when no read model is injected (fail closed).
func TestDiagnosticsRouteFailsClosedWhenUnwired(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger())
	rec := httptest.NewRecorder()
	path := "/catalog/product-diagnostics?marketplaceAccountId=" + uuid.New().String() +
		"&variantId=" + uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
}

// TestListProductDiagnosticsDerivesOrgAndMapsResults asserts the org is taken from
// the authenticated principal and the read-only report is mapped, including the
// named field/rule provenance, pass/warn results, and observed metadata.
func TestListProductDiagnosticsDerivesOrgAndMapsResults(t *testing.T) {
	acct := uuid.New()
	variant := uuid.New()
	length := 11
	fd := &fakeDiagnostics{report: diagnostics.Report{
		VariantID:            variant.String(),
		MarketplaceAccountID: acct.String(),
		EvaluatedAt:          time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
		Items: []diagnostics.Diagnostic{
			{
				Entity: diagnostics.EntityVariant, Field: diagnostics.FieldTitle,
				RuleID: diagnostics.RuleTitlePresent, RuleVersion: diagnostics.RuleVersionV1,
				Result:      diagnostics.ResultPass,
				Observed:    diagnostics.ObservedMeta{State: diagnostics.StatePresent, CharacterLength: &length},
				EvidenceRef: "catalog/variant/7719004",
				CapturedAt:  time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC),
			},
			{
				Entity: diagnostics.EntityListing, Field: diagnostics.FieldDescription,
				RuleID: diagnostics.RuleDescriptionPresent, RuleVersion: diagnostics.RuleVersionV1,
				Result:      diagnostics.ResultWarn,
				Observed:    diagnostics.ObservedMeta{State: diagnostics.StateNotObserved},
				EvidenceRef: "catalog/listing/8842213",
				CapturedAt:  time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC),
			},
		},
	}}
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithDiagnostics(fd))

	rec := httptest.NewRecorder()
	path := "/catalog/product-diagnostics?marketplaceAccountId=" + acct.String() + "&variantId=" + variant.String()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if fd.lastOrg != orgID {
		t.Fatalf("service org = %v, want authenticated org %v", fd.lastOrg, orgID)
	}
	if fd.lastAcc != acct || fd.lastVar != variant {
		t.Fatalf("account/variant not forwarded: acc=%v var=%v", fd.lastAcc, fd.lastVar)
	}
	var got struct {
		Items []struct {
			Entity      string `json:"entity"`
			Field       string `json:"field"`
			RuleID      string `json:"ruleId"`
			RuleVersion string `json:"ruleVersion"`
			Result      string `json:"result"`
			Observed    struct {
				State           string `json:"state"`
				CharacterLength *int   `json:"characterLength"`
			} `json:"observed"`
			EvidenceRef string `json:"evidenceRef"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(got.Items))
	}
	title := got.Items[0]
	if title.Field != "title" || title.RuleID != "listing.title.present" || title.Result != "pass" {
		t.Fatalf("title provenance/result wrong: %+v", title)
	}
	if title.Observed.State != "present" || title.Observed.CharacterLength == nil || *title.Observed.CharacterLength != 11 {
		t.Fatalf("title observed metadata wrong: %+v", title.Observed)
	}
	desc := got.Items[1]
	if desc.Field != "description" || desc.Result != "warn" || desc.Observed.State != "not_observed" {
		t.Fatalf("description must fail closed to not_observed/warn: %+v", desc)
	}
	if desc.Observed.CharacterLength != nil {
		t.Fatalf("not_observed must carry no length metadata")
	}
}

// TestDiagnosticsCrossAccountReturns404 asserts a foreign/unknown account fails
// closed as 404 (no existence oracle), never leaking another account's data.
func TestDiagnosticsCrossAccountReturns404(t *testing.T) {
	fd := &fakeDiagnostics{err: diagnostics.ErrAccountNotFound}
	fa := newFakeAuth()
	ownerSession(fa)
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithDiagnostics(fd))

	rec := httptest.NewRecorder()
	path := "/catalog/product-diagnostics?marketplaceAccountId=" + uuid.New().String() + "&variantId=" + uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// TestDiagnosticsUnknownVariantReturns404 asserts an unknown/foreign variant is a
// 404, never another variant's diagnostics.
func TestDiagnosticsUnknownVariantReturns404(t *testing.T) {
	fd := &fakeDiagnostics{err: diagnostics.ErrVariantNotFound}
	fa := newFakeAuth()
	ownerSession(fa)
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithDiagnostics(fd))

	rec := httptest.NewRecorder()
	path := "/catalog/product-diagnostics?marketplaceAccountId=" + uuid.New().String() + "&variantId=" + uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}
