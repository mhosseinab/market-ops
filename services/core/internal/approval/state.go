// Package approval implements the §8.4 approval state machine and the APR-001
// version-bound approval control for the DK Marketplace Intelligence core.
//
// Two never-cut invariants (PRD §4.6) live here:
//
//   - Approval versioning (APR-001): an approval control binds the exact action
//     ID, parameter version, context version, evidence versions, policy version,
//     cost-profile version, and an expiry. ANY change to a bound dimension, or a
//     reached expiry, INVALIDATES the control — proven per dimension by the
//     invalidation suite, not merely reviewed.
//   - Free-text containment (§8): only a structured, version-bound control moves
//     a card past AwaitingConfirmation. Free text never approves; a simulation
//     never carries a control. A card PRICE EDIT mints a NEW parameter version
//     (CHAT-044) and the stale control is rejected — the price is never mutated
//     in place.
//
// This package is inside the money static-guard set (internal/{money,margin,
// policy,approval}, tools/semgrep/money.yml + forbidigo): there is NO float and
// NO raw arithmetic operator (+ - * / %) anywhere here, including tests. Version
// numbers are assigned by the append-only store (SQL), never incremented in Go;
// this package only compares them. Time advances via time.Time methods.
//
// Execution itself is S18. The Revalidating → Executing boundary is a stub that
// fails CLOSED (Revalidate always blocks) and carries a negative test naming S18
// as the completing step (engineering method: an explicitly-planned stub).
package approval

// State is one node of the §8.4 approval state machine. The set is closed and
// exactly the PRD diagram's states; an unknown state authorizes nothing.
type State string

const (
	// StateDraft is the entry state ([*] → Draft). A card is (re)built here; the
	// terminal WRITE of the model plane is Draft (approval is never a model tool).
	StateDraft State = "draft"
	// StateReadyForReview — deterministic validation passed (Draft → ReadyForReview).
	StateReadyForReview State = "ready_for_review"
	// StateBlocked — a data or policy blocker prevented review (Draft → Blocked).
	// Terminal: a blocked card carries NO approval control (PRC-002).
	StateBlocked State = "blocked"
	// StateAwaitingConfirmation — the card is opened and awaiting the bound control
	// (ReadyForReview → AwaitingConfirmation).
	StateAwaitingConfirmation State = "awaiting_confirmation"
	// StateApproved — the bound structured control was activated
	// (AwaitingConfirmation → Approved). Free text can never reach this state.
	StateApproved State = "approved"
	// StateExpired — the expiry was reached before confirmation
	// (AwaitingConfirmation → Expired). Terminal.
	StateExpired State = "expired"
	// StateInvalidated — a bound version changed (AwaitingConfirmation → Invalidated
	// or Revalidating → Invalidated). Recalculation returns to Draft.
	StateInvalidated State = "invalidated"
	// StateRevalidating — pre-execution revalidation (Approved → Revalidating). The
	// Revalidating → Executing boundary is stubbed closed until S18.
	StateRevalidating State = "revalidating"
	// StateExecuting — the idempotent external write is in flight
	// (Revalidating → Executing). Lands in S18.
	StateExecuting State = "executing"
	// StateAccepted — the external write was accepted (terminal).
	StateAccepted State = "accepted"
	// StateRejected — the external write was rejected (terminal).
	StateRejected State = "rejected"
	// StatePendingReconciliation — the external result is unknown; it is NEVER
	// inferred as success/failure (EXE-003). Reconciles to Accepted or Failed.
	StatePendingReconciliation State = "pending_reconciliation"
	// StateFailed — the write failed (terminal).
	StateFailed State = "failed"
)

// AllStates is the closed set of §8.4 states in a stable order (tests, exhaustive
// wiring).
var AllStates = []State{
	StateDraft,
	StateReadyForReview,
	StateBlocked,
	StateAwaitingConfirmation,
	StateApproved,
	StateExpired,
	StateInvalidated,
	StateRevalidating,
	StateExecuting,
	StateAccepted,
	StateRejected,
	StatePendingReconciliation,
	StateFailed,
}

// Valid reports whether s is a known §8.4 state.
func (s State) Valid() bool {
	return stateSet[s]
}

var stateSet = func() map[State]bool {
	m := make(map[State]bool, len(AllStates))
	for _, s := range AllStates {
		m[s] = true
	}
	return m
}()

// Transition is one directed edge of the §8.4 machine. The table below is the
// EXPLICIT, verbatim transcription of the PRD diagram; every allowed edge is a
// row and everything absent is an undefined transition that Advance rejects.
type Transition struct {
	From State
	To   State
}

// transitions is the §8.4 state diagram VERBATIM. It is the single source of
// truth for "which move is legal"; a move not listed here is undefined and fails
// closed (ErrUndefinedTransition). The state-machine test asserts this table
// equals the diagram exactly and that every unlisted (from, to) pair is rejected.
//
//	[*]                    -> Draft
//	Draft                  -> ReadyForReview
//	Draft                  -> Blocked
//	ReadyForReview         -> AwaitingConfirmation
//	AwaitingConfirmation   -> Approved
//	AwaitingConfirmation   -> Expired
//	AwaitingConfirmation   -> Invalidated
//	Approved               -> Revalidating
//	Revalidating           -> Executing
//	Revalidating           -> Invalidated
//	Executing              -> Accepted
//	Executing              -> Rejected
//	Executing              -> PendingReconciliation
//	Executing              -> Failed
//	PendingReconciliation  -> Accepted
//	PendingReconciliation  -> Failed
//	Invalidated            -> Draft
var transitions = []Transition{
	{StateDraft, StateReadyForReview},
	{StateDraft, StateBlocked},
	{StateReadyForReview, StateAwaitingConfirmation},
	{StateAwaitingConfirmation, StateApproved},
	{StateAwaitingConfirmation, StateExpired},
	{StateAwaitingConfirmation, StateInvalidated},
	{StateApproved, StateRevalidating},
	{StateRevalidating, StateExecuting},
	{StateRevalidating, StateInvalidated},
	{StateExecuting, StateAccepted},
	{StateExecuting, StateRejected},
	{StateExecuting, StatePendingReconciliation},
	{StateExecuting, StateFailed},
	{StatePendingReconciliation, StateAccepted},
	{StatePendingReconciliation, StateFailed},
	{StateInvalidated, StateDraft},
}

// transitionSet indexes the table for O(1), fail-closed lookup.
var transitionSet = func() map[Transition]bool {
	m := make(map[Transition]bool, len(transitions))
	for _, t := range transitions {
		m[t] = true
	}
	return m
}()

// Transitions returns the §8.4 edge table in declaration order (tests, wiring).
func Transitions() []Transition {
	out := make([]Transition, len(transitions))
	copy(out, transitions)
	return out
}

// CanTransition reports whether from → to is a defined §8.4 edge. Any pair not in
// the table (including a move out of a terminal state) is undefined and denied.
func CanTransition(from, to State) bool {
	return transitionSet[Transition{From: from, To: to}]
}

// Terminal reports whether s has no outgoing edge (Accepted, Rejected, Failed,
// Blocked, Expired). A terminal card is inert: no move advances it.
func (s State) Terminal() bool {
	if !s.Valid() {
		return false
	}
	for _, t := range transitions {
		if t.From == s {
			return false
		}
	}
	return true
}

// Advance validates a requested §8.4 move. It returns ErrUnknownState for an
// unrecognised endpoint and ErrUndefinedTransition for a move the diagram does
// not permit; otherwise nil. This is the ONLY sanctioned way to move a card, so
// an undefined transition can never be persisted.
func Advance(from, to State) error {
	if !from.Valid() || !to.Valid() {
		return ErrUnknownState
	}
	if !CanTransition(from, to) {
		return ErrUndefinedTransition
	}
	return nil
}
