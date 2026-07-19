package cost

import (
	"reflect"
	"testing"
)

// present/staleP are readiness-input builders keeping the table compact. present
// is a NON-authoritative (seller-supplied) version; authPresent/authStaleP are
// authoritative (e.g. connector-derived). Commission must be authoritative to
// satisfy its hard requirement (§9.2); COGS and the optional components do not
// require authoritative provenance.
func present() ComponentPresence     { return ComponentPresence{Present: true} }
func staleP() ComponentPresence      { return ComponentPresence{Present: true, Stale: true} }
func authPresent() ComponentPresence { return ComponentPresence{Present: true, Authoritative: true} }
func authStaleP() ComponentPresence {
	return ComponentPresence{Present: true, Stale: true, Authoritative: true}
}

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
			in:          ReadinessInput{Components: comps(map[Component]ComponentPresence{ComponentCommission: authPresent()})},
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
				ComponentCommission: authPresent(),
			})},
			wantState: StateStale,
			wantStale: []Component{ComponentCOGS},
		},
		{
			name: "partial: hard fresh, applicable fulfillment missing",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCOGS:       present(),
					ComponentCommission: authPresent(),
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
					ComponentCommission: authPresent(),
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
				ComponentCommission: authPresent(),
			})},
			wantState: StateComplete,
		},
		{
			name: "complete: hard + applicable + policy-required all present fresh",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCOGS:        present(),
					ComponentCommission:  authPresent(),
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
					ComponentCommission: authPresent(),
				}),
				// ads/returns not required by policy, fulfillment not applicable.
			},
			wantState: StateComplete,
		},
		{
			name: "precedence: missing hard beats stale and partial",
			in: ReadinessInput{
				Components: comps(map[Component]ComponentPresence{
					ComponentCommission:  authStaleP(),
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
					ComponentCommission: authPresent(),
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

// TestDeriveReadiness_CommissionProvenance is the issue #38 regression guard
// (PRD §9.2, §16, CST-003): commission satisfies its hard requirement ONLY from
// an authoritative source. A present-but-seller-entered (non-authoritative)
// commission must leave the SKU blocked (Missing) even with fresh COGS — it must
// never be inferred to be the marketplace fee. The same SKU becomes Complete only
// once an authoritative commission is present.
func TestDeriveReadiness_CommissionProvenance(t *testing.T) {
	// Non-authoritative commission (seller-entered) + fresh COGS → still Missing,
	// with commission named as the blocker.
	got := DeriveReadiness(ReadinessInput{Components: comps(map[Component]ComponentPresence{
		ComponentCOGS:       present(),
		ComponentCommission: present(), // present but NOT authoritative
	})})
	if got.State != StateMissing {
		t.Fatalf("seller-entered commission state = %q, want missing", got.State)
	}
	if !reflect.DeepEqual(got.Missing, []Component{ComponentCommission}) {
		t.Fatalf("seller-entered commission missing = %v, want [commission]", got.Missing)
	}

	// Authoritative commission (connector-derived) + fresh COGS → Complete.
	got = DeriveReadiness(ReadinessInput{Components: comps(map[Component]ComponentPresence{
		ComponentCOGS:       present(),
		ComponentCommission: authPresent(),
	})})
	if got.State != StateComplete {
		t.Fatalf("authoritative commission state = %q, want complete", got.State)
	}

	// A stale authoritative commission is Stale (its authoritative version aged
	// out), NOT Missing — provenance and staleness are distinct dimensions.
	got = DeriveReadiness(ReadinessInput{Components: comps(map[Component]ComponentPresence{
		ComponentCOGS:       present(),
		ComponentCommission: authStaleP(),
	})})
	if got.State != StateStale {
		t.Fatalf("stale authoritative commission state = %q, want stale", got.State)
	}
}

// TestRequiresAuthoritativeProvenance pins the single source of the provenance
// rule: commission requires it; COGS and the optional components do not.
func TestRequiresAuthoritativeProvenance(t *testing.T) {
	if !ComponentCommission.RequiresAuthoritativeProvenance() {
		t.Error("commission must require authoritative provenance (§9.2)")
	}
	for _, c := range []Component{ComponentCOGS, ComponentFulfillment, ComponentShipping, ComponentPackaging, ComponentPromotion, ComponentAds, ComponentReturns} {
		if c.RequiresAuthoritativeProvenance() {
			t.Errorf("%s must not require authoritative provenance", c)
		}
	}
	if !IsAuthoritativeSource(SourceConnector) {
		t.Error("connector must be an authoritative source")
	}
	for _, s := range []string{SourceCSVImport, SourceSingleValue, "", "bogus"} {
		if IsAuthoritativeSource(s) {
			t.Errorf("source %q must not be authoritative", s)
		}
	}
}

func nonEmpty(cs []Component) []Component {
	if len(cs) == 0 {
		return nil
	}
	return cs
}
