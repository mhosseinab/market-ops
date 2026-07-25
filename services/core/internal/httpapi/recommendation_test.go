package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// validRecommendationRow is a minimal, well-formed db.Recommendation with
// valid required money fields — the base every corrupt-JSON test below
// mutates one field of.
func validRecommendationRow() db.Recommendation {
	return db.Recommendation{
		ID:                   uuid.New(),
		MarketplaceAccountID: uuid.New(),
		VariantID:            uuid.New(),
		LineageID:            uuid.New(),
		Version:              1,
		Objective:            "maximize_contribution",
		CurrentPriceMantissa: 100000,
		CurrentPriceCurrency: "IRR",
		CurrentPriceExponent: 0,
		Readiness:            "complete",
		EvidenceQuality:      "verified",
		Assumptions:          []byte(`["a"]`),
		Blockers:             []byte(`[]`),
		Inputs:               []byte(`[]`),
	}
}

// TestGetRecommendationDetail_CorruptJSONFailsClosed is the money/evidence
// silent-swallow fix: corrupt persisted JSONB for a field the wire
// RecommendationDetail schema marks `required` (assumptions, blockers,
// contributionDeductions) must return a 500 (actionable error), NEVER a
// 200-with-silently-emptied field. A degraded-but-200 response would present
// an incomplete PRC-001 record as complete.
func TestGetRecommendationDetailCorruptJSONFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*db.Recommendation)
	}{
		{"corrupt assumptions", func(r *db.Recommendation) { r.Assumptions = []byte(`not-json`) }},
		{"corrupt blockers", func(r *db.Recommendation) { r.Blockers = []byte(`{"not":`) }},
		{"corrupt contribution deductions (inputs)", func(r *db.Recommendation) { r.Inputs = []byte(`[{"Amount":`) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := validRecommendationRow()
			c.mutate(&row)
			srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(&fakeApproval{rec: row}))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/recommendations/detail?recommendationId="+row.ID.String(), nil)
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 (actionable error, not a silently-emptied 200), body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestGetRecommendationDetail_WellFormedJSONStillSucceeds is the positive
// control for the fix above: valid JSON in every field still returns 200.
func TestGetRecommendationDetailWellFormedJSONStillSucceeds(t *testing.T) {
	row := validRecommendationRow()
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(&fakeApproval{rec: row}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/recommendations/detail?recommendationId="+row.ID.String(), nil)
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}
