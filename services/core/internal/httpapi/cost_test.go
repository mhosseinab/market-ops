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
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// fakeCost is a CostService stub for transport tests.
type fakeCost struct {
	preview   cost.Preview
	commit    cost.CommitResult
	profile   db.CostProfile
	profiles  []db.CostProfile
	readiness db.MarginReadiness
	err       error

	lastPreview cost.PreviewInput
	lastCommit  uuid.UUID

	// Tenant-scoped read recording (issue #131): the *ForOrg reads record the org they
	// were called with so a transport test can assert the handler derives scope from
	// the authenticated principal, never from the request param.
	lastOrg     uuid.UUID
	lastVariant uuid.UUID
	lastBatch   uuid.UUID
}

func (f *fakeCost) PreviewImport(_ context.Context, in cost.PreviewInput) (cost.Preview, error) {
	f.lastPreview = in
	return f.preview, f.err
}
func (f *fakeCost) GetPreviewForOrg(_ context.Context, org, batchID uuid.UUID) (cost.Preview, error) {
	f.lastOrg, f.lastBatch = org, batchID
	return f.preview, f.err
}
func (f *fakeCost) CommitImport(_ context.Context, batchID, _ uuid.UUID) (cost.CommitResult, error) {
	f.lastCommit = batchID
	return f.commit, f.err
}
func (f *fakeCost) EnterSingleCost(context.Context, cost.SingleCostInput) (db.CostProfile, error) {
	return f.profile, f.err
}
func (f *fakeCost) CostProfileAtForOrg(_ context.Context, org, variant uuid.UUID, _ time.Time) ([]db.CostProfile, error) {
	f.lastOrg, f.lastVariant = org, variant
	return f.profiles, f.err
}
func (f *fakeCost) GetReadinessForOrg(_ context.Context, org, variant uuid.UUID) (db.MarginReadiness, error) {
	f.lastOrg, f.lastVariant = org, variant
	return f.readiness, f.err
}

// TestCostRoutesFailClosedWhenUnwired asserts every /cost route returns a
// structured 503 when no cost service is injected.
func TestCostRoutesFailClosedWhenUnwired(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger())
	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/cost/import/preview", `{"marketplaceAccountId":"` + uuid.New().String() + `","csv":"sku,cogs\nA,1\n"}`},
		{http.MethodGet, "/cost/import?batchId=" + uuid.New().String(), ""},
		{http.MethodPost, "/cost/import/commit", `{"batchId":"` + uuid.New().String() + `"}`},
		{http.MethodPost, "/cost/value", `{"marketplaceAccountId":"` + uuid.New().String() + `","variantId":"` + uuid.New().String() + `","component":"cogs","rawValue":"10"}`},
		{http.MethodGet, "/cost/profiles?variantId=" + uuid.New().String(), ""},
		{http.MethodGet, "/cost/readiness?variantId=" + uuid.New().String(), ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d, want 503, body=%s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// TestSingleValuePercentRejectedAs400 is the #40 seam completion at the transport
// boundary: a percent token entered via single-value entry is rejected seller
// input, not a server fault. EnterSingleCost surfaces cost.ErrPercentNotMoney,
// which must classify as HTTP 400 with the stable, disposition-parity code
// PERCENT_NOT_MONEY (mirrors the CSV preview reason percent_not_money) — never
// the default 500/COST_ERROR arm that pollutes 5xx budgets.
func TestSingleValuePercentRejectedAs400(t *testing.T) {
	fake := &fakeCost{err: cost.ErrPercentNotMoney}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithCost(fake))

	body := `{"marketplaceAccountId":"` + uuid.New().String() +
		`","variantId":"` + uuid.New().String() +
		`","component":"cogs","rawValue":"۱۰٪"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/cost/value", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Code != "PERCENT_NOT_MONEY" {
		t.Fatalf("error code = %q, want PERCENT_NOT_MONEY", env.Code)
	}
}

// TestPreviewCommitRoundTrip maps the preview and commit models onto the contract.
func TestPreviewCommitRoundTrip(t *testing.T) {
	account := uuid.New()
	batch := uuid.New()
	variant := uuid.New()
	fake := &fakeCost{
		preview: cost.Preview{
			Batch: db.CostImportBatch{ID: batch, MarketplaceAccountID: account, Status: "preview", AcceptCount: 1, DuplicateCount: 0},
			Rows: []db.CostImportRow{{
				RowNumber: 1, RawSku: "A", Component: "cogs", RawValue: "1500", NormalizedValue: "1500",
				Disposition: "accept",
			}},
		},
		commit: cost.CommitResult{
			Batch:            db.CostImportBatch{ID: batch, Status: "committed"},
			CommittedRows:    1,
			AffectedVariants: []uuid.UUID{variant},
		},
	}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithCost(fake))

	// Preview.
	rec := httptest.NewRecorder()
	body := `{"marketplaceAccountId":"` + account.String() + `","csv":"sku,cogs\nA,1500\n"}`
	req := httptest.NewRequest(http.MethodPost, "/cost/import/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var pv gateway.CostImportPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if pv.Status != "preview" || len(pv.Rows) != 1 || pv.Rows[0].Disposition != "accept" {
		t.Fatalf("unexpected preview: %+v", pv)
	}

	// Commit.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/cost/import/commit", strings.NewReader(`{"batchId":"`+batch.String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastCommit != batch {
		t.Fatalf("commit batch id = %s, want %s", fake.lastCommit, batch)
	}
	var cr gateway.CostImportCommitResult
	if err := json.Unmarshal(rec.Body.Bytes(), &cr); err != nil {
		t.Fatalf("decode commit: %v", err)
	}
	if cr.Status != "committed" || cr.CommittedRows != 1 || len(cr.AffectedVariantIds) != 1 {
		t.Fatalf("unexpected commit result: %+v", cr)
	}
}
