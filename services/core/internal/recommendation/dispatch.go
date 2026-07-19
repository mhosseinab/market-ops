package recommendation

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// JobDispatcher is the River-backed ExecutionDispatcher (issue #92): it enqueues
// the durable execute_approved intent transactionally when a card commits Approved.
// It imports only the platform jobs package (never internal/execution), so the
// recommendation → jobs edge stays acyclic; the worker side wires execution.Execute
// in cmd/core/main.go. The River job row IS the durable intent/outbox record — no
// bespoke intent table or claim protocol is invented (§4.6: prefer River's durable
// job store for claim/retry).
type JobDispatcher struct{ client *jobs.Client }

// NewJobDispatcher wires the dispatcher over the platform River client.
func NewJobDispatcher(client *jobs.Client) *JobDispatcher { return &JobDispatcher{client: client} }

var _ ExecutionDispatcher = (*JobDispatcher)(nil)

// DispatchApprovedTx enqueues the durable execution intent on the caller's confirm
// transaction, carrying the approval-control identity (action/parameter/context
// version) so the worker and audit trail can reconstruct the control from the job
// alone (AUD-001). The intent is unique by card id, so a duplicate enqueue collapses
// to one durable record.
func (d *JobDispatcher) DispatchApprovedTx(ctx context.Context, tx pgx.Tx, card db.ApprovalCard) error {
	_, err := jobs.EnqueueExecuteApprovedTx(ctx, d.client, tx, jobs.ExecuteApprovedArgs{
		CardID:           card.ID,
		ActionID:         card.ActionID,
		ParameterVersion: card.ParameterVersion,
		ContextVersion:   card.ContextVersion,
	})
	return err
}
