package execution

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// OwnedPriceSource supplies the observed owned-offer prices for a variant since a
// given instant, as authoritative Money (EXE-005 matching). It is the seam the
// recommend-only reconciler correlates approved prices against.
//
// The PRODUCTION implementation may return owned-offer prices ONLY once the source
// money unit is verified and the region transform is active (Gate 0a / S35) — until
// then it returns none, so no owned-offer value is ever coerced into Money on an
// unverified unit (§9.1 quarantine), and awaiting actions correctly Lapse after 24h
// rather than false-matching. Tests inject a source with known Money observations.
type OwnedPriceSource interface {
	OwnedPrices(ctx context.Context, variant uuid.UUID, since time.Time) ([]OwnedPriceObservation, error)
}

// RecommendOnlyReconciler is the periodic EXE-005 matcher: for each awaiting
// recommend-only action it matches the approved price against owned-price
// observations within 24h (→ ExternallyExecuted) or lapses it once the window has
// passed (→ Lapsed). It is the production caller that moves recommend-only actions
// forward — recommend-only is the ONLY reachable execution mode while writes are
// dark, so this is the default-production WVRA (§5.1) signal.
type RecommendOnlyReconciler struct {
	pool   *pgxpool.Pool
	source OwnedPriceSource
	tel    *telemetry
	now    func() time.Time
	batch  int32
}

// NewRecommendOnlyReconciler wires the matcher. A nil source defaults to the
// no-owned-price source (everything lapses; the fail-closed dark behaviour).
func NewRecommendOnlyReconciler(pool *pgxpool.Pool, source OwnedPriceSource) *RecommendOnlyReconciler {
	if source == nil {
		source = noOwnedPrices{}
	}
	return &RecommendOnlyReconciler{
		pool: pool, source: source, tel: newTelemetry(nil),
		now: func() time.Time { return time.Now().UTC() }, batch: 500,
	}
}

// WithClock overrides the clock (tests).
func (r *RecommendOnlyReconciler) WithClock(now func() time.Time) *RecommendOnlyReconciler {
	r.now = now
	return r
}

// Summary reports what one reconciliation pass did.
type Summary struct {
	Scanned            int
	ExternallyExecuted int
	Lapsed             int
}

// RunOnce processes one bounded batch of awaiting recommend-only actions (EXE-005).
func (r *RecommendOnlyReconciler) RunOnce(ctx context.Context) (Summary, error) {
	q := db.New(r.pool)
	rows, err := q.ListAwaitingRecommendOnlyActions(ctx, r.batch)
	if err != nil {
		return Summary{}, err
	}
	now := r.now()
	var sum Summary
	for _, row := range rows {
		sum.Scanned++
		approved, err := money.New(row.ApprovedPriceMantissa, row.ApprovedPriceCurrency, int8(row.ApprovedPriceExponent))
		if err != nil {
			// A malformed stored price is skipped, never coerced.
			continue
		}
		obs, err := r.source.OwnedPrices(ctx, row.VariantID, row.ApprovedAt)
		if err != nil {
			return sum, err
		}
		res := Match(MatchInput{ApprovedPrice: approved, ApprovedAt: row.ApprovedAt, Observations: obs, Now: now})
		switch res.State {
		case StateExternallyExecuted:
			matchedAt := now
			if res.Matched != nil {
				matchedAt = res.Matched.ObservedAt
			}
			if err := r.transition(ctx, row, StateExternallyExecuted, matchedAt); err != nil {
				return sum, err
			}
			sum.ExternallyExecuted++
		case StateLapsed:
			if err := r.transition(ctx, row, StateLapsed, time.Time{}); err != nil {
				return sum, err
			}
			sum.Lapsed++
		default:
			// Still awaiting inside the window; nothing to do this pass.
		}
	}
	return sum, nil
}

// transition advances a recommend-only action to a terminal EXE-005 state and
// appends its audit record in ONE transaction (a terminal recommend-only state
// never lands without its audit row). The guarded update makes it idempotent.
func (r *RecommendOnlyReconciler) transition(ctx context.Context, row db.RecommendOnlyAction, to RecommendOnlyState, matchedAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	matched := pgtype.Timestamptz{}
	if !matchedAt.IsZero() {
		matched = pgtype.Timestamptz{Time: matchedAt, Valid: true}
	}
	updated, err := q.SetRecommendOnlyState(ctx, db.SetRecommendOnlyStateParams{
		ActionID:             row.ActionID,
		State:                string(to),
		MatchedObservationAt: matched,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already resolved by a concurrent pass; idempotent no-op.
	}
	if err != nil {
		return err
	}
	if _, err := audit.Append(ctx, q, audit.Event{
		ActionID: row.ActionID, CardID: row.CardID, AccountID: row.MarketplaceAccountID,
		Type:         audit.EventRecommendOnly,
		Detail:       map[string]any{"state": to, "matched_at": matchedAt},
		CardSnapshot: map[string]any{"recommend_only_state": updated.State},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// noOwnedPrices is the fail-closed default source: it returns no owned-offer
// prices, so no match can be inferred on an unverified money unit and awaiting
// actions lapse after their 24h window. Replaced once S35 verifies the unit.
type noOwnedPrices struct{}

func (noOwnedPrices) OwnedPrices(context.Context, uuid.UUID, time.Time) ([]OwnedPriceObservation, error) {
	return nil, nil
}
