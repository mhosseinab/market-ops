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
