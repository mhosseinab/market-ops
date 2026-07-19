package cost

// State is a margin-readiness state (CST-003). The set is closed and exactly the
// four PRD states. Only Complete may drive an executable recommendation; Partial
// may show analysis but exposes no approval control; Stale and Missing block.
type State string

const (
	// StateComplete — every required component is present and fresh. The ONLY
	// state that may drive an executable recommendation (enforced in S16/S17).
	StateComplete State = "complete"
	// StatePartial — hard requirements are present and fresh, but a
	// required-when-applicable or policy-required component is absent. Analysis
	// may be shown; NO approval control.
	StatePartial State = "partial"
	// StateStale — a required, present component is past its review-by instant.
	// Blocks (§16 "stale COGS → block").
	StateStale State = "stale"
	// StateMissing — a hard requirement (COGS or commission) is absent. Blocks
	// (§16 "missing COGS / unknown commission → block").
	StateMissing State = "missing"
)

// ComponentPresence is the in-force status of one component for a SKU at the
// evaluation instant: whether a version exists, whether that version is stale
// (past its review-by instant), and whether it comes from an authoritative
// source. Authoritative matters only for components that
// RequiresAuthoritativeProvenance (commission, §9.2): for those, a present but
// non-authoritative version does NOT satisfy the requirement.
type ComponentPresence struct {
	Present bool
	Stale   bool
	// Authoritative reports whether the in-force version's source is authoritative
	// provenance (§9.2). Set from IsAuthoritativeSource at recompute time. It is
	// consulted only for components that RequiresAuthoritativeProvenance; other
	// components are satisfied regardless of source.
	Authoritative bool
}

// ReadinessInput is the pure input to DeriveReadiness. It carries no time, no
// DB, and no money — the service resolves those and hands over booleans, so the
// four-state rule is deterministic and property-testable in isolation.
type ReadinessInput struct {
	// Components holds the presence/staleness of each component in force at the
	// evaluation instant. A component absent from the map is treated as not present.
	Components map[Component]ComponentPresence
	// Applicable are the required-when-applicable components that apply to this
	// listing (subset of fulfillment/shipping/promotion), from per-SKU data.
	Applicable map[Component]bool
	// RequiredOptional are the P0-optional components this account's policy
	// requires (subset of packaging/ads/returns), from account policy.
	RequiredOptional map[Component]bool
}

// Readiness is the derived readiness verdict for a SKU.
type Readiness struct {
	State State
	// Missing names every required component with no in-force version, in
	// canonical order (blocker chips).
	Missing []Component
	// Stale names every required, present component that is past its review-by
	// instant, in canonical order.
	Stale []Component
}

// required reports whether c is required for THIS SKU under the given input:
// always for hard components, when applicable for the applicable-optional set,
// and when the account policy requires it for the P0-optional set.
func (in ReadinessInput) required(c Component) bool {
	switch {
	case c.IsHardRequired():
		return true
	case c.IsApplicableOptional():
		return in.Applicable[c]
	case c.IsP0Optional():
		return in.RequiredOptional[c]
	default:
		return false
	}
}

// DeriveReadiness computes the four-state readiness (CST-003) from presence,
// applicability, and policy. Precedence, hardest first: Missing (a hard
// requirement absent) > Stale (a required present component aged out) > Partial
// (a required-when-applicable/policy component absent) > Complete. The precedence
// is total, so exactly one state results for any input.
func DeriveReadiness(in ReadinessInput) Readiness {
	var missing, stale []Component
	hardMissing := false

	for _, c := range AllComponents {
		if !in.required(c) {
			continue
		}
		p := in.Components[c]
		// A component that requires authoritative provenance (commission, §9.2) is
		// NOT satisfied by a present-but-non-authoritative (seller-entered) version:
		// treat it as absent so it blocks (§16) rather than being inferred to be the
		// marketplace figure (quarantine-over-inference, §4.6). The seller value is
		// still stored and shown as evidence; it just cannot drive Complete.
		if !p.Present || (c.RequiresAuthoritativeProvenance() && !p.Authoritative) {
			missing = append(missing, c)
			if c.IsHardRequired() {
				hardMissing = true
			}
			continue
		}
		if p.Stale {
			stale = append(stale, c)
		}
	}

	var state State
	switch {
	case hardMissing:
		state = StateMissing
	case len(stale) > 0:
		state = StateStale
	case len(missing) > 0:
		// Only non-hard requirements can be missing here (hard-missing handled
		// above): base contribution is computable, so analysis is possible but the
		// SKU is not execution-complete.
		state = StatePartial
	default:
		state = StateComplete
	}

	return Readiness{State: state, Missing: missing, Stale: stale}
}
