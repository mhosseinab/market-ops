package recommendation

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// Store is implemented by *Service: the runtime producer persists through it.
var _ Store = (*Service)(nil)

// CurrentRecommendationForLineage returns the greatest-version recommendation for a
// lineage. A lineage with no rows yet is reported as (zero, false, nil) — NOT an error
// — so the producer treats "never produced" and "produced" uniformly through the dedup
// gate. Any other DB error is surfaced for retry.
func (s *Service) CurrentRecommendationForLineage(ctx context.Context, lineage uuid.UUID) (db.Recommendation, bool, error) {
	row, err := db.New(s.pool).GetCurrentRecommendation(ctx, lineage)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Recommendation{}, false, nil
	}
	if err != nil {
		return db.Recommendation{}, false, err
	}
	return row, true, nil
}

// ProduceVersion persists rec as a new append-only version in lineage and, when rec is
// Approvable, mints its Draft approval card — in ONE transaction. A blocked/incomplete
// recommendation commits with its exact blocker reasons and NO card (no control, PRC-
// 002). It returns the persisted row and whether a card was minted. The atomic commit
// is what makes the producer's version-based dedup safe under retry: a committed
// version always carries its card, so a replayed pass skips cleanly rather than
// leaving an approvable version permanently cardless.
func (s *Service) ProduceVersion(ctx context.Context, lineage, account uuid.UUID, rec Recommendation) (db.Recommendation, bool, error) {
	params, err := buildInsertRecommendationParams(lineage, rec)
	if err != nil {
		return db.Recommendation{}, false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Recommendation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	persisted, err := q.InsertRecommendation(ctx, params)
	if err != nil {
		return db.Recommendation{}, false, err
	}

	cardCreated := false
	if rec.Approvable() {
		binding, ok := rec.BuildBinding()
		if !ok {
			// Defensive: Approvable() and BuildBinding() agree by construction; if they
			// ever diverge, fail closed rather than persist an approvable row with no
			// control-bearing card.
			return db.Recommendation{}, false, ErrRejectedTransition
		}
		price, ok := rec.ProposedPrice.Get()
		if !ok {
			return db.Recommendation{}, false, ErrRejectedTransition
		}
		if _, err := s.mintDraftCardTx(ctx, q, persisted.ID, lineage, account, binding, price); err != nil {
			return db.Recommendation{}, false, err
		}
		cardCreated = true
	}

	if err := tx.Commit(ctx); err != nil {
		return db.Recommendation{}, false, err
	}
	return persisted, cardCreated, nil
}

// DBEventSource is the runtime EventSource: it reads the durable, account-wide set of
// open|updated market events awaiting a recommendation directly from committed data.
// It holds no cursor — a re-scan of the same committed events is safe because
// production is idempotent per (event, evidence version).
type DBEventSource struct{ pool *pgxpool.Pool }

// NewEventSource wires the DB-backed event source over the pool.
func NewEventSource(pool *pgxpool.Pool) *DBEventSource { return &DBEventSource{pool: pool} }

var _ EventSource = (*DBEventSource)(nil)

// Eligible returns every open|updated market event as an EligibleEvent, carrying the
// evidence_update_count as the monotonic dedup/context token.
func (d *DBEventSource) Eligible(ctx context.Context) ([]EligibleEvent, error) {
	rows, err := db.New(d.pool).ListEligibleRecommendationEvents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]EligibleEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, EligibleEvent{
			EventID:         r.ID,
			AccountID:       r.MarketplaceAccountID,
			VariantID:       r.VariantID,
			EvidenceVersion: int64(r.EvidenceUpdateCount),
		})
	}
	return out, nil
}

// DarkInputResolver is the fail-closed default InputResolver. It resolves NO
// authoritative input and always returns ErrInputsUnavailable, so a booted core
// consumes eligible events and PARKS them (observable via the parked counter) rather
// than fabricating a price or inferring a missing input. It is the recommendation-plane
// analogue of the execution plane's dark DefaultResolver: the live authoritative
// resolver (identity/price/cost/boundary/permission/policy over committed data) is
// wired under the same gated enablement as the execution write path; until then
// production is honest and never approves on non-live truth (§4.6 quarantine-over-
// inference; §8 free text / fabrication never approves).
type DarkInputResolver struct{}

// NewDarkInputResolver builds the fail-closed default resolver.
func NewDarkInputResolver() *DarkInputResolver { return &DarkInputResolver{} }

var _ InputResolver = (*DarkInputResolver)(nil)

// Resolve always fails closed with ErrInputsUnavailable (dark posture).
func (DarkInputResolver) Resolve(context.Context, EligibleEvent) (AssembleInput, error) {
	return AssembleInput{}, ErrInputsUnavailable
}
