package approval

// Sealed authorization outcome (issue #90 blocker 2, PRD §8.4 / §4.6 idempotency).
//
// "Has this card's structured control already been activated?" is DOMAIN knowledge
// about the §8.4 machine, so it lives here — one source of truth — rather than being
// re-derived by each caller from an ad-hoc state comparison. The bulk-confirmation
// resume path (recommendation.authorizeBulkMember) previously asked only "is the card
// exactly Approved?", which mislabelled every member that had legitimately ADVANCED
// past Approved (Revalidating, Executing, or a terminal external result) as
// `invalidated` / `not_control_bearing` — contradicting the already_authorized
// contract and blocking a resume from retrying the members that were genuinely still
// eligible.
//
// The rule: an authorization is SEALED at the moment the control is activated
// (AwaitingConfirmation → Approved) and REMAINS observable for every state downstream
// of it. It is deliberately NOT graph reachability from Approved: Approved →
// Revalidating → Invalidated → Draft is a defined path, but an invalidated card's
// authorization has been VOIDED and the card must be recalculated from Draft, so
// Invalidated and Draft report false. Each state therefore carries an EXPLICIT
// decision below, and an exhaustiveness test fails if a future §8.4 state is added
// without one — a new state can never silently fall into the wrong bucket.

// authorizedStates is the explicit, per-state decision table. Every member of
// AllStates appears exactly once (asserted by TestStateHasAuthorizedCoversEveryState).
var authorizedStates = map[State]bool{
	// Pre-activation: no structured control has been activated yet.
	StateDraft:                false,
	StateReadyForReview:       false,
	StateAwaitingConfirmation: false,
	// Terminal without activation: a blocked card never carries a control (PRC-002),
	// and an expired one lapsed before confirmation.
	StateBlocked: false,
	StateExpired: false,
	// Voided: a bound version changed. The authorization (if one existed) no longer
	// stands and recalculation returns to Draft — never reported as authorized.
	StateInvalidated: false,

	// Activated. The control WAS activated on this card; the authorization is sealed
	// and a replay/resume must report already_authorized and NEVER re-authorize or
	// re-dispatch.
	StateApproved:     true,
	StateRevalidating: true,
	StateExecuting:    true,
	// Terminal external results: the write was attempted under this authorization.
	// A failed or rejected write is still an authorization that HAPPENED — retrying
	// it is the reconciliation-gated /actions retry path's decision (EXE-003: an
	// unknown result must reconcile first), never a second bulk authorization.
	StateAccepted:              true,
	StateRejected:              true,
	StatePendingReconciliation: true,
	StateFailed:                true,
}

// StateHasAuthorized reports whether a card in state s has ALREADY had its
// structured control activated — i.e. its authorization is sealed. An unknown state
// fails closed (false): it never claims an authorization that cannot be proven from
// the §8.4 machine.
func StateHasAuthorized(s State) bool { return authorizedStates[s] }

// LIVE pending execution (issue #90 fix cycle 1, M1).
//
// "Has this card's control been activated?" (StateHasAuthorized) and "is an
// execution still IN FLIGHT under that authorization?" are DIFFERENT questions, and
// conflating them made the bulk-confirmation outcome untruthful: a resume over
// members whose writes had already terminated (Failed, Rejected, Accepted, or
// PendingReconciliation) reported `executionPending` true, telling the operator a
// write was in flight when none was.
//
// The rule: an execution intent is PENDING from the moment the control is activated
// (Approved) until the §8.4 machine records an external RESULT. Approved,
// Revalidating and Executing are pending; every terminal external result is not —
// including PendingReconciliation, whose name refers to the RECONCILIATION owed on
// an unknown result (EXE-003), not to a still-running write. Pending is therefore a
// strict subset of sealed-authorized, and each state carries an EXPLICIT decision
// below so a future §8.4 state cannot silently fall into either bucket.

// pendingExecutionStates is the explicit, per-state decision table. Every member of
// AllStates appears exactly once (asserted by
// TestStateHasPendingExecutionCoversEveryState).
var pendingExecutionStates = map[State]bool{
	// Never activated: there is no execution intent at all.
	StateDraft:                false,
	StateReadyForReview:       false,
	StateAwaitingConfirmation: false,
	StateBlocked:              false,
	StateExpired:              false,
	// Voided: the authorization no longer stands, so no intent may be pending on it.
	StateInvalidated: false,

	// Live: the control was activated and no external result has been recorded yet.
	StateApproved:     true,
	StateRevalidating: true,
	StateExecuting:    true,

	// An external RESULT exists. The authorization remains sealed
	// (StateHasAuthorized), but nothing is in flight: a definitively Failed or
	// Rejected write is done, an Accepted one succeeded, and PendingReconciliation
	// owes a reconciliation — never a running write (EXE-003).
	StateAccepted:              false,
	StateRejected:              false,
	StatePendingReconciliation: false,
	StateFailed:                false,
}

// StateHasPendingExecution reports whether a card in state s carries a LIVE,
// still-unresolved execution authorization. An unknown state fails closed (false):
// it never claims a pending execution that cannot be proven from the §8.4 machine.
func StateHasPendingExecution(s State) bool { return pendingExecutionStates[s] }
