// Package reconcile resolves the external state of a write after the fact
// (post-write read-back and periodic owned-offer reconciliation) and invalidates
// stale approval cards when the marketplace changes out of band (§16 "Manual DK
// price change → reconcile owned offer; invalidate stale cards").
//
// Two never-cut invariants meet here:
//
//   - Reconciliation (EXE-003): an action parked in Pending Reconciliation is
//     resolved to a terminal Accepted or Failed only by observed evidence, never
//     by inference. The §8.4 machine's PendingReconciliation → {Accepted, Failed}
//     edges are the only resolutions.
//   - Card invalidation (§16 / APR-001): a manual owned-offer change invalidates
//     every live card whose control could still authorize a write, so no stale
//     card writes over a change the seller already made.
package reconcile

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// ErrNotPending — reconciliation was asked to resolve an action that is not in
// Pending Reconciliation. Only a pending action reconciles.
var ErrNotPending = errors.New("reconcile: action is not pending reconciliation")

// ErrUnknownResolution — ReconcilePending was handed a resolution outside the
// CLOSED {Accepted, Failed} set (§8.4 PendingReconciliation edges). The action is
// LEFT in Pending Reconciliation (fail closed, EXE-003): an unrecognised
// resolution NEVER terminalises a pending write, and NO DB read or state change
// occurs. Before this guard an unrecognised value silently defaulted to Failed,
// destroying the pending state on inference — a never-cut violation.
var ErrUnknownResolution = errors.New("reconcile: unknown resolution; pending reconciliation preserved")

// CardStore is the §8.4 card-state seam (satisfied by *recommendation.Service).
// AdvanceTx lets the state transition commit atomically with its AUD-001 audit row.
type CardStore interface {
	GetCard(ctx context.Context, id uuid.UUID) (db.ApprovalCard, error)
	AdvanceTx(ctx context.Context, q *db.Queries, cardID uuid.UUID, from, to approval.State, reason string) (db.ApprovalCard, error)
}

// StaleCardInvalidator invalidates live cards for a variant (satisfied by
// *recommendation.Service.ExpireDependentForVariant).
type StaleCardInvalidator interface {
	ExpireDependentForVariant(ctx context.Context, variant uuid.UUID, reason string) (int, error)
}

// Service performs reconciliation and stale-card invalidation.
type Service struct {
	pool        *pgxpool.Pool
	cards       CardStore
	invalidator StaleCardInvalidator
	now         func() time.Time
}

// NewService wires the reconciler.
func NewService(pool *pgxpool.Pool, cards CardStore, invalidator StaleCardInvalidator) *Service {
	return &Service{pool: pool, cards: cards, invalidator: invalidator, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// Resolution is the reconciled terminal state for a pending action. Only Accepted
// or Failed are valid resolutions (§8.4 PendingReconciliation edges); an ambiguous
// read-back stays pending and reconciles later.
type Resolution string

const (
	ResolveAccepted Resolution = "accepted"
	ResolveFailed   Resolution = "failed"
)

// Valid reports whether r is a DECLARED resolution. The set is closed; anything
// else fails closed and must never resolve a pending action.
func (r Resolution) Valid() bool {
	switch r {
	case ResolveAccepted, ResolveFailed:
		return true
	default:
		return false
	}
}

// resolve maps a DECLARED resolution onto its terminal external+card state with
// EXHAUSTIVE handling. An undeclared resolution returns ErrUnknownResolution and
// NO state — the caller must leave the action in Pending Reconciliation (EXE-003,
// unknown input leaves state unchanged). This replaces the prior
// "default-to-Failed" logic, which inferred a terminal state from an unrecognised
// value.
func resolve(r Resolution) (execution.ExternalState, approval.State, error) {
	switch r {
	case ResolveAccepted:
		return execution.StateAccepted, approval.StateAccepted, nil
	case ResolveFailed:
		return execution.StateFailed, approval.StateFailed, nil
	default:
		return "", "", ErrUnknownResolution
	}
}

// ReconcilePending resolves a Pending Reconciliation action to a terminal state
// from observed evidence (a post-write read-back or a periodic owned-offer
// reconciliation). It advances the card PendingReconciliation → {Accepted,
// Failed}, records the reconciliation and terminal audit, and opens the OUT-001
// outcome window. It refuses any action that is not pending.
func (s *Service) ReconcilePending(ctx context.Context, actionID uuid.UUID, resolution Resolution, detail string) error {
	// Fail closed FIRST: an undeclared resolution errors before any DB read or
	// state change, so the action stays in Pending Reconciliation (EXE-003). Only a
	// declared resolution ({Accepted, Failed}) may terminalise a pending write.
	terminal, cardState, err := resolve(resolution)
	if err != nil {
		return err
	}

	q := db.New(s.pool)
	exec, err := q.GetActionExecutionByAction(ctx, actionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotPending
	}
	if err != nil {
		return err
	}
	if execution.ExternalState(exec.ExternalState) != execution.StatePendingReconciliation {
		return ErrNotPending
	}

	card, err := s.cards.GetCard(ctx, exec.CardID)
	if err != nil {
		return err
	}
	binding, err := bindingOf(card)
	if err != nil {
		return err
	}

	// Resolve the execution record, advance the card PendingReconciliation →
	// terminal, append the reconciliation + terminal audit records, and open the
	// OUT-001 window — ALL in ONE transaction. A terminal state never commits
	// without its audit row; on any failure the whole tx rolls back (fail closed)
	// and the action stays pending for the next reconciliation pass.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tq := db.New(tx)

	if _, err := tq.ReconcileActionExecution(ctx, db.ReconcileActionExecutionParams{
		ID:            exec.ID,
		ExternalState: string(terminal),
		ExternalRef:   exec.ExternalRef,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := s.cards.AdvanceTx(ctx, tq, card.ID, approval.StatePendingReconciliation, cardState, "reconciled:"+string(terminal)); err != nil {
		return err
	}
	// The reconciliation record carries the read-back / manual evidence that
	// resolved the pending write, plus the pre-reconciliation external ref, into the
	// append-only trail (issue #104 / AUD-001): the terminal state is reproducible
	// from immutable rows, not inferred and not read from the mutable
	// action_executions projection. `detail` is a bounded, structured note — never
	// raw marketplace free text.
	if _, err := audit.Append(ctx, tq, audit.Event{
		ActionID: actionID, CardID: card.ID, Type: audit.EventReconciliation,
		Binding:      binding,
		CardSnapshot: map[string]any{"id": card.ID, "action_id": card.ActionID, "state": string(cardState)},
		Detail: map[string]any{
			"resolution":           terminal,
			"detail":               detail,
			"prior_external_state": exec.ExternalState,
			"external_ref":         exec.ExternalRef,
		},
		TerminalState: string(terminal),
	}); err != nil {
		return err
	}
	opened := s.now()
	if _, err := tq.OpenOutcomeWindow(ctx, db.OpenOutcomeWindowParams{
		ActionID: actionID, CardID: pgUUID(card.ID),
		OpenedAt: opened, ClosesAt: opened.Add(7 * 24 * time.Hour),
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := audit.Append(ctx, tq, audit.Event{
		ActionID: actionID, CardID: card.ID, Type: audit.EventTerminal,
		Binding: binding, TerminalState: string(terminal),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InvalidateStaleCardsForVariant is the §16 manual-DK-change consumer: an observed
// out-of-band owned-offer change invalidates every live card for the variant so a
// stale card cannot write over the seller's own change. It returns the count
// invalidated.
func (s *Service) InvalidateStaleCardsForVariant(ctx context.Context, variant uuid.UUID, reason string) (int, error) {
	return s.invalidator.ExpireDependentForVariant(ctx, variant, "manual_dk_change:"+reason)
}

// bindingOf builds the APR-001 binding for a reconciliation audit event, parsing
// the card's bound evidence-version map so the reconciliation and terminal records
// carry the exact cited-evidence versions that were approved (issue #104 / AUD-001)
// rather than an empty {}. A malformed payload is a corrupt card state and is
// returned as an error (fail closed).
func bindingOf(card db.ApprovalCard) (approval.Binding, error) {
	evidence, err := recommendation.DecodeEvidenceVersions(card.EvidenceVersions)
	if err != nil {
		return approval.Binding{}, err
	}
	return approval.Binding{
		ActionID:           card.ActionID,
		ParameterVersion:   card.ParameterVersion,
		ContextVersion:     card.ContextVersion,
		PolicyVersion:      card.PolicyVersion,
		CostProfileVersion: card.CostProfileVersion,
		EvidenceVersions:   evidence,
		Expiry:             card.ExpiresAt,
	}, nil
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
