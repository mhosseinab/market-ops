package notify

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// JobDispatcher is the River-backed notification producer (issue #110, NOT-001): it
// enqueues one durable notification_deliver intent transactionally on the caller's
// transaction, so the intent commits ATOMICALLY with the owning lifecycle transition
// (a market event opening, an execution failure, a safety/gate failure). The River
// job row IS the durable intent/outbox record — no bespoke outbox table is invented.
// Each producing package (event, execution) depends only on a small consumer-defined
// interface that *JobDispatcher structurally satisfies, so this notify → jobs edge
// stays the only new import (no event/execution → notify dependency, no cycle).
//
// The delivery itself runs later, in the NotificationDeliverWorker, through the
// idempotent Store.Deliver — so this dispatcher NEVER performs authoritative
// calculation or renders copy; it only derives the server-side event identity,
// closed catalog binding, and dedup key from the transition.
type JobDispatcher struct{ client *jobs.Client }

// NewJobDispatcher wires the dispatcher over the platform River client.
func NewJobDispatcher(client *jobs.Client) *JobDispatcher { return &JobDispatcher{client: client} }

// MarketEventTx enqueues a market_event notification for a freshly-OPENED market
// event (NOT-001: the notification shares the market event's product id, and the key
// batches into the daily digest). Idempotent by the event id, so a producer replay
// that re-opens nothing enqueues nothing new and a duplicate job collapses at the
// store.
func (d *JobDispatcher) MarketEventTx(ctx context.Context, tx pgx.Tx, account, eventID, variant uuid.UUID) error {
	_, err := jobs.EnqueueNotificationDeliverTx(ctx, d.client, tx, buildMarketEventArgs(account, eventID, variant))
	return err
}

// ExecutionFailureTx enqueues an urgent execution_failure notification for a
// definitively Failed external write (EXE-003). It bypasses the digest (delivered
// immediately) and is keyed by the action-execution row id (one urgent notice per
// failed attempt).
func (d *JobDispatcher) ExecutionFailureTx(ctx context.Context, tx pgx.Tx, account, actionID, execID uuid.UUID) error {
	_, err := jobs.EnqueueNotificationDeliverTx(ctx, d.client, tx, buildExecutionFailureArgs(account, actionID, execID))
	return err
}

// SafetyFailureTx enqueues an urgent safety_failure notification for a card that a
// revalidation gate blocked (EXE-001), keyed by the card id (one urgent notice per
// invalidated card). The reason is a bounded gate token (a technical identifier,
// never free text or localized copy).
func (d *JobDispatcher) SafetyFailureTx(ctx context.Context, tx pgx.Tx, account, actionID, cardID uuid.UUID, gate string) error {
	_, err := jobs.EnqueueNotificationDeliverTx(ctx, d.client, tx, buildSafetyFailureArgs(account, actionID, cardID, gate))
	return err
}

// buildMarketEventArgs derives the durable market_event delivery intent from the
// opened event. Title and body use the single deliverable market_event key with its
// {variant} slot; severity is info (the digest batches it). The dedup key is the
// server-derived event identity, so a replay collapses and a distinct event does not.
func buildMarketEventArgs(account, eventID, variant uuid.UUID) jobs.NotificationDeliverArgs {
	return jobs.NotificationDeliverArgs{
		Account:  account,
		EventID:  eventID,
		DedupKey: string(CategoryMarketEvent) + ":" + eventID.String(),
		Category: string(CategoryMarketEvent),
		Severity: "info",
		TitleKey: KeyItemMarketEvent,
		BodyKey:  KeyItemMarketEvent,
		Params:   map[string]string{"variant": variant.String()},
	}
}

// buildExecutionFailureArgs derives the durable execution_failure delivery intent.
// The shared product id is the action id; severity is critical (bypasses the digest,
// delivered immediately). Keyed by the execution row id.
func buildExecutionFailureArgs(account, actionID, execID uuid.UUID) jobs.NotificationDeliverArgs {
	return jobs.NotificationDeliverArgs{
		Account:  account,
		EventID:  actionID,
		DedupKey: string(CategoryExecutionFailure) + ":" + execID.String(),
		Category: string(CategoryExecutionFailure),
		Severity: "critical",
		TitleKey: KeyItemExecutionFail,
		BodyKey:  KeyItemExecutionFail,
		Params:   map[string]string{"action": actionID.String()},
	}
}

// buildSafetyFailureArgs derives the durable safety_failure delivery intent. The
// shared product id is the action id; severity is critical (bypasses the digest).
// Keyed by the card id; the {reason} slot is the bounded gate token.
func buildSafetyFailureArgs(account, actionID, cardID uuid.UUID, gate string) jobs.NotificationDeliverArgs {
	return jobs.NotificationDeliverArgs{
		Account:  account,
		EventID:  actionID,
		DedupKey: string(CategorySafetyFailure) + ":" + cardID.String(),
		Category: string(CategorySafetyFailure),
		Severity: "critical",
		TitleKey: KeyItemSafetyFail,
		BodyKey:  KeyItemSafetyFail,
		Params:   map[string]string{"reason": gate},
	}
}
