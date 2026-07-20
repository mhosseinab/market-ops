package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

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
// immediately) and is keyed by the action-execution row identity AND payload (one
// urgent notice per failed attempt; a distinct payload never collapses).
func (d *JobDispatcher) ExecutionFailureTx(ctx context.Context, tx pgx.Tx, account, actionID, execID uuid.UUID) error {
	_, err := jobs.EnqueueNotificationDeliverTx(ctx, d.client, tx, buildExecutionFailureArgs(account, actionID, execID))
	return err
}

// SafetyFailureTx enqueues an urgent safety_failure notification for a card that a
// revalidation gate blocked (EXE-001), keyed by the card identity AND payload — so the
// SAME card blocked by a DIFFERENT gate is a distinct notice, never silently dropped.
// The reason is a bounded gate token (a technical identifier, never free text or
// localized copy) and is bound into the idempotency key.
func (d *JobDispatcher) SafetyFailureTx(ctx context.Context, tx pgx.Tx, account, actionID, cardID uuid.UUID, gate string) error {
	_, err := jobs.EnqueueNotificationDeliverTx(ctx, d.client, tx, buildSafetyFailureArgs(account, actionID, cardID, gate))
	return err
}

// deriveDedupKey is the single source of the NOT-001 idempotency key (issue #123). It
// is a STABLE, deterministic function of the notification's event IDENTITY and its
// PAYLOAD: the category, the server-derived identity, the closed catalog keys, and the
// named slots. So an exact replay of the same transition re-derives the SAME key (the
// store's (account, dedup_key) uniqueness collapses it, no duplicate product event),
// while a notification that shares an identity but differs in payload — e.g. the same
// card blocked by a different revalidation gate — derives a DISTINCT key and is NOT
// silently swallowed. Params are sorted so map iteration order can never make the key
// unstable (an unstable key would defeat idempotency), and every field is
// length-prefixed so no two distinct field sequences can canonicalize to one digest.
// The readable "category:identity:" prefix keeps the logged key diagnosable; the
// payload digest is the disambiguator. Identity/payload are technical identifiers, not
// PII or locale copy (LOC-001) — the {reason} slot is a bounded gate token.
func deriveDedupKey(category, identity, titleKey, bodyKey string, params map[string]string) string {
	h := sha256.New()
	// sha256's Write never errors; the blank assignment satisfies errcheck.
	writeField := func(s string) { _, _ = fmt.Fprintf(h, "%d:%s|", len(s), s) }
	writeField(category)
	writeField(identity)
	writeField(titleKey)
	writeField(bodyKey)
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeField(k)
		writeField(params[k])
	}
	return category + ":" + identity + ":" + hex.EncodeToString(h.Sum(nil))
}

// buildMarketEventArgs derives the durable market_event delivery intent from the
// opened event. Title and body use the single deliverable market_event key with its
// {variant} slot; severity is info (the digest batches it). The dedup key binds the
// server-derived event identity AND payload, so a replay collapses and a distinct
// event or payload does not.
func buildMarketEventArgs(account, eventID, variant uuid.UUID) jobs.NotificationDeliverArgs {
	params := map[string]string{"variant": variant.String()}
	return jobs.NotificationDeliverArgs{
		Account:  account,
		EventID:  eventID,
		DedupKey: deriveDedupKey(string(CategoryMarketEvent), eventID.String(), KeyItemMarketEvent, KeyItemMarketEvent, params),
		Category: string(CategoryMarketEvent),
		Severity: "info",
		TitleKey: KeyItemMarketEvent,
		BodyKey:  KeyItemMarketEvent,
		Params:   params,
	}
}

// buildExecutionFailureArgs derives the durable execution_failure delivery intent.
// The shared product id is the action id; severity is critical (bypasses the digest,
// delivered immediately). Keyed by the execution row identity AND payload.
func buildExecutionFailureArgs(account, actionID, execID uuid.UUID) jobs.NotificationDeliverArgs {
	params := map[string]string{"action": actionID.String()}
	return jobs.NotificationDeliverArgs{
		Account:  account,
		EventID:  actionID,
		DedupKey: deriveDedupKey(string(CategoryExecutionFailure), execID.String(), KeyItemExecutionFail, KeyItemExecutionFail, params),
		Category: string(CategoryExecutionFailure),
		Severity: "critical",
		TitleKey: KeyItemExecutionFail,
		BodyKey:  KeyItemExecutionFail,
		Params:   params,
	}
}

// buildSafetyFailureArgs derives the durable safety_failure delivery intent. The
// shared product id is the action id; severity is critical (bypasses the digest). Keyed
// by the card identity AND payload; the {reason} slot is the bounded gate token, so a
// distinct gate on the same card yields a distinct, non-collapsing notice.
func buildSafetyFailureArgs(account, actionID, cardID uuid.UUID, gate string) jobs.NotificationDeliverArgs {
	params := map[string]string{"reason": gate}
	return jobs.NotificationDeliverArgs{
		Account:  account,
		EventID:  actionID,
		DedupKey: deriveDedupKey(string(CategorySafetyFailure), cardID.String(), KeyItemSafetyFail, KeyItemSafetyFail, params),
		Category: string(CategorySafetyFailure),
		Severity: "critical",
		TitleKey: KeyItemSafetyFail,
		BodyKey:  KeyItemSafetyFail,
		Params:   params,
	}
}
