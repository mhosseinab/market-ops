package approval

import "testing"

// prdDiagram is the §8.4 state diagram transcribed INDEPENDENTLY of the package's
// own transition table, so this test proves the implementation matches the PRD —
// not merely itself. Each key lists the states the key may move to. States absent
// as keys (Accepted, Rejected, Failed, Blocked, Expired) are terminal.
var prdDiagram = map[State][]State{
	StateDraft:                 {StateReadyForReview, StateBlocked},
	StateReadyForReview:        {StateAwaitingConfirmation},
	StateAwaitingConfirmation:  {StateApproved, StateExpired, StateInvalidated},
	StateApproved:              {StateRevalidating},
	StateRevalidating:          {StateExecuting, StateInvalidated},
	StateExecuting:             {StateAccepted, StateRejected, StatePendingReconciliation, StateFailed},
	StatePendingReconciliation: {StateAccepted, StateFailed},
	StateInvalidated:           {StateDraft},
}

// expectedEdges builds the set of every allowed (from,to) edge from prdDiagram.
func expectedEdges() map[Transition]bool {
	edges := map[Transition]bool{}
	for from, tos := range prdDiagram {
		for _, to := range tos {
			edges[Transition{From: from, To: to}] = true
		}
	}
	return edges
}

// TestAdvance_CoversEveryDefinedTransition asserts every §8.4 edge (from the
// independent PRD transcription) is accepted by Advance.
func TestAdvance_CoversEveryDefinedTransition(t *testing.T) {
	for edge := range expectedEdges() {
		if err := Advance(edge.From, edge.To); err != nil {
			t.Errorf("defined §8.4 edge %s→%s rejected: %v", edge.From, edge.To, err)
		}
		if !CanTransition(edge.From, edge.To) {
			t.Errorf("CanTransition(%s,%s) = false; want true", edge.From, edge.To)
		}
	}
}

// TestAdvance_RejectsEveryUndefinedTransition iterates the full state×state
// space and asserts Advance accepts EXACTLY the PRD edges and rejects everything
// else with ErrUndefinedTransition. This is the "rejects every undefined
// transition" release-gate assertion.
func TestAdvance_RejectsEveryUndefinedTransition(t *testing.T) {
	edges := expectedEdges()
	for _, from := range AllStates {
		for _, to := range AllStates {
			err := Advance(from, to)
			want := edges[Transition{From: from, To: to}]
			switch {
			case want && err != nil:
				t.Errorf("edge %s→%s should be allowed, got %v", from, to, err)
			case !want && err == nil:
				t.Errorf("edge %s→%s is undefined but Advance allowed it", from, to)
			case !want && err != ErrUndefinedTransition:
				t.Errorf("edge %s→%s: want ErrUndefinedTransition, got %v", from, to, err)
			}
		}
	}
}

// TestAdvance_RejectsUnknownState fails closed on an endpoint that is not a §8.4
// state.
func TestAdvance_RejectsUnknownState(t *testing.T) {
	if err := Advance(State("nonsense"), StateDraft); err != ErrUnknownState {
		t.Errorf("unknown from-state: want ErrUnknownState, got %v", err)
	}
	if err := Advance(StateDraft, State("nonsense")); err != ErrUnknownState {
		t.Errorf("unknown to-state: want ErrUnknownState, got %v", err)
	}
}

// TestTransitionTable_MatchesPRDCount pins the edge count so an accidental added
// or dropped edge fails the build.
func TestTransitionTable_MatchesPRDCount(t *testing.T) {
	got := len(Transitions())
	want := len(expectedEdges())
	if got != want {
		t.Fatalf("transition table has %d edges; PRD diagram has %d", got, want)
	}
}

// TestTerminalStates asserts the exact set of terminal states (no outgoing edge).
func TestTerminalStates(t *testing.T) {
	wantTerminal := map[State]bool{
		StateAccepted: true,
		StateRejected: true,
		StateFailed:   true,
		StateBlocked:  true,
		StateExpired:  true,
	}
	for _, s := range AllStates {
		if s.Terminal() != wantTerminal[s] {
			t.Errorf("Terminal(%s) = %v; want %v", s, s.Terminal(), wantTerminal[s])
		}
	}
}
