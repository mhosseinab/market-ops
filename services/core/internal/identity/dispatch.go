package identity

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// ReopenDispatcher durably enqueues the reopen-invalidation intent for a mapping that
// has just been reopened (issue #49, §16). Its enqueue runs INSIDE the reopen
// transaction (tx), so the intent commits ATOMICALLY with the guarded state
// transition, the append-only audit row, and the append-only
// recommendation_invalidation_events row: a rollback discards ALL of them, and a
// committed reopen ALWAYS carries its durable delivery intent. Delivery therefore no
// longer depends on a fire-and-forget post-commit callback — the defect issue #49
// closes (a committed reopen could be permanently lost when the sink call, or the
// process, died after commit).
type ReopenDispatcher interface {
	DispatchReopenTx(ctx context.Context, tx pgx.Tx, ev MappingReopenedEvent) error
}

// JobReopenDispatcher is the River-backed ReopenDispatcher: it enqueues the durable
// mapping_reopened intent transactionally when a mapping is reopened. It imports only
// the platform jobs package (never internal/recommendation or internal/routec), so the
// identity → jobs edge stays acyclic; the worker side wires the idempotent consumer in
// cmd/core/main.go. The River job row IS the durable intent/outbox record — no bespoke
// intent table or claim protocol is invented, and the append-only event row is NEVER
// mutated to record delivery (delivery state lives in the River job store).
type JobReopenDispatcher struct{ client *jobs.Client }

// NewJobReopenDispatcher wires the dispatcher over the platform River client.
func NewJobReopenDispatcher(client *jobs.Client) *JobReopenDispatcher {
	return &JobReopenDispatcher{client: client}
}

var _ ReopenDispatcher = (*JobReopenDispatcher)(nil)

// DispatchReopenTx enqueues the durable reopen-invalidation intent on the caller's
// reopen transaction, carrying the JSON-safe business data from the event (plan §4.8)
// so the worker and telemetry can reconstruct it from the job alone. The intent is
// unique by dedup key, so a duplicate enqueue collapses to one durable record.
func (d *JobReopenDispatcher) DispatchReopenTx(ctx context.Context, tx pgx.Tx, ev MappingReopenedEvent) error {
	_, err := jobs.EnqueueMappingReopenedTx(ctx, d.client, tx, jobs.MappingReopenedArgs{
		EventID:    ev.EventID,
		AccountID:  ev.AccountID,
		VariantID:  ev.VariantID,
		IdentityID: ev.IdentityID,
		Reason:     string(ev.Reason),
		DedupKey:   ev.DedupKey,
	})
	return err
}
