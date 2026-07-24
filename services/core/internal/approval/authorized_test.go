package approval

import "testing"

// TestStateHasAuthorizedCoversEveryState is the exhaustiveness guard: the sealed
// authorization predicate must classify EVERY §8.4 state explicitly. A state added
// to the machine without a decision here fails this test rather than silently
// falling into the "never authorized" bucket — which is exactly the issue #90
// blocker-2 defect class (an execution-advanced card mislabelled as never
// authorized, blocking a safe bulk resume).
func TestStateHasAuthorizedCoversEveryState(t *testing.T) {
	for _, s := range AllStates {
		if _, ok := authorizedStates[s]; !ok {
			t.Errorf("state %q has no explicit authorization decision", s)
		}
	}
	if len(authorizedStates) != len(AllStates) {
		t.Errorf("authorizedStates has %d entries for %d §8.4 states; every state gets exactly one explicit decision",
			len(authorizedStates), len(AllStates))
	}
	for s := range authorizedStates {
		if !s.Valid() {
			t.Errorf("authorizedStates decides unknown state %q", s)
		}
	}
}

// TestStateHasAuthorizedSealsTheAuthorizationOutcome pins the domain rule: once a
// card's structured control has been ACTIVATED (Approved), the authorization is
// SEALED and stays observable as such through every state the §8.4 machine can
// reach downstream of it — Revalidating, Executing, and the terminal external
// results (Accepted, Rejected, PendingReconciliation, Failed). It is NOT reachability
// from Approved: Revalidating → Invalidated → Draft is a defined path, but an
// invalidated card's authorization was VOIDED and must be recalculated from Draft,
// so those states report false.
func TestStateHasAuthorizedSealsTheAuthorizationOutcome(t *testing.T) {
	authorized := []State{
		StateApproved,
		StateRevalidating,
		StateExecuting,
		StateAccepted,
		StateRejected,
		StatePendingReconciliation,
		StateFailed,
	}
	for _, s := range authorized {
		if !StateHasAuthorized(s) {
			t.Errorf("state %q must report an already-sealed authorization", s)
		}
	}

	neverAuthorized := []State{
		StateDraft,
		StateReadyForReview,
		StateBlocked,
		StateAwaitingConfirmation,
		StateExpired,
		StateInvalidated,
	}
	for _, s := range neverAuthorized {
		if StateHasAuthorized(s) {
			t.Errorf("state %q must NOT report an authorization (its control was never activated, or was voided)", s)
		}
	}
}

// TestStateHasAuthorizedRejectsUnknownState fails closed: an unrecognised state
// never claims an authorization.
func TestStateHasAuthorizedRejectsUnknownState(t *testing.T) {
	if StateHasAuthorized(State("not-a-state")) {
		t.Fatal("an unknown state must never report an authorization (fail closed)")
	}
	if StateHasAuthorized(State("")) {
		t.Fatal("the empty state must never report an authorization (fail closed)")
	}
}

// TestStateHasPendingExecutionCoversEveryState is the exhaustiveness guard for the
// LIVE-pending-execution predicate (issue #90 fix cycle 1, M1). Every §8.4 state
// carries an explicit decision, so a state added to the machine can never silently
// fall into "pending" (which would report a truthless `executionPending`) or out of
// it without a deliberate decision.
func TestStateHasPendingExecutionCoversEveryState(t *testing.T) {
	for _, s := range AllStates {
		if _, ok := pendingExecutionStates[s]; !ok {
			t.Errorf("state %q has no explicit pending-execution decision", s)
		}
	}
	if len(pendingExecutionStates) != len(AllStates) {
		t.Errorf("pendingExecutionStates has %d entries for %d §8.4 states; every state gets exactly one explicit decision",
			len(pendingExecutionStates), len(AllStates))
	}
	for s := range pendingExecutionStates {
		if !s.Valid() {
			t.Errorf("pendingExecutionStates decides unknown state %q", s)
		}
	}
}

// TestStateHasPendingExecutionIsLiveIntentOnly pins the M1 rule: a SEALED
// authorization is not the same claim as a LIVE pending execution. Only the states
// that still carry an in-flight execution intent — Approved, Revalidating,
// Executing — are pending. Every terminal external result (Accepted, Rejected,
// PendingReconciliation, Failed) has already produced its result, so nothing is
// pending even though StateHasAuthorized is true for all of them.
func TestStateHasPendingExecutionIsLiveIntentOnly(t *testing.T) {
	pending := []State{StateApproved, StateRevalidating, StateExecuting}
	for _, s := range pending {
		if !StateHasPendingExecution(s) {
			t.Errorf("state %q must report a LIVE pending execution", s)
		}
		if !StateHasAuthorized(s) {
			t.Errorf("state %q is pending but not sealed-authorized; pending must imply authorized", s)
		}
	}

	notPending := []State{
		StateDraft,
		StateReadyForReview,
		StateBlocked,
		StateAwaitingConfirmation,
		StateExpired,
		StateInvalidated,
		StateAccepted,
		StateRejected,
		StatePendingReconciliation,
		StateFailed,
	}
	for _, s := range notPending {
		if StateHasPendingExecution(s) {
			t.Errorf("state %q must NOT report a live pending execution", s)
		}
	}

	// Pending is a STRICT subset of sealed-authorized: a state can never be pending
	// without having been authorized.
	for _, s := range AllStates {
		if StateHasPendingExecution(s) && !StateHasAuthorized(s) {
			t.Errorf("state %q reports pending execution without a sealed authorization", s)
		}
	}
}

// TestStateHasPendingExecutionRejectsUnknownState fails closed.
func TestStateHasPendingExecutionRejectsUnknownState(t *testing.T) {
	if StateHasPendingExecution(State("not-a-state")) {
		t.Fatal("an unknown state must never report a pending execution (fail closed)")
	}
	if StateHasPendingExecution(State("")) {
		t.Fatal("the empty state must never report a pending execution (fail closed)")
	}
}
