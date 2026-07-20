package outcome_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
// (opened 8 days ago), so it is immediately closable. cardID may be uuid.Nil.
func openClosedWindow(t *testing.T, pool *pgxpool.Pool, cardID uuid.UUID) uuid.UUID {
	t.Helper()
	actionID := uuid.New()
	past := time.Now().UTC().Add(-8 * 24 * time.Hour)
	svc := outcome.NewService(pool).WithClock(func() time.Time { return past })
	if _, err := svc.OpenWindow(context.Background(), actionID, cardID); err != nil {
		t.Fatalf("open window: %v", err)
	}
	return actionID
}

func resultFor(t *testing.T, q *db.Queries, actionID uuid.UUID) (string, bool) {
	t.Helper()
	ctx := context.Background()
	win, err := q.GetOutcomeWindowByAction(ctx, actionID)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	res, err := q.GetOutcomeResult(ctx, win.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	return res.Result, true
}

// --- fake sources exercising the closer's disposition handling -------------------

type fixedSource struct {
	res outcome.Resolution
	err error
}

func (f fixedSource) Evidence(context.Context, uuid.UUID) (outcome.Resolution, error) {
	return f.res, f.err
}

// TestCloser_SourceErrorNeverNotMeasurable is the crux acceptance test: a source
// ERROR must NOT close the window (and NEVER as NotMeasurable); it stays unclosed
// and RunOnce returns the error so the pass is retried.
func TestCloser_SourceErrorNeverNotMeasurable(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	actionID := openClosedWindow(t, pool, uuid.Nil)

	n, err := outcome.NewCloser(pool, fixedSource{err: errors.New("query timeout")}).RunOnce(ctx)
	if err == nil {
		t.Fatalf("RunOnce: want error from source failure, got nil")
	}
	if n != 0 {
		t.Fatalf("closed %d windows on source error; want 0", n)
	}
	if _, ok := resultFor(t, q, actionID); ok {
		t.Fatalf("source error must NOT append a result (would fabricate NotMeasurable)")
	}
}

// TestCloser_IncompleteLeavesWindowOpen proves an Incomplete resolution (not yet
// measurable / pending write) leaves the window unclosed for a later pass — never
// closed as NotMeasurable.
func TestCloser_IncompleteLeavesWindowOpen(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	actionID := openClosedWindow(t, pool, uuid.Nil)

	n, err := outcome.NewCloser(pool, fixedSource{res: outcome.Resolution{Disposition: outcome.DispositionIncomplete}}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 0 {
		t.Fatalf("closed %d incomplete windows; want 0", n)
	}
	if _, ok := resultFor(t, q, actionID); ok {
		t.Fatalf("incomplete window must stay unclosed")
	}
}

// TestCloser_AbsentClosesNotMeasurable proves the ONLY legitimate NotMeasurable
// path: a source that reports genuinely-absent evidence closes NotMeasurable.
func TestCloser_AbsentClosesNotMeasurable(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	actionID := openClosedWindow(t, pool, uuid.Nil)

	n, err := outcome.NewCloser(pool, fixedSource{res: outcome.Resolution{Disposition: outcome.DispositionAbsent}}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n < 1 {
		t.Fatalf("closed %d windows; want at least 1", n)
	}
	got, ok := resultFor(t, q, actionID)
	if !ok || got != string(outcome.NotMeasurable) {
		t.Fatalf("result = %q (present=%v); want not_measurable", got, ok)
	}
}

// TestCloser_MeasurableClasses proves the wired close job produces each measurable
// §15.3 class from complete evidence — the classification the nil-source bug lost.
func TestCloser_MeasurableClasses(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	cases := []struct {
		name string
		in   outcome.Inputs
		want outcome.Result
	}{
		{"positive", outcome.Inputs{EvidenceComplete: true, ObjectiveImproved: true}, outcome.Positive},
		{"negative", outcome.Inputs{EvidenceComplete: true, ObjectiveWorsened: true}, outcome.Negative},
		{"neutral", outcome.Inputs{EvidenceComplete: true, WithinMateriality: true}, outcome.Neutral},
		{"inconclusive", outcome.Inputs{EvidenceComplete: true, AttributionBlocked: true}, outcome.Inconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actionID := openClosedWindow(t, pool, uuid.Nil)
			src := fixedSource{res: outcome.Resolution{Disposition: outcome.DispositionMeasurable, Inputs: tc.in}}
			if _, err := outcome.NewCloser(pool, src).RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			got, ok := resultFor(t, q, actionID)
			if !ok || got != string(tc.want) {
				t.Fatalf("result = %q (present=%v); want %q", got, ok, tc.want)
			}
		})
	}
}

// TestCloser_RestartClosesOnce proves idempotency under replay: a second pass over
// an already-closed window appends no second result and closes nothing.
func TestCloser_RestartClosesOnce(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	actionID := openClosedWindow(t, pool, uuid.Nil)
	src := fixedSource{res: outcome.Resolution{Disposition: outcome.DispositionMeasurable,
		Inputs: outcome.Inputs{EvidenceComplete: true, ObjectiveImproved: true}}}

	n1, err := outcome.NewCloser(pool, src).RunOnce(ctx)
	if err != nil || n1 < 1 {
		t.Fatalf("first pass closed %d (err=%v); want >=1", n1, err)
	}
	n2, err := outcome.NewCloser(pool, src).RunOnce(ctx)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("replay closed %d windows; want 0 (close-once)", n2)
	}
	if got, ok := resultFor(t, q, actionID); !ok || got != string(outcome.Positive) {
		t.Fatalf("result = %q (present=%v); want stable positive", got, ok)
	}
}
