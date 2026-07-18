// Package audit implements the AUD-001 append-only audit trail for the action
// plane. Every state-changing operation on an action (confirmation, revalidation
// block, execution start, external result, reconciliation, recommend-only
// tracking, terminal state) appends one immutable record carrying the actor,
// surface, the APR-001 versions, a card snapshot, and the structured detail
// (confirmation event / write request+response / reconciliation).
//
// The never-cut invariant this package holds is transcript independence: a
// historical action is reproducible from these rows alone (plus the append-only
// approval_card_states and the single action_executions record) WITHOUT the chat
// conversation. There is deliberately no UPDATE or DELETE path — the trail is
// INSERT/SELECT only.
package audit

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// EventType names the state-changing operation an audit record captures. The set
// is the closed CHECK set of audit_records.event_type.
type EventType string

const (
	// EventConfirmation — the structured control was activated (Approved).
	EventConfirmation EventType = "confirmation"
	// EventRevalidationBlocked — an EXE-001 gate changed; the write was prevented.
	EventRevalidationBlocked EventType = "revalidation_blocked"
	// EventExecutionStarted — the idempotent write began (card Executing).
	EventExecutionStarted EventType = "execution_started"
	// EventExternalResult — the classified external result (EXE-003).
	EventExternalResult EventType = "external_result"
	// EventReconciliation — a post-write read-back or periodic reconciliation.
	EventReconciliation EventType = "reconciliation"
	// EventRecommendOnly — recommend-only tracking (EXE-005).
	EventRecommendOnly EventType = "recommend_only"
	// EventTerminal — the action reached a terminal state.
	EventTerminal EventType = "terminal"
	// EventLevel2Proposal — a §8.3 Level-2 reversible-config proposal was written
	// (CHAT-061/062). It is a Draft-only governance write; recording it in the one
	// AUD-001 trail keeps the proposal transcript-independently reproducible.
	EventLevel2Proposal EventType = "level2_proposal"
)

// Actor is the AUD-001 actor + surface. It is identity, never free-text
// authority: the actor is a principal id/name and role, and the surface is the
// UI/system origin (screen, chat, system) — never a chat message body.
type Actor struct {
	ID      string
	Role    string
	Surface string
}

// Event is the fully-resolved input to one append. Binding supplies the APR-001
// versions; CardSnapshot and Detail are arbitrary JSON-safe structures (the card
// state at the operation, and the operation-specific evidence).
type Event struct {
	ActionID      uuid.UUID
	CardID        uuid.UUID
	AccountID     uuid.UUID
	Type          EventType
	Actor         Actor
	Binding       approval.Binding
	CardSnapshot  any
	Detail        any
	TerminalState string
}

// Append writes one immutable audit record through q (which may be a transaction,
// so the audit append commits atomically with the state change it records). It is
// the ONLY way to add to the trail; there is no update or delete.
func Append(ctx context.Context, q *db.Queries, ev Event) (db.AuditRecord, error) {
	evJSON, err := marshalEvidenceVersions(ev.Binding.EvidenceVersions)
	if err != nil {
		return db.AuditRecord{}, err
	}
	snapshot, err := marshalJSON(ev.CardSnapshot)
	if err != nil {
		return db.AuditRecord{}, err
	}
	detail, err := marshalJSON(ev.Detail)
	if err != nil {
		return db.AuditRecord{}, err
	}
	return q.AppendAuditRecord(ctx, db.AppendAuditRecordParams{
		ActionID:             ev.ActionID,
		CardID:               optionalUUID(ev.CardID),
		MarketplaceAccountID: optionalUUID(ev.AccountID),
		EventType:            string(ev.Type),
		Actor:                ev.Actor.ID,
		ActorRole:            ev.Actor.Role,
		Surface:              ev.Actor.Surface,
		ContextVersion:       ev.Binding.ContextVersion,
		ParameterVersion:     ev.Binding.ParameterVersion,
		PolicyVersion:        ev.Binding.PolicyVersion,
		CostProfileVersion:   ev.Binding.CostProfileVersion,
		EvidenceVersions:     evJSON,
		CardSnapshot:         snapshot,
		Detail:               detail,
		TerminalState:        ev.TerminalState,
	})
}

// Reproduction is the transcript-independent AUD-001 reconstruction of an action:
// the append-only audit records for the action. It is everything a reviewer needs
// to reproduce the action's decision and result without the conversation.
type Reproduction struct {
	ActionID uuid.UUID
	Records  []db.AuditRecord
}

// Reproduce reads the complete append-only audit trail for an action. It joins
// NOTHING in the conversation tables, so deleting a conversation leaves the
// reproduction intact (CHAT-008 / AUD-001). An action with no records yields an
// empty, non-error reproduction.
func Reproduce(ctx context.Context, q *db.Queries, actionID uuid.UUID) (Reproduction, error) {
	records, err := q.ListAuditRecordsForAction(ctx, actionID)
	if err != nil {
		return Reproduction{}, err
	}
	return Reproduction{ActionID: actionID, Records: records}, nil
}

// HasTerminal reports whether the reproduction contains a terminal record — a
// completed action's audit always ends in one.
func (r Reproduction) HasTerminal() bool {
	for _, rec := range r.Records {
		if rec.EventType == string(EventTerminal) {
			return true
		}
	}
	return false
}

func optionalUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func marshalJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(v)
}

// marshalEvidenceVersions encodes the bound evidence-version map as a JSON object
// keyed by observation id (mirrors the recommendation/approval encoding).
func marshalEvidenceVersions(m map[uuid.UUID]int64) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	out := make(map[string]int64, len(m))
	for id, v := range m {
		out[id.String()] = v
	}
	return json.Marshal(out)
}
