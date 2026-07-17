// Package cost implements effective-dated, component-versioned cost profiles,
// the CSV import pipeline (preview + row dispositions), single-value cost entry,
// point-in-time version lookup, and margin-readiness derivation (PRD §7.2
// CST-001..003, §9.2, §16).
//
// MONEY (PRD §9.1, never-cut): a cost value is seller-entered in the account's
// configured currency, so it is representable as an authoritative money.Money
// (currency known). It is stored as the exact (mantissa, currency, exponent)
// triple — NEVER a float — and the raw entered text/value/unit is preserved
// separately as evidence. These values are deliberately kept OUT of every
// executable path until S16 (contribution/policy) + S35 (verified parameters);
// nothing here wires a cost into an approve/execute path.
package cost

// Component is one cost component of the §9.2 contribution model. The set is
// closed and matches the cost_profiles CHECK constraint.
type Component string

const (
	// ComponentCOGS — cost of goods sold. Required, seller-supplied; the
	// action-eligibility gate (no executable recommendation without it).
	ComponentCOGS Component = "cogs"
	// ComponentCommission — marketplace commission. Required.
	ComponentCommission Component = "commission"
	// ComponentFulfillment — fulfillment cost. Required WHEN APPLICABLE (§9.2).
	ComponentFulfillment Component = "fulfillment"
	// ComponentShipping — seller-funded shipping. Required WHEN APPLICABLE.
	ComponentShipping Component = "shipping"
	// ComponentPackaging — packaging. Optional in P0; account policy may require it.
	ComponentPackaging Component = "packaging"
	// ComponentPromotion — seller-funded promotion. Required WHEN APPLICABLE.
	ComponentPromotion Component = "promotion"
	// ComponentAds — variable advertising allocation. Optional in P0; policy may require.
	ComponentAds Component = "ads"
	// ComponentReturns — expected returns allowance. Optional in P0; policy may require.
	ComponentReturns Component = "returns"
)

// AllComponents is the canonical ordering used for deterministic output (blocker
// chips, missing/stale lists). It matches the §9.2 contribution formula order.
var AllComponents = []Component{
	ComponentCOGS,
	ComponentCommission,
	ComponentFulfillment,
	ComponentShipping,
	ComponentPackaging,
	ComponentPromotion,
	ComponentAds,
	ComponentReturns,
}

// hardRequired are the components that are ALWAYS required (§9.2): their absence
// is a hard block (Missing readiness), and no analysis of base contribution is
// possible without them.
var hardRequired = map[Component]bool{
	ComponentCOGS:       true,
	ComponentCommission: true,
}

// applicableOptional are the components required ONLY WHEN APPLICABLE to the
// listing (§9.2). Applicability is per-SKU data (sku_cost_requirements), never a
// hardcoded rule.
var applicableOptional = map[Component]bool{
	ComponentFulfillment: true,
	ComponentShipping:    true,
	ComponentPromotion:   true,
}

// p0Optional are optional in P0 (§9.2). They are required for readiness ONLY
// when the account policy lists them (required_optional_components).
var p0Optional = map[Component]bool{
	ComponentPackaging: true,
	ComponentAds:       true,
	ComponentReturns:   true,
}

// Valid reports whether c is a known cost component.
func (c Component) Valid() bool {
	return hardRequired[c] || applicableOptional[c] || p0Optional[c]
}

// IsHardRequired reports whether c is always required (COGS or commission).
func (c Component) IsHardRequired() bool { return hardRequired[c] }

// IsApplicableOptional reports whether c is required only when applicable.
func (c Component) IsApplicableOptional() bool { return applicableOptional[c] }

// IsP0Optional reports whether c is P0-optional (policy may require it).
func (c Component) IsP0Optional() bool { return p0Optional[c] }

// ParseComponent normalizes and validates a component token from a CSV header or
// API field. It returns ok=false for an unknown token (fail closed).
func ParseComponent(s string) (Component, bool) {
	c := Component(s)
	if c.Valid() {
		return c, true
	}
	return "", false
}
