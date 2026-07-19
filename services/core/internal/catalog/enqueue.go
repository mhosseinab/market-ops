package catalog

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// SyncEnqueuer adapts the transactional EnqueueIncrementalSync helper to the
// connector's SyncEnqueuer seam: it owns the short-lived transaction so the
// connector service can initiate a catalog sync without importing River or the
// pool. The run row and the River job commit together (transactional enqueue,
// jobs pkg invariant): the job becomes visible only if the sync-run row commits,
// so a crash between the two can never leave a job without its progress row.
type SyncEnqueuer struct {
	client *jobs.Client
	pool   *pgxpool.Pool
}

// NewSyncEnqueuer wires the enqueuer over the platform River client and pool.
func NewSyncEnqueuer(client *jobs.Client, pool *pgxpool.Pool) *SyncEnqueuer {
	return &SyncEnqueuer{client: client, pool: pool}
}

// EnqueueIncrementalSync opens a transaction, creates the incremental sync run,
// and enqueues its job atomically, returning the new run id. It is org-scoped so
// the sync's connector reads stay org-predicated (S8-AUTHZ-001).
func (e *SyncEnqueuer) EnqueueIncrementalSync(ctx context.Context, organizationID, accountID uuid.UUID) (uuid.UUID, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("catalog: begin sync enqueue tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	runID, err := EnqueueIncrementalSync(ctx, e.client, tx, organizationID, accountID)
	if err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("catalog: commit sync enqueue tx: %w", err)
	}
	return runID, nil
}
