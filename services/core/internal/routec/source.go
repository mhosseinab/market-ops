package routec

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// DBTargetSource enumerates active observation targets by tier from the store. A
// target deactivated by identity reopen is excluded by the query's active=true
// filter, so a reopened identity is never fetched.
type DBTargetSource struct {
	pool *pgxpool.Pool
}

// NewDBTargetSource builds the DB-backed target source.
func NewDBTargetSource(pool *pgxpool.Pool) *DBTargetSource {
	return &DBTargetSource{pool: pool}
}

// TargetsByTier returns the tier's active targets as observer refs.
func (s *DBTargetSource) TargetsByTier(ctx context.Context, tier observation.Tier) ([]TargetRef, error) {
	rows, err := db.New(s.pool).ListActiveTargetsByTier(ctx, string(tier))
	if err != nil {
		return nil, fmt.Errorf("routec: list active targets by tier: %w", err)
	}
	out := make([]TargetRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, TargetRef{
			Account:         r.MarketplaceAccountID,
			TargetID:        r.ID,
			NativeVariantID: r.NativeVariantID,
			NativeProductID: r.NativeProductID,
			Tier:            observation.Tier(r.Tier),
		})
	}
	return out, nil
}
