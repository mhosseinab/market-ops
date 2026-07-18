package outcome

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrWindowOpen — a result was requested before the seven-day window closed.
var ErrWindowOpen = errors.New("outcome: window has not closed")

// ErrNoWindow — there is no outcome window for the action.
var ErrNoWindow = errors.New("outcome: no window for action")

// Service persists the append-only OUT-001 windows and their §15.3 results.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService wires the outcome persistence service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// OpenWindow opens the seven-day window for a reconciled action (OUT-001). It is
// idempotent (UNIQUE action_id); a duplicate open returns the existing window.
func (s *Service) OpenWindow(ctx context.Context, actionID, cardID uuid.UUID) (db.OutcomeWindow, error) {
	w := Open(s.now())
	q := db.New(s.pool)
	row, err := q.OpenOutcomeWindow(ctx, db.OpenOutcomeWindowParams{
		ActionID: actionID,
		CardID:   optionalUUID(cardID),
		OpenedAt: w.OpenedAt,
		ClosesAt: w.ClosesAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return q.GetOutcomeWindowByAction(ctx, actionID)
	}
	return row, err
}

// ComputeAndAppend closes an action's window: it evaluates the §15.3 rule over in
// and appends the result + confidence exactly once. It refuses to compute before
// the window has closed (the result is computed once the window closes — OUT-001).
func (s *Service) ComputeAndAppend(ctx context.Context, actionID uuid.UUID, in Inputs) (db.OutcomeResult, error) {
	q := db.New(s.pool)
	win, err := q.GetOutcomeWindowByAction(ctx, actionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.OutcomeResult{}, ErrNoWindow
	}
	if err != nil {
		return db.OutcomeResult{}, err
	}
	if s.now().Before(win.ClosesAt) {
		return db.OutcomeResult{}, ErrWindowOpen
	}
	result, confidence := Evaluate(in)
	row, err := q.AppendOutcomeResult(ctx, db.AppendOutcomeResultParams{
		WindowID:   win.ID,
		Result:     string(result),
		Confidence: string(confidence),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Already computed (append-only, one per window): return the existing.
		return q.GetOutcomeResult(ctx, win.ID)
	}
	return row, err
}

// View is an action's outcome window plus (when computed) its §15.3 result.
type View struct {
	Window    db.OutcomeWindow
	HasResult bool
	Result    db.OutcomeResult
}

// Get returns the outcome window for an action and its computed result when
// present. It fails closed with ErrNoWindow when no window exists.
func (s *Service) Get(ctx context.Context, actionID uuid.UUID) (View, error) {
	q := db.New(s.pool)
	win, err := q.GetOutcomeWindowByAction(ctx, actionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNoWindow
	}
	if err != nil {
		return View{}, err
	}
	res, err := q.GetOutcomeResult(ctx, win.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{Window: win}, nil
	}
	if err != nil {
		return View{}, err
	}
	return View{Window: win, HasResult: true, Result: res}, nil
}

// ListClosable returns windows whose seven days elapsed and that have no computed
// result yet (the scheduled close job consumes these).
func (s *Service) ListClosable(ctx context.Context) ([]db.OutcomeWindow, error) {
	return db.New(s.pool).ListClosableOutcomeWindows(ctx, s.now())
}

func optionalUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
