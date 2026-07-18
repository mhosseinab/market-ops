// Package briefing persists and serves the once-per-business-day daily briefing
// (PRD §6.8, CHAT-010). The briefing is generated FROM the Today ranking — it
// reuses the SAME event.Rank the Today feed uses (via the TodayRanker seam), so
// its event ids and ORDER EQUAL the feed by construction and cannot drift. A
// divergence would be a traceability breach (CHAT-010, never-cut determinism).
//
// briefings + briefing_events are APPEND-ONLY. Generation is idempotent per
// business day: the (account, business_day) unique constraint makes a same-day
// retry a no-op, so a re-run of the River job never produces a duplicate briefing.
package briefing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
)

// TodayRanker yields the account's ranked Today feed (EVT-004). *event.Service
// satisfies it via Today; the briefing calls THIS so the two cannot diverge.
type TodayRanker interface {
	Today(ctx context.Context, account uuid.UUID) ([]event.Ranked, error)
}

// Event is one ranked event snapshot in a briefing. Rank is 1-based and equals
// the Today feed position; EventID equals the Today feed event id (CHAT-010).
type Event struct {
	Rank      int32
	EventID   uuid.UUID
	EventType string
	Severity  string
}

// Briefing is the stored daily briefing for one account/business-day.
type Briefing struct {
	AccountID   uuid.UUID
	BusinessDay time.Time
	GeneratedAt time.Time
	Events      []Event
}

// Service generates and reads daily briefings.
type Service struct {
	pool   *pgxpool.Pool
	ranker TodayRanker
	now    func() time.Time
}

// NewService builds a briefing Service over the pool and the Today ranker.
func NewService(pool *pgxpool.Pool, ranker TodayRanker) *Service {
	return &Service{pool: pool, ranker: ranker, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock (tests only).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// BusinessDay is the current UTC calendar date the briefing covers. Storage is
// UTC (locale-neutral, LOC-001); a Jalali display calendar is a frontend concern.
func (s *Service) BusinessDay() time.Time {
	n := s.now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// GenerateForAccount creates the briefing for one account for the current
// business day, snapshotting the Today ranking (CHAT-010). It is idempotent: on a
// same-day conflict it inserts nothing and reports created=false. The briefing
// header and its ranked events are written in ONE transaction.
func (s *Service) GenerateForAccount(ctx context.Context, account uuid.UUID) (created bool, err error) {
	day := s.BusinessDay()
	ranked, err := s.ranker.Today(ctx, account)
	if err != nil {
		return false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	header, err := q.InsertBriefing(ctx, db.InsertBriefingParams{
		MarketplaceAccountID: account,
		BusinessDay:          pgtype.Date{Time: day, Valid: true},
		GeneratedAt:          s.now().UTC(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Same-day conflict: the briefing already exists. Idempotent no-op.
		return false, nil
	}
	if err != nil {
		return false, err
	}

	for _, r := range ranked {
		if _, err := q.InsertBriefingEvent(ctx, db.InsertBriefingEventParams{
			BriefingID: header.ID,
			Rank:       int32(r.Rank),
			EventID:    r.Event.ID,
			EventType:  r.Event.EventType,
			Severity:   r.Event.Severity,
		}); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// GenerateAll generates the current-business-day briefing for every marketplace
// account (the River job fan-out). It returns the number of briefings NEWLY
// created (idempotent same-day re-runs create zero). An error on one account
// aborts the pass (fail closed) so the job retries rather than silently skipping.
func (s *Service) GenerateAll(ctx context.Context) (int, error) {
	ids, err := db.New(s.pool).ListMarketplaceAccountIDs(ctx)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, id := range ids {
		ok, err := s.GenerateForAccount(ctx, id)
		if err != nil {
			return created, err
		}
		if ok {
			created++
		}
	}
	return created, nil
}

// Get returns the stored briefing for an account/business-day (the GET /briefing
// read). It returns pgx.ErrNoRows when no briefing exists for that day.
func (s *Service) Get(ctx context.Context, account uuid.UUID, day time.Time) (Briefing, error) {
	q := db.New(s.pool)
	header, err := q.GetBriefingByAccountDay(ctx, db.GetBriefingByAccountDayParams{
		MarketplaceAccountID: account,
		BusinessDay:          pgtype.Date{Time: day.UTC(), Valid: true},
	})
	if err != nil {
		return Briefing{}, err
	}
	rows, err := q.ListBriefingEvents(ctx, header.ID)
	if err != nil {
		return Briefing{}, err
	}
	events := make([]Event, 0, len(rows))
	for _, r := range rows {
		events = append(events, Event{
			Rank:      r.Rank,
			EventID:   r.EventID,
			EventType: r.EventType,
			Severity:  r.Severity,
		})
	}
	return Briefing{
		AccountID:   header.MarketplaceAccountID,
		BusinessDay: header.BusinessDay.Time,
		GeneratedAt: header.GeneratedAt,
		Events:      events,
	}, nil
}
