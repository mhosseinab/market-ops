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
