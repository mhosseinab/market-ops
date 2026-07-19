package cost

// Cost-profile provenance (PRD §9.2, §16, CST-003). Some components may only
// satisfy their hard requirement when their in-force version comes from an
// AUTHORITATIVE source — a seller-entered value is preserved as evidence but must
// never be inferred to be the marketplace-authoritative figure (quarantine-over-
// inference, §4.6). This is the single source of that domain knowledge (DRY):
// both the readiness rule (RequiresAuthoritativeProvenance) and the source
// classification (IsAuthoritativeSource) live here, not scattered across service
// wiring.

// Cost-profile source identifiers. These mirror the cost_profiles.source CHECK
// constraint values exactly; do not introduce a source token that is not also a
// permitted CHECK value.
const (
	// SourceCSVImport — a value committed from a seller CSV import (CST-001).
	// Seller-supplied provenance: NOT authoritative for commission.
	SourceCSVImport = "csv_import"
	// SourceSingleValue — a value from single-value seller entry (e.g. the chat
	// blocker flow). Seller-supplied provenance: NOT authoritative for commission.
	SourceSingleValue = "single_value"
	// SourceConnector — a value derived from the official DK connector. This is the
	// authoritative provenance §9.2 requires for commission.
	SourceConnector = "connector"
)

// authoritativeSources are the cost-profile sources DK treats as authoritative
// provenance for components that require it (§9.2 "official connector or verified
// category rule"). Only connector-derived is authoritative in P0; a verified
// category rule would be a FUTURE authoritative source and, when modelled, would
// be added here (and to the source CHECK) — never satisfied by silently trusting
// a seller value.
var authoritativeSources = map[string]bool{
	SourceConnector: true,
}

// IsAuthoritativeSource reports whether a cost-profile source string is
// authoritative provenance (§9.2). Fail-closed: an unknown/empty source is NOT
// authoritative.
func IsAuthoritativeSource(source string) bool {
	return authoritativeSources[source]
}

// authoritativeProvenance are the components whose hard requirement is satisfied
// ONLY by an authoritative source (§9.2). Commission must come from the official
// connector (or a verified category rule); a present-but-seller-entered commission
// does not satisfy the requirement and leaves the SKU blocked (§16 "unknown
// commission → block executable recommendation").
var authoritativeProvenance = map[Component]bool{
	ComponentCommission: true,
}

// RequiresAuthoritativeProvenance reports whether component c may only satisfy its
// requirement when backed by an authoritative source. For such a component a
// present but non-authoritative version is treated as NOT satisfying the
// requirement in DeriveReadiness.
func (c Component) RequiresAuthoritativeProvenance() bool {
	return authoritativeProvenance[c]
}
