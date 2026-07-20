package execution

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// Sentinel errors. Each is a stable value the transport maps to a precise status
// (§8: a message never carries authority).
var (
	// ErrNotApproved — Execute was called on a card that is not in Approved and has
	// no prior execution/recommend-only record. Only an Approved card executes.
	ErrNotApproved = errors.New("execution: card is not approved")
	// ErrUnreconciled — the retry endpoint refuses an action whose execution is
	// still Pending Reconciliation (EXE-003 / CHAT-074). An unknown result is never
	// retried; it must reconcile first.
	ErrUnreconciled = errors.New("execution: action is pending reconciliation; retry is blocked")
	// ErrNoExecution — there is no execution record for the action to retry.
	ErrNoExecution = errors.New("execution: no execution record for action")
	// ErrAlreadyTerminal — the action already reached a reconciled terminal result;
	// it is not retry-eligible (a new recommendation is required to act again).
	ErrAlreadyTerminal = errors.New("execution: action already reached a terminal result")
)

// CardStore is the §8.4 card-state seam the executor drives. *recommendation.Service
// satisfies it. The executor never mutates card state except through AdvanceTx, so
// every move is a FROM-guarded, append-only §8.4 transition that commits in the
// SAME transaction as its AUD-001 audit record (a state change never lands without
// its audit row).
type CardStore interface {
	GetCard(ctx context.Context, id uuid.UUID) (db.ApprovalCard, error)
	AdvanceTx(ctx context.Context, q *db.Queries, cardID uuid.UUID, from, to approval.State, reason string) (db.ApprovalCard, error)
}

// RevalidationContext is the SERVER-resolved state the executor gates against. It
// is produced by a Resolver from authoritative sources — NEVER from a
// client-echoed request body (carry-forward from S17: the current binding is
// re-resolved server-side at the Revalidating gate).
type RevalidationContext struct {
	Inputs          RevalidationInputs
	Enablement      WriteEnablement
	Actor           audit.Actor
	AccountID       uuid.UUID
	VariantID       uuid.UUID
	VariantNativeID int64
}

// Resolver re-resolves the current binding and the external gate signals for a
// card at execution time. The implementation reads the authoritative store
// (identity, current price, cost profile version, policy version, boundary,
// evidence/JIT, permission, write enablement); it must not trust any client input.
type Resolver interface {
	Resolve(ctx context.Context, card db.ApprovalCard) (RevalidationContext, error)
}

// Service is the DB-backed executor: the EXE-001 revalidation gate, the EXE-002
// idempotent write, the EXE-003 external states, the EXE-005 recommend-only
// tracking, and the OUT-001 window opening. It fails closed at every seam and is
// fully instrumented (metrics + traces + structured logs) on the never-cut
// boundaries.
type Service struct {
	pool     *pgxpool.Pool
	cards    CardStore
	writer   Writer
	resolver Resolver
	now      func() time.Time
	tel      *telemetry
	notifier FailureNotifier
}

// FailureNotifier durably enqueues a NOT-001 URGENT notification for an execution or
// safety failure (issue #110). Each enqueue runs INSIDE the transaction that commits
// the failing transition + its append-only audit rows, so the notification intent
// commits ATOMICALLY with the failure: a rollback discards BOTH. It is optional (nil
// until wired via SetNotifier) — without it the failure paths are exactly the pre-#110
// behaviour (tests). The concrete implementation is *notify.JobDispatcher; this
// consumer-defined interface keeps the execution → notify dependency out of the
// import graph (ISP, no cycle).
type FailureNotifier interface {
	// ExecutionFailureTx enqueues an execution_failure for a definitively Failed
	// external write (EXE-003), keyed by the action-execution row id.
	ExecutionFailureTx(ctx context.Context, tx pgx.Tx, account, actionID, execID uuid.UUID) error
	// SafetyFailureTx enqueues a safety_failure for a gate-blocked (invalidated) card
	// (EXE-001), keyed by the card id, carrying the bounded gate token as the reason.
	SafetyFailureTx(ctx context.Context, tx pgx.Tx, account, actionID, cardID uuid.UUID, gate string) error
}

// NewService wires the executor. A nil clock defaults to time.Now (UTC).
func NewService(pool *pgxpool.Pool, cards CardStore, writer Writer, resolver Resolver) *Service {
	return &Service{
		pool: pool, cards: cards, writer: writer, resolver: resolver,
		now: func() time.Time { return time.Now().UTC() },
		tel: newTelemetry(nil),
	}
}

// WithClock overrides the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// WithLogger overrides the structured logger (tests, and to attach request scope).
func (s *Service) WithLogger(logger *slog.Logger) *Service { s.tel = newTelemetry(logger); return s }

// SetNotifier wires the durable failure-notification producer (issue #110). It is
// called once during startup, AFTER the River client exists and BEFORE the HTTP
// server serves, so there is no concurrent access to the field. Returns s for
// chaining.
func (s *Service) SetNotifier(n FailureNotifier) *Service { s.notifier = n; return s }

// advanceWithAudit advances a card from → to and appends its AUD-001 audit record
// in ONE transaction, so a state transition NEVER commits without its audit row
// (AUD-001 never-cut). On an audit-append failure the whole transaction rolls back
// (fail closed) and the failure is surfaced as a metric + structured error log —
// never swallowed. It returns the advanced card.
func (s *Service) advanceWithAudit(ctx context.Context, card db.ApprovalCard, from, to approval.State, reason string, ev audit.Event, withinTx ...func(ctx context.Context, tx pgx.Tx) error) (db.ApprovalCard, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	advanced, err := s.cards.AdvanceTx(ctx, q, card.ID, from, to, reason)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if _, err := audit.Append(ctx, q, ev); err != nil {
		s.tel.auditWriteFailed(ctx, card.ActionID, err)
		return db.ApprovalCard{}, err // rollback via defer: state change reverts.
	}
	// Optional in-transaction hooks (issue #110): a durable notification intent is
	// enqueued here so it commits ATOMICALLY with the state change + audit — a
	// rollback from any seam discards all three. A hook failure fails the whole
	// transition closed (never a committed state with a lost/duplicated notification).
	for _, h := range withinTx {
		if err := h(ctx, tx); err != nil {
			return db.ApprovalCard{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		s.tel.auditWriteFailed(ctx, card.ActionID, err)
		return db.ApprovalCard{}, err
	}
	return advanced, nil
}

// safetyFailureHook builds the in-transaction enqueue closure for a gate-blocked
// (safety) failure (issue #110). It is passed to advanceWithAudit so the durable
// safety_failure notification commits ATOMICALLY with the Revalidating → Invalidated
// transition + its audit. A nil notifier yields a no-op hook (pre-#110 / tests). The
// reason is the bounded gate token — a technical identifier, never free text.
func (s *Service) safetyFailureHook(account, actionID, cardID uuid.UUID, gate Gate) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		if s.notifier == nil {
			return nil
		}
		return s.notifier.SafetyFailureTx(ctx, tx, account, actionID, cardID, string(gate))
	}
}

// gateBlockedExecutionHook builds the in-transaction insert of the issue #105
// gate-blocked recovery marker — a no-write action_executions row — for a card a
// crash left in Executing whose EXE-001 gate FAILED on resume. It is passed to
// advanceWithAudit so the marker commits ATOMICALLY with the Executing →
// PendingReconciliation advance + its audit: either the parked card becomes VISIBLE
// (OPS-002 queue + pending_reconciliation backlog gauge) and DRAINABLE
// (ReconcilePending), or nothing commits. The record carries wrote:false and
// gate_blocked=true, so an authoritative read-back may resolve it ONLY to Failed
// (EXE-003: never infer a success from a write that never happened). It claims the
// card's stable idempotency key with ON CONFLICT DO NOTHING, so a concurrent resume
// is idempotent (no error, the single record stands) AND any later claimAndWrite on
// that key is permanently foreclosed — the marker is never a live write claim
// (EXE-002 at-most-one-write).
func (s *Service) gateBlockedExecutionHook(card db.ApprovalCard, gate Gate) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		payload, err := json.Marshal(map[string]any{"gate_blocked": true, "gate": gate, "wrote": false})
		if err != nil {
			return err
		}
		_, err = db.New(tx).InsertGateBlockedExecution(ctx, db.InsertGateBlockedExecutionParams{
			CardID:         card.ID,
			ActionID:       card.ActionID,
			IdempotencyKey: card.IdempotencyKey,
			RequestPayload: payload,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING: a concurrent resume already recorded the single
			// marker. Idempotent — not an error.
			return nil
		}
		return err
	}
}

// Mode is the execution mode of a completed Execute call.
type Mode string

const (
	// ModeWrite — a real external write was attempted (write enabled).
	ModeWrite Mode = "write"
	// ModeRecommendOnly — writes are OFF (capability/region), so the approved
	// action is tracked for external matching (EXE-005).
	ModeRecommendOnly Mode = "recommend_only"
)

// ExecuteResult is the outcome of an Execute call.
type ExecuteResult struct {
	ActionID           uuid.UUID
	CardID             uuid.UUID
	Mode               Mode
	Blocked            bool
	FailedGate         Gate
	ExternalState      ExternalState
	RecommendOnlyState RecommendOnlyState
	DidWrite           bool
}

// Execute revalidates an Approved card server-side and, only if every EXE-001
// gate passes AND writes are enabled, performs exactly one idempotent external
// write; otherwise it records a recommend-only action (EXE-005). It is idempotent:
// a repeat call for an action that already executed replays the recorded result
// with ZERO additional external writes.
func (s *Service) Execute(ctx context.Context, cardID uuid.UUID, actor audit.Actor) (ExecuteResult, error) {
	card, err := s.cards.GetCard(ctx, cardID)
	if err != nil {
		return ExecuteResult{}, err
	}
	q := db.New(s.pool)

	// Idempotent replay: a prior write for this action returns its recorded state
	// without touching the marketplace again (EXE-002). replayWrite additionally
	// self-heals a card a crash left stranded behind its recorded external state.
	if existing, err := q.GetActionExecutionByAction(ctx, card.ActionID); err == nil {
		return s.replayWrite(ctx, card, existing, actor)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ExecuteResult{}, err
	}
	// A prior recommend-only record likewise replays.
	if ro, err := q.GetRecommendOnlyAction(ctx, card.ActionID); err == nil {
		return ExecuteResult{
			ActionID:           card.ActionID,
			CardID:             card.ID,
			Mode:               ModeRecommendOnly,
			RecommendOnlyState: RecommendOnlyState(ro.State),
		}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ExecuteResult{}, err
	}

	// Restart-safe entry (EXE-002/003): a fresh Approved card executes; a card a
	// crash left in Revalidating or Executing (with no execution record — the replay
	// above already handled the record-exists case) RESUMES the write path from
	// where it stopped. No other state is executable — everything else fails closed
	// with ErrNotApproved and no state change.
	state := approval.State(card.State)
	resumableWrite := state == approval.StateRevalidating || state == approval.StateExecuting
	if state != approval.StateApproved && !resumableWrite {
		return ExecuteResult{}, ErrNotApproved
	}

	rc, err := s.resolver.Resolve(ctx, card)
	if err != nil {
		return ExecuteResult{}, err
	}

	if rc.Enablement.CanWrite() {
		return s.executeWrite(ctx, card, rc, actor)
	}
	// Writes are OFF (recommend-only, EXE-005). Recommend-only tracking only begins
	// from a fresh Approved card; a card stranded mid-write with writes now OFF is an
	// inconsistent state we NEVER silently reroute to recommend-only — fail closed,
	// state unchanged, for explicit reconciliation.
	if state != approval.StateApproved {
		return ExecuteResult{}, ErrNotApproved
	}
	return s.recordRecommendOnly(ctx, card, rc, actor)
}

// replayWrite replays an already-recorded write execution WITHOUT any new external
// write (EXE-002 idempotent replay) and, if a crash left the card stranded behind
// its recorded external state, self-heals the card so it converges. The only
// stranding reachable: the durable claim + external write committed (execution
// record = pending_reconciliation) but the result-commit transaction that advances
// the card never ran — leaving the card in Executing while the external result is
// ambiguous. Converging the card to PendingReconciliation makes it visible to
// reconciliation (authoritative read-back), which resolves it to a terminal state;
// the ambiguous result is NEVER inferred as success/failure (EXE-003). It is
// idempotent: a card already at the recorded state is returned untouched, and a
// concurrent resume that won the transition is tolerated (ErrRejectedTransition).
func (s *Service) replayWrite(ctx context.Context, card db.ApprovalCard, existing db.ActionExecution, actor audit.Actor) (ExecuteResult, error) {
	extState := ExternalState(existing.ExternalState)
	want := cardStateFor(extState)
	// The only reachable stranding is a pending claim whose result-commit crashed:
	// the record is pending_reconciliation while the card is still Executing. A
	// terminal record always advanced its card in the SAME transaction, so it can
	// never be found behind an Executing card — restricting the heal to the pending
	// case keeps replay from skipping the terminal path's outcome-window open.
	if approval.State(card.State) == approval.StateExecuting && want == approval.StatePendingReconciliation {
		binding, err := bindingOf(card)
		if err != nil {
			return ExecuteResult{}, err
		}
		// Append the external_result audit that the crashed result-commit never wrote
		// (AUD-001: a state change carries its audit row), recording that this call
		// wrote NOTHING and the result stays pending reconciliation.
		if _, err := s.advanceWithAudit(ctx, card, approval.StateExecuting, want,
			"recovery:converge_to_"+string(extState), audit.Event{
				ActionID: card.ActionID, CardID: card.ID, AccountID: card.MarketplaceAccountID,
				Type: audit.EventExternalResult, Actor: actor, Binding: binding,
				CardSnapshot: cardSnapshot(card), Detail: map[string]any{
					"external_state": extState,
					"external_ref":   existing.ExternalRef,
					"wrote":          false,
					"recovery":       true,
				},
				TerminalState: string(extState),
			}); err != nil && !errors.Is(err, recommendation.ErrRejectedTransition) {
			return ExecuteResult{}, err
		}
	}
	return ExecuteResult{
		ActionID:      card.ActionID,
		CardID:        card.ID,
		Mode:          ModeWrite,
		ExternalState: extState,
		DidWrite:      false,
	}, nil
}

// executeWrite runs the §8.4 Approved → Revalidating → Executing → terminal write
// path with the EXE-001 gate matrix at the Revalidating boundary. Every state
// transition that carries an AUD-001 event commits atomically with its audit row.
func (s *Service) executeWrite(ctx context.Context, card db.ApprovalCard, rc RevalidationContext, actor audit.Actor) (ExecuteResult, error) {
	ctx, span := s.tel.startSpan(ctx, "execution.write", card)
	defer span.End()

	binding, err := bindingOf(card)
	if err != nil {
		return ExecuteResult{}, err
	}

	// Restart-safe §8.4 prelude: apply ONLY the transitions not already taken, so a
	// card a crash left in Revalidating/Executing resumes from where it stopped. No
	// step is re-applied (append-only, FROM-guarded); the idempotent claim below
	// guarantees AT MOST ONE external write no matter how often this resumes.
	//
	// entryState is the state on ENTRY. A non-Approved entry means a crash stranded
	// this card mid-write and this call RESUMES it; recovery is never silent (§4.6 /
	// CLAUDE.md): emit a traced metric + log now, and stamp recovery:true onto the
	// append-only audit below.
	entryState := approval.State(card.State)
	recovered := entryState != approval.StateApproved
	if recovered {
		s.tel.recovered(ctx, card, entryState)
	}
	state := entryState

	// Approved → Revalidating (its append-only §8.4 history row is written in the
	// same transaction by AdvanceTx; the semantic audit events land below).
	if state == approval.StateApproved {
		if _, err := s.advance(ctx, card, approval.StateApproved, approval.StateRevalidating, "revalidation started"); err != nil {
			return ExecuteResult{}, err
		}
		state = approval.StateRevalidating
	}

	// Gate + Revalidating → Executing. Re-validation runs for a fresh Approved card
	// AND on resume from Revalidating; a gate block fails closed via the legal §8.4
	// Revalidating → Invalidated edge (no write occurred).
	if state == approval.StateRevalidating {
		gate := EvaluateGates(rc.Inputs)
		if !gate.OK {
			s.tel.gateBlocked(ctx, card, gate.Failed, ModeWrite)
			if _, err := s.advanceWithAudit(ctx, card, approval.StateRevalidating, approval.StateInvalidated,
				"gate_blocked:"+string(gate.Failed), audit.Event{
					ActionID: card.ActionID, CardID: card.ID, AccountID: rc.AccountID,
					Type: audit.EventRevalidationBlocked, Actor: actor, Binding: binding,
					CardSnapshot: cardSnapshot(card), Detail: recoveryDetail(map[string]any{"gate": gate.Failed, "reason": gate.Reason}, recovered, entryState),
				}, s.safetyFailureHook(rc.AccountID, card.ActionID, card.ID, gate.Failed)); err != nil {
				return ExecuteResult{}, err
			}
			return ExecuteResult{ActionID: card.ActionID, CardID: card.ID, Mode: ModeWrite, Blocked: true, FailedGate: gate.Failed}, nil
		}

		// Revalidating → Executing + execution_started audit (atomic).
		if _, err := s.advanceWithAudit(ctx, card, approval.StateRevalidating, approval.StateExecuting,
			"revalidated; executing", audit.Event{
				ActionID: card.ActionID, CardID: card.ID, AccountID: rc.AccountID,
				Type: audit.EventExecutionStarted, Actor: actor, Binding: binding,
				CardSnapshot: cardSnapshot(card), Detail: recoveryDetail(nil, recovered, entryState),
			}); err != nil {
			return ExecuteResult{}, err
		}
	}

	// EXE-001 gate on RESUME-FROM-EXECUTING (issue #105, never-cut §4.6). A card a
	// crash stranded in Executing with NO execution record (the record-exists case is
	// self-healed by replayWrite) has NOT written externally yet: the durable claim is
	// taken BEFORE the write, so the absence of a record proves no marketplace write
	// happened. It MUST re-validate before the fresh write — resuming straight into
	// claimAndWrite would perform a live write even if the control EXPIRED, the actor's
	// permission was revoked, the cited evidence went stale, or the live price moved,
	// i.e. "loss of approval-control versioning across a retry". On a gate failure fail
	// closed to the legal §8.4 Executing → PendingReconciliation edge (there is NO
	// Executing → Invalidated edge; §8.4 table) and write NOTHING.
	//
	// The park must be DRAINABLE and VISIBLE, not a zombie: in the SAME transaction
	// as the Executing → PendingReconciliation advance we insert a gate-blocked,
	// no-write action_executions marker (gateBlockedExecutionHook). Every drain path
	// keys off that row — ReconcilePending (GetActionExecutionByAction), the OPS-002
	// operations queue (ListPendingReconciliationByAccount), and the
	// pending_reconciliation backlog gauge (AggregatePendingReconciliation) — so the
	// parked card is now enumerable and resolvable. The marker records wrote:false and
	// gate_blocked=true, so reconciliation may resolve it ONLY to Failed via
	// authoritative read-back (EXE-003: never infer a success from a write that never
	// happened). It claims the card's stable idempotency key (ON CONFLICT DO NOTHING),
	// so it is idempotent under concurrent resumes AND permanently forecloses any later
	// claimAndWrite on that key — the marker can never be consumed as a green light for
	// an external write (EXE-002 at-most-one-write).
	if entryState == approval.StateExecuting {
		gate := EvaluateGates(rc.Inputs)
		if !gate.OK {
			s.tel.gateBlocked(ctx, card, gate.Failed, ModeWrite)
			// Tolerate ErrRejectedTransition for symmetry with replayWrite /
			// commitWriteResult: two concurrent resumes both fail the gate and race on
			// the FROM-guarded Executing → PendingReconciliation advance; the loser's
			// whole transaction (marker + audit + advance) rolls back and it returns the
			// same clean Blocked result — no write occurs on either path, and the single
			// winner's marker stands.
			if _, err := s.advanceWithAudit(ctx, card, approval.StateExecuting, approval.StatePendingReconciliation,
				"gate_blocked_on_recovery:"+string(gate.Failed), audit.Event{
					ActionID: card.ActionID, CardID: card.ID, AccountID: rc.AccountID,
					Type: audit.EventRevalidationBlocked, Actor: actor, Binding: binding,
					CardSnapshot: cardSnapshot(card),
					Detail: recoveryDetail(map[string]any{
						"gate": gate.Failed, "reason": gate.Reason, "wrote": false,
					}, true, entryState),
					TerminalState: string(approval.StatePendingReconciliation),
				}, s.gateBlockedExecutionHook(card, gate.Failed), s.safetyFailureHook(rc.AccountID, card.ActionID, card.ID, gate.Failed)); err != nil && !errors.Is(err, recommendation.ErrRejectedTransition) {
				return ExecuteResult{}, err
			}
			return ExecuteResult{
				ActionID: card.ActionID, CardID: card.ID, Mode: ModeWrite,
				Blocked: true, FailedGate: gate.Failed, ExternalState: StatePendingReconciliation,
			}, nil
		}
	}

	req := WriteRequest{
		IdempotencyKey:  card.IdempotencyKey,
		VariantNativeID: rc.VariantNativeID,
		PriceMantissa:   card.PriceMantissa,
		PriceCurrency:   card.PriceCurrency,
		PriceExponent:   int8(card.PriceExponent),
	}
	// Claim the idempotency key and perform AT MOST ONE external write (EXE-002).
	claimed, result, wrote, err := s.claimAndWrite(ctx, card, req)
	if err != nil {
		return ExecuteResult{}, err
	}
	var extState ExternalState
	if wrote {
		extState = Classify(result)
		s.tel.wroteExternal(ctx, card, extState)
	} else {
		// A duplicate claim (concurrent request): this call wrote nothing. Adopt the
		// existing record's state so the card converges without a second write.
		extState = ExternalState(claimed.ExternalState)
	}

	// Record the classified result, advance Executing → terminal/pending, append the
	// external_result (and terminal) audit, and open the OUT-001 window — ALL in ONE
	// transaction, so an external mutation never lands without its audit + state. A
	// resumed write stamps recovery:true onto that append-only audit (crash recovery
	// is reproducible from immutable rows, never only from telemetry).
	if err := s.commitWriteResult(ctx, card, rc, actor, req, claimed, result, extState, wrote, recovered, entryState); err != nil {
		return ExecuteResult{}, err
	}

	return ExecuteResult{
		ActionID: card.ActionID, CardID: card.ID, Mode: ModeWrite,
		ExternalState: extState, DidWrite: wrote,
	}, nil
}

// claimAndWrite claims the stable idempotency key (EXE-002) and, only when THIS
// call wins the claim, performs EXACTLY ONE external write. A duplicate request
// (ON CONFLICT DO NOTHING returns no row) finds the existing record and writes
// nothing external — proven by the concurrent duplicate-request suite. The claim
// is durable BEFORE the write, so a crash mid-write leaves a pending record for
// reconciliation, never a silent success.
func (s *Service) claimAndWrite(ctx context.Context, card db.ApprovalCard, req WriteRequest) (db.ActionExecution, WriteResult, bool, error) {
	q := db.New(s.pool)
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return db.ActionExecution{}, WriteResult{}, false, err
	}
	claimed, err := q.ClaimActionExecution(ctx, db.ClaimActionExecutionParams{
		CardID:         card.ID,
		ActionID:       card.ActionID,
		IdempotencyKey: req.IdempotencyKey,
		Mode:           "write",
		ExternalState:  string(StatePendingReconciliation),
		RequestPayload: reqJSON,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.tel.dedupHit(ctx, req.IdempotencyKey)
		existing, err := q.GetActionExecutionByKey(ctx, req.IdempotencyKey)
		return existing, WriteResult{}, false, err
	}
	if err != nil {
		return db.ActionExecution{}, WriteResult{}, false, err
	}
	// We own the claim: write EXACTLY ONCE.
	result := s.writer.WritePrice(ctx, req)
	return claimed, result, true, nil
}

// commitWriteResult records the classified external result, advances the card to
// its terminal/pending §8.4 state, appends the external_result and (when terminal)
// the terminal audit records, and opens the OUT-001 window — all in ONE
// transaction. On any failure the whole transaction rolls back (fail closed): the
// execution record stays pending and the card stays Executing, so reconciliation
// resolves it; no partial, audit-less state is ever committed.
func (s *Service) commitWriteResult(ctx context.Context, card db.ApprovalCard, rc RevalidationContext, actor audit.Actor, req WriteRequest, claimed db.ActionExecution, result WriteResult, extState ExternalState, wrote, recovered bool, entryState approval.State) error {
	binding, err := bindingOf(card)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	if wrote {
		respJSON, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if _, err := q.RecordExecutionResult(ctx, db.RecordExecutionResultParams{
			ID:              claimed.ID,
			ExternalState:   string(extState),
			ExternalRef:     result.ExternalRef,
			ResponsePayload: respJSON,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}

	if _, err := s.cards.AdvanceTx(ctx, q, card.ID, approval.StateExecuting, cardStateFor(extState), "external_result:"+string(extState)); err != nil {
		// A concurrent duplicate that already terminalised the card matches no row.
		if errors.Is(err, recommendation.ErrRejectedTransition) {
			return tx.Commit(ctx)
		}
		return err
	}
	// The external_result detail carries the REDACTED write request AND response
	// into the append-only trail (issue #104 / AUD-001): the write request/response
	// must be reproducible from immutable rows, never only from the mutable
	// action_executions projection. Redaction preserves the money triple, the
	// idempotency key, the outcome, and the marketplace handle, and drops raw
	// marketplace free text.
	if _, err := audit.Append(ctx, q, audit.Event{
		ActionID: card.ActionID, CardID: card.ID, AccountID: rc.AccountID,
		Type: audit.EventExternalResult, Actor: actor, Binding: binding,
		CardSnapshot: cardSnapshot(card), Detail: recoveryDetail(map[string]any{
			"external_state": extState,
			"external_ref":   result.ExternalRef,
			"wrote":          wrote,
			"request":        redactedRequest(req),
			"response":       redactedResponse(result, extState),
		}, recovered, entryState),
		TerminalState: string(extState),
	}); err != nil {
		s.tel.auditWriteFailed(ctx, card.ActionID, err)
		return err
	}

	if extState.Terminal() {
		opened := s.now()
		if _, err := q.OpenOutcomeWindow(ctx, db.OpenOutcomeWindowParams{
			ActionID: card.ActionID, CardID: pgUUID(card.ID),
			OpenedAt: opened, ClosesAt: opened.Add(outcomeWindow),
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := audit.Append(ctx, q, audit.Event{
			ActionID: card.ActionID, CardID: card.ID, AccountID: rc.AccountID,
			Type: audit.EventTerminal, Actor: actor, Binding: binding,
			CardSnapshot: cardSnapshot(card), TerminalState: string(extState),
		}); err != nil {
			s.tel.auditWriteFailed(ctx, card.ActionID, err)
			return err
		}
	}

	// Enqueue the NOT-001 URGENT execution_failure notification ATOMICALLY with the
	// terminal Failed state + its audit (issue #110). Only a definitively Failed
	// write notifies: an Accepted/Rejected result or an unknown PendingReconciliation
	// (which reconciliation later resolves) does not. Keyed by the execution row id,
	// so a concurrent duplicate write that adopts the same record collapses at the
	// store. A nil notifier is a no-op (pre-#110 / tests).
	if extState == StateFailed && s.notifier != nil {
		if err := s.notifier.ExecutionFailureTx(ctx, tx, rc.AccountID, card.ActionID, claimed.ID); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		s.tel.auditWriteFailed(ctx, card.ActionID, err)
		return err
	}
	if extState.Terminal() {
		s.tel.terminal(ctx, extState)
	}
	return nil
}

// recordRecommendOnly records an approved action in recommend-only mode (EXE-005)
// when writes are OFF. A gate block invalidates the card (Approved → Revalidating
// → Invalidated, aligned with the write path); otherwise the card stays Approved
// and a recommend_only_actions row (+ its audit) tracks the 24h external-match
// window ATOMICALLY. This is the default-off path: with no verified write
// capability, NOTHING is written to the marketplace.
func (s *Service) recordRecommendOnly(ctx context.Context, card db.ApprovalCard, rc RevalidationContext, actor audit.Actor) (ExecuteResult, error) {
	ctx, span := s.tel.startSpan(ctx, "execution.recommend_only", card)
	defer span.End()
	s.tel.recommendOnlyFallback(ctx, card)

	binding, err := bindingOf(card)
	if err != nil {
		return ExecuteResult{}, err
	}

	gate := EvaluateGates(rc.Inputs)
	if !gate.OK {
		s.tel.gateBlocked(ctx, card, gate.Failed, ModeRecommendOnly)
		// Align state-consistency with the write path: a stale binding invalidates
		// the card via Revalidating → Invalidated rather than leaving it Approved.
		if _, err := s.advance(ctx, card, approval.StateApproved, approval.StateRevalidating, "revalidation started (recommend-only)"); err != nil {
			return ExecuteResult{}, err
		}
		if _, err := s.advanceWithAudit(ctx, card, approval.StateRevalidating, approval.StateInvalidated,
			"gate_blocked:"+string(gate.Failed), audit.Event{
				ActionID: card.ActionID, CardID: card.ID, AccountID: rc.AccountID,
				Type: audit.EventRevalidationBlocked, Actor: actor, Binding: binding,
				CardSnapshot: cardSnapshot(card), Detail: map[string]any{"gate": gate.Failed, "reason": gate.Reason, "mode": ModeRecommendOnly},
			}, s.safetyFailureHook(rc.AccountID, card.ActionID, card.ID, gate.Failed)); err != nil {
			return ExecuteResult{}, err
		}
		return ExecuteResult{ActionID: card.ActionID, CardID: card.ID, Mode: ModeRecommendOnly, Blocked: true, FailedGate: gate.Failed}, nil
	}

	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecuteResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	if _, err := q.InsertRecommendOnlyAction(ctx, db.InsertRecommendOnlyActionParams{
		CardID:                card.ID,
		ActionID:              card.ActionID,
		MarketplaceAccountID:  card.MarketplaceAccountID,
		VariantID:             rc.VariantID,
		ApprovedPriceMantissa: card.PriceMantissa,
		ApprovedPriceCurrency: card.PriceCurrency,
		ApprovedPriceExponent: card.PriceExponent,
		ApprovedAt:            now,
		WindowExpiresAt:       now.Add(matchWindow),
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ExecuteResult{}, err
	}
	if _, err := audit.Append(ctx, q, audit.Event{
		ActionID: card.ActionID, CardID: card.ID, AccountID: rc.AccountID,
		Type: audit.EventRecommendOnly, Actor: actor, Binding: binding,
		CardSnapshot: cardSnapshot(card), Detail: map[string]any{"state": StateAwaitingExternalExecution, "window_expires_at": now.Add(matchWindow)},
	}); err != nil {
		s.tel.auditWriteFailed(ctx, card.ActionID, err)
		return ExecuteResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecuteResult{}, err
	}
	return ExecuteResult{
		ActionID: card.ActionID, CardID: card.ID, Mode: ModeRecommendOnly,
		RecommendOnlyState: StateAwaitingExternalExecution,
	}, nil
}

// RetryResult reports whether a failed action is retry-eligible. A retry never
// performs an inverse or duplicate write itself (EXE-004): an eligible failure is
// re-attempted only through a fresh approved action.
type RetryResult struct {
	ActionID uuid.UUID
	Eligible bool
	State    ExternalState
}

// Retry gates a retry request (EXE-003 / CHAT-074). It REJECTS an action whose
// execution is still Pending Reconciliation — an unknown result must reconcile
// first, never be retried. A definitively Failed action is retry-eligible (§16
// "retry only eligible reconciled failures"); an Accepted/Rejected action is
// terminal and not retried.
func (s *Service) Retry(ctx context.Context, actionID uuid.UUID, _ audit.Actor) (RetryResult, error) {
	q := db.New(s.pool)
	exec, err := q.GetActionExecutionByAction(ctx, actionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RetryResult{}, ErrNoExecution
	}
	if err != nil {
		return RetryResult{}, err
	}
	switch ExternalState(exec.ExternalState) {
	case StatePendingReconciliation:
		return RetryResult{}, ErrUnreconciled
	case StateFailed:
		return RetryResult{ActionID: actionID, Eligible: true, State: StateFailed}, nil
	default:
		return RetryResult{}, ErrAlreadyTerminal
	}
}

// GetExecution returns the single execution record for an action (CHAT-073 read).
func (s *Service) GetExecution(ctx context.Context, actionID uuid.UUID) (db.ActionExecution, error) {
	return db.New(s.pool).GetActionExecutionByAction(ctx, actionID)
}

// GetUnifiedAction resolves an action across BOTH execution modes (issue #106): a
// write-mode action_executions record OR a recommend-only action, projected onto
// the common UnifiedAction view. It fails closed with pgx.ErrNoRows when neither
// exists — so a recommend-only action is no longer invisible (404) through the
// common action API. A write record takes precedence: the two are mutually
// exclusive per action, and a stray recommend-only row must never mask a real
// external write.
func (s *Service) GetUnifiedAction(ctx context.Context, actionID uuid.UUID) (UnifiedAction, error) {
	q := db.New(s.pool)
	if exec, err := q.GetActionExecutionByAction(ctx, actionID); err == nil {
		return unifiedFromExecution(exec), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return UnifiedAction{}, err
	}
	if ro, err := q.GetRecommendOnlyAction(ctx, actionID); err == nil {
		return unifiedFromRecommendOnly(ro), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return UnifiedAction{}, err
	}
	return UnifiedAction{}, pgx.ErrNoRows
}

// ListUnifiedByAccount projects every executed / recommend-only action for an
// account onto the common UnifiedAction view (issue #106), keyed by action id.
// Both modes appear so the account action list can group by canonical state
// without deep-link-only discovery. It is a read; it advances no state.
func (s *Service) ListUnifiedByAccount(ctx context.Context, account uuid.UUID, limit int32) ([]UnifiedAction, error) {
	if limit <= 0 {
		limit = 200
	}
	q := db.New(s.pool)
	execs, err := q.ListActionExecutionsByAccount(ctx, db.ListActionExecutionsByAccountParams{
		MarketplaceAccountID: account, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	ros, err := q.ListRecommendOnlyActionsByAccount(ctx, db.ListRecommendOnlyActionsByAccountParams{
		MarketplaceAccountID: account, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]UnifiedAction, 0, len(execs)+len(ros))
	for _, e := range execs {
		out = append(out, unifiedFromExecution(e))
	}
	for _, r := range ros {
		out = append(out, unifiedFromRecommendOnly(r))
	}
	return out, nil
}

// ListPendingReconciliation returns the account's action_executions still
// awaiting reconciliation (PD-3 item 8, S37 Operations queue) — an unknown
// external result that must resolve before any retry (EXE-003, never inferred).
func (s *Service) ListPendingReconciliation(ctx context.Context, account uuid.UUID, limit int32) ([]db.ActionExecution, error) {
	if limit <= 0 {
		limit = 200
	}
	return db.New(s.pool).ListPendingReconciliationByAccount(ctx, db.ListPendingReconciliationByAccountParams{
		MarketplaceAccountID: account,
		Limit:                limit,
	})
}

// outcomeWindow is the OUT-001 seven-day span.
const outcomeWindow = 7 * 24 * time.Hour

// advance performs a §8.4 move whose only audit is its append-only
// approval_card_states row (written atomically by AdvanceTx). It is used for the
// internal Approved → Revalidating prelude; every transition carrying a semantic
// AUD-001 event uses advanceWithAudit instead.
func (s *Service) advance(ctx context.Context, card db.ApprovalCard, from, to approval.State, reason string) (db.ApprovalCard, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	advanced, err := s.cards.AdvanceTx(ctx, db.New(tx), card.ID, from, to, reason)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.ApprovalCard{}, err
	}
	return advanced, nil
}

// recoveryDetail stamps the crash-recovery marker onto an AUD-001 audit detail when
// the write path RESUMED a crash-stranded card (issue #105). A non-recovery call
// returns the base detail untouched (nil stays nil), so the append-only trail carries
// recovery:true + recovered_from ONLY on a genuine resume — a recovery is then
// reproducible from immutable rows alone, never only from telemetry (§4.6: no silent
// recovery). It never mutates the caller's map.
func recoveryDetail(base map[string]any, recovered bool, from approval.State) map[string]any {
	if !recovered {
		return base
	}
	out := make(map[string]any, len(base)+2)
	for k, v := range base {
		out[k] = v
	}
	out["recovery"] = true
	out["recovered_from"] = string(from)
	return out
}

// cardStateFor maps an external state onto the §8.4 terminal (or pending) card
// state.
func cardStateFor(s ExternalState) approval.State {
	switch s {
	case StateAccepted:
		return approval.StateAccepted
	case StateRejected:
		return approval.StateRejected
	case StateFailed:
		return approval.StateFailed
	default:
		return approval.StatePendingReconciliation
	}
}

// bindingOf builds the APR-001 binding for an audit event from a card. It parses
// the card's bound evidence-version map so EVERY audit record carries the exact
// cited-evidence versions that were approved (issue #104 / AUD-001): before this,
// evidence_versions serialized as an empty {} on every execution/terminal record.
// A malformed evidence payload is a corrupt card state and is returned as an error
// (fail closed) — never silently defaulted to an empty binding.
func bindingOf(card db.ApprovalCard) (approval.Binding, error) {
	evidence, err := parseEvidenceVersions(card.EvidenceVersions)
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

// cardSnapshot is the COMPLETE immutable AUD-001 card/recommendation snapshot: the
// exact recommendation identity, every APR-001 version (parameter/context/policy/
// cost-profile), the bound evidence versions, expiry, and the money triple. It is
// what was shown and approved, reproducible from the append-only trail alone —
// never the current values (issue #104). The price stays the integer triple
// (mantissa/currency/exponent); no float ever enters the money path.
func cardSnapshot(card db.ApprovalCard) map[string]any {
	return map[string]any{
		"id":                   card.ID,
		"recommendation_id":    card.RecommendationID,
		"lineage_id":           card.LineageID,
		"account_id":           card.MarketplaceAccountID,
		"version":              card.Version,
		"state":                card.State,
		"action_id":            card.ActionID,
		"parameter_version":    card.ParameterVersion,
		"context_version":      card.ContextVersion,
		"policy_version":       card.PolicyVersion,
		"cost_profile_version": card.CostProfileVersion,
		"evidence_versions":    rawEvidenceJSON(card.EvidenceVersions),
		"idempotency_key":      card.IdempotencyKey,
		"price_mantissa":       card.PriceMantissa,
		"price_currency":       card.PriceCurrency,
		"price_exponent":       card.PriceExponent,
		"expires_at":           card.ExpiresAt,
		"created_at":           card.CreatedAt,
	}
}

// rawEvidenceJSON embeds the card's bound evidence-version JSON object verbatim in
// the snapshot so an evidence add/remove/version-bump is byte-distinguishable in
// the replayed record. An empty payload normalises to an empty object.
func rawEvidenceJSON(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(raw)
}

// redactedRequest projects a write request onto the fields the audit trail needs to
// reproduce WHAT was written — the money triple, the target variant, and the
// idempotency key that gates EXE-002. It carries NO auth secret: the token lives on
// the transport, never in the request payload. The money stays an integer triple.
func redactedRequest(req WriteRequest) map[string]any {
	return map[string]any{
		"idempotency_key":   req.IdempotencyKey,
		"variant_native_id": req.VariantNativeID,
		"price_mantissa":    req.PriceMantissa,
		"price_currency":    req.PriceCurrency,
		"price_exponent":    req.PriceExponent,
	}
}

// redactedResponse projects a write result onto the fields the audit trail needs to
// reproduce the OUTCOME — the raw outcome, the marketplace handle, and the
// classified external state — while dropping WriteResult.Detail, which is a raw,
// non-authoritative marketplace note that must never persist (structured-logs +
// free-text-containment invariants: no PII / no raw marketplace free text).
func redactedResponse(result WriteResult, extState ExternalState) map[string]any {
	return map[string]any{
		"outcome":        result.Outcome,
		"external_ref":   result.ExternalRef,
		"external_state": extState,
	}
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
