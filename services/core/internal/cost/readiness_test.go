package cost

import (
	"reflect"
	"testing"
)

// present/staleP are readiness-input builders keeping the table compact.
func present() ComponentPresence { return ComponentPresence{Present: true} }
func staleP() ComponentPresence  { return ComponentPresence{Present: true, Stale: true} }

func comps(m map[Component]ComponentPresence) map[Component]ComponentPresence { return m }
func set(cs ...Component) map[Component]bool {
	out := map[Component]bool{}
	for _, c := range cs {
		out[c] = true
	}
	return out
}

// TestDeriveReadiness_TransitionTable exercises every one of the four CST-003
// states, plus the precedence between them (Missing > Stale > Partial > Complete).
func TestDeriveReadiness_TransitionTable(t *testing.T) {
	tests := []struct {
		name        string
		in          ReadinessInput
		wantState   State
		wantMissing []Component
		wantStale   []Component
	}{
		{
			name:        "missing: no cogs at all",
			in:          ReadinessInput{Components: comps(map[Component]ComponentPresence{ComponentCommission: present()})},
			wantState:   StateMissing,
			wantMissing: []Component{ComponentCOGS},
		},
		{
			name:        "missing: cogs present, commission absent",
			in:          ReadinessInput{Components: comps(map[Component]ComponentPresence{ComponentCOGS: present()})},
			wantState:   StateMissing,
			wantMissing: []Component{ComponentCommission},
		},
		{
			name: "stale: hard requirements present but cogs stale",
			in: ReadinessInput{Components: comps(map[Component]ComponentPresence{
				ComponentCOGS:       staleP(),
				ComponentCommission: present(),
			})},
			wantState: StateStale,
			wantStale: []Component{ComponentCOGS},
		},
		{
			name: "partial: hard fresh, applicable fulfillment missing",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCOGS:       present(),
					ComponentCommission: present(),
				}),
				Applicable: set(ComponentFulfillment),
			},
			wantState:   StatePartial,
			wantMissing: []Component{ComponentFulfillment},
		},
		{
			name: "partial: hard fresh, policy-required packaging missing",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCOGS:       present(),
					ComponentCommission: present(),
				}),
				RequiredOptional: set(ComponentPackaging),
			},
			wantState:   StatePartial,
			wantMissing: []Component{ComponentPackaging},
		},
		{
			name: "complete: only hard requirements, both fresh",
			in: ReadinessInput{Components: comps(map[Component]ComponentPresence{
				ComponentCOGS:       present(),
				ComponentCommission: present(),
			})},
			wantState: StateComplete,
		},
		{
			name: "complete: hard + applicable + policy-required all present fresh",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCOGS:        present(),
					ComponentCommission:  present(),
					ComponentFulfillment: present(),
					ComponentPackaging:   present(),
				}),
				Applicable:       set(ComponentFulfillment),
				RequiredOptional: set(ComponentPackaging),
			},
			wantState: StateComplete,
		},
		{
			name: "complete: unrequired optional absent does not demote",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCOGS:       present(),
					ComponentCommission: present(),
				}),
				// ads/returns not required by policy, fulfillment not applicable.
			},
			wantState: StateComplete,
		},
		{
			name: "precedence: missing hard beats stale and partial",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCommission:  staleP(),
					ComponentFulfillment: present(),
				}),
				Applicable: set(ComponentFulfillment),
			},
			wantState:   StateMissing,
			wantMissing: []Component{ComponentCOGS},
			wantStale:   []Component{ComponentCommission},
		},
		{
			name: "precedence: stale beats partial",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCOGS:       staleP(),
					ComponentCommission: present(),
				}),
				Applicable: set(ComponentShipping), // applicable but absent → would be partial
			},
			wantState:   StateStale,
			wantMissing: []Component{ComponentShipping},
			wantStale:   []Component{ComponentCOGS},
		},
	}

	seen := map[State]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveReadiness(tt.in)
			if got.State != tt.wantState {
				t.Errorf("state = %q, want %q", got.State, tt.wantState)
			}
			if !reflect.DeepEqual(nonEmpty(got.Missing), nonEmpty(tt.wantMissing)) {
				t.Errorf("missing = %v, want %v", got.Missing, tt.wantMissing)
			}
			if !reflect.DeepEqual(nonEmpty(got.Stale), nonEmpty(tt.wantStale)) {
				t.Errorf("stale = %v, want %v", got.Stale, tt.wantStale)
			}
			seen[got.State] = true
		})
	}

	for _, s := range []State{StateComplete, StatePartial, StateStale, StateMissing} {
		if !seen[s] {
			t.Errorf("transition table did not cover state %q", s)
		}
	}
}

func nonEmpty(cs []Component) []Component {
	if len(cs) == 0 {
		return nil
	}
	return cs
}
