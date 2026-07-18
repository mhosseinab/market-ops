package outcome_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/outcome"
)

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping outcome DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// openClosedWindow opens an outcome window whose seven days have already elapsed
// (opened 8 days ago), so it is immediately closable.
func openClosedWindow(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	actionID := uuid.New()
	past := time.Now().UTC().Add(-8 * 24 * time.Hour)
	svc := outcome.NewService(pool).WithClock(func() time.Time { return past })
	if _, err := svc.OpenWindow(context.Background(), actionID, uuid.Nil); err != nil {
		t.Fatalf("open window: %v", err)
	}
	return actionID
}

// measurableSource yields evidence for a measurable, clean-positive outcome.
type measurableSource struct{}

func (measurableSource) Evidence(context.Context, uuid.UUID) (outcome.Inputs, error) {
	return outcome.Inputs{EvidenceComplete: true, ObjectiveImproved: true, ConcurrentMaterialChanges: 0}, nil
}

// TestCloser_NotMeasurableByDefault proves the WIRED close job produces a Not
// Measurable result when the default evidence source reports absent evidence
// (§15.3 fail-closed path) — the honest default while no metric pipeline exists.
func TestCloser_NotMeasurableByDefault(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	actionID := openClosedWindow(t, pool)

	n, err := outcome.NewCloser(pool, nil).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n < 1 {
		t.Fatalf("closed %d windows; want at least 1", n)
	}
	win, err := q.GetOutcomeWindowByAction(ctx, actionID)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	res, err := q.GetOutcomeResult(ctx, win.ID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	if res.Result != string(outcome.NotMeasurable) {
		t.Fatalf("result = %q; want not_measurable", res.Result)
	}
}

// TestCloser_MeasurablePositive proves the WIRED close job computes a §15.3
// measurable result (Positive/High) when the evidence source is measurable.
func TestCloser_MeasurablePositive(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	actionID := openClosedWindow(t, pool)

	n, err := outcome.NewCloser(pool, measurableSource{}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n < 1 {
		t.Fatalf("closed %d windows; want at least 1", n)
	}
	win, _ := q.GetOutcomeWindowByAction(ctx, actionID)
	res, err := q.GetOutcomeResult(ctx, win.ID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	if res.Result != string(outcome.Positive) || res.Confidence != string(outcome.High) {
		t.Fatalf("result/confidence = %q/%q; want positive/high", res.Result, res.Confidence)
	}
}
