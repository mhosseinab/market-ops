// Package observation is the append-only observation store and the six-state
// evidence-quality machine (PRD §7.3 OBS-001..004/OBS-008, §10.3, §16). It turns
// a validated capture into append-only evidence, derives the current Observed
// Offer view, deduplicates replays without losing route provenance, and enforces
// that an expired value can never satisfy a current-data gate.
//
// Money quarantine (PRD §9.1): price is carried ONLY as money.RawAmount evidence.
// This package never constructs a Money, never converts units, and never assumes
// IRR/Toman — the source unit is validation-gated (Gate 0a) and unknown.
package observation

// Quality is one of the SIX evidence-quality states (PRD §10.3, OBS-003). The set
// is closed: there is no seventh state, and the string values are the storage /
// wire keys. Each state has a fixed display/recommend/execute consequence,
// declared once in the ConsequenceMatrix below and asserted by fixture tests.
type Quality string

const (
	// Verified: fresh, schema-valid, identity-valid evidence corroborated within
	// window by a second qualifying path or a verified official signal.
	Verified Quality = "verified"
	// Supported: one fresh qualifying path plus consistent recent history.
	Supported Quality = "supported"
	// Unverified: a value was captured but schema, unit, parser, or identity
	// confidence is below threshold.
	Unverified Quality = "unverified"
	// Conflicted: qualifying routes or official/current state disagree outside
	// tolerance.
	Conflicted Quality = "conflicted"
	// Stale: last valid evidence exceeds the freshness deadline.
	Stale Quality = "stale"
	// Unavailable: no usable current value.
	Unavailable Quality = "unavailable"
)

// AllQualities is the closed six-state set, in §10.3 table order.
var AllQualities = []Quality{Verified, Supported, Unverified, Conflicted, Stale, Unavailable}

// Valid reports whether q is one of the six states.
func (q Quality) Valid() bool {
	switch q {
	case Verified, Supported, Unverified, Conflicted, Stale, Unavailable:
		return true
	default:
		return false
	}
}

// DisplayMode is how a value in a given quality state may be shown (PRD §10.3
// "Display" column). It is not free-form: the edge renders the canonical state
// copy from the localization glossary keyed on this mode.
type DisplayMode string

const (
	// DisplayFull: the value is shown normally ("Yes").
	DisplayFull DisplayMode = "full"
	// DisplayWithWarning: the value is shown but flagged low-confidence.
	DisplayWithWarning DisplayMode = "with_warning"
	// DisplayWithConflict: the value is shown with the conflicting route values/times.
	DisplayWithConflict DisplayMode = "with_conflict"
	// DisplayAgeOnly: the value is NOT shown; only its age is rendered (Stale).
	DisplayAgeOnly DisplayMode = "age_only"
	// DisplayStateOnly: neither value nor age; only the state (Unavailable).
	DisplayStateOnly DisplayMode = "state_only"
)

// ExecuteMode is the §10.3 "Execute" column: whether and under what condition an
// action may execute on evidence in this state.
type ExecuteMode string

const (
	// ExecuteNever: execution is blocked outright.
	ExecuteNever ExecuteMode = "never"
	// ExecuteIfGatesPass: Verified — execute only if all other gates pass.
	ExecuteIfGatesPass ExecuteMode = "if_gates_pass"
	// ExecuteAfterJITRefresh: Supported — execute only after a successful
	// just-in-time refresh within the OBS-009 window and tolerance.
	ExecuteAfterJITRefresh ExecuteMode = "after_jit_refresh"
)

// Consequence is the fixed display/recommend/execute matrix row for a quality
// state (PRD §10.3). CanShowValue/Recommend/CanExecute are the load-bearing
// booleans the fixture table asserts; Display and Execute carry the exact mode.
type Consequence struct {
	// Display is the rendering mode for the value.
	Display DisplayMode
	// CanShowValue reports whether the actual value may be displayed at all
	// (false for Stale age-only and Unavailable state-only).
	CanShowValue bool
	// Recommend reports whether the value may drive a recommendation.
	Recommend bool
	// Execute is the execution mode (never / if-gates-pass / after-JIT-refresh).
	Execute ExecuteMode
	// CanExecute reports whether execution is EVER permitted for this state
	// (true only for Verified and Supported, each still subject to its condition).
	CanExecute bool
}

// consequenceMatrix is THE §10.3 table as data — the single source of truth for
// every surface. Fixture tests assert every state and every boolean/mode against
// the PRD wording; nothing may read these consequences from anywhere else.
var consequenceMatrix = map[Quality]Consequence{
	Verified:    {Display: DisplayFull, CanShowValue: true, Recommend: true, Execute: ExecuteIfGatesPass, CanExecute: true},
	Supported:   {Display: DisplayFull, CanShowValue: true, Recommend: true, Execute: ExecuteAfterJITRefresh, CanExecute: true},
	Unverified:  {Display: DisplayWithWarning, CanShowValue: true, Recommend: false, Execute: ExecuteNever, CanExecute: false},
	Conflicted:  {Display: DisplayWithConflict, CanShowValue: true, Recommend: false, Execute: ExecuteNever, CanExecute: false},
	Stale:       {Display: DisplayAgeOnly, CanShowValue: false, Recommend: false, Execute: ExecuteNever, CanExecute: false},
	Unavailable: {Display: DisplayStateOnly, CanShowValue: false, Recommend: false, Execute: ExecuteNever, CanExecute: false},
}

// ConsequenceOf returns the §10.3 consequence for a quality state. An unknown
// state fails closed to the most restrictive consequence (Unavailable), so a
// mislabeled value can never display, recommend, or execute.
func ConsequenceOf(q Quality) Consequence {
	if c, ok := consequenceMatrix[q]; ok {
		return c
	}
	return consequenceMatrix[Unavailable]
}

// SatisfiesCurrentDataGate reports whether a value in this state may satisfy a
// "current-data" gate (PRD OBS-004, §16 "Routes disagree ... block"). It FAILS
// CLOSED: an EXPIRED value (Stale), an Unavailable one, a Conflicted one (routes
// disagree — §16 block), and any UNKNOWN state are all excluded. Only the fresh,
// non-disagreeing states — Verified, Supported, Unverified — are current;
// execute/recommend gating is still separate (CanExecute/Recommend), so callers
// combine this freshness gate with the consequence matrix.
func (q Quality) SatisfiesCurrentDataGate() bool {
	switch q {
	case Verified, Supported, Unverified:
		return true
	default:
		// Conflicted, Stale, Unavailable, and any unrecognized state fail closed.
		return false
	}
}

// QualitySignals are the derivation inputs for a single capture. They are the
// only thing that decides a state — never the marketplace name or a default.
type QualitySignals struct {
	// HasValue is false when no usable current value was captured.
	HasValue bool
	// Disappeared marks an offer that has vanished (§16 close, never zero price).
	Disappeared bool
	// Fresh is false once the capture is past its freshness deadline.
	Fresh bool
	// SchemaValid / IdentityValid gate below-threshold captures to Unverified.
	SchemaValid   bool
	IdentityValid bool
	// LowConfidence marks a capture whose parser/unit confidence is below
	// threshold (docs/08 confidence != verified/partially_verified qualifying).
	LowConfidence bool
	// Conflicted marks qualifying routes / official state disagreeing outside
	// tolerance (§16 "Routes disagree").
	Conflicted bool
	// Corroborated marks a SECOND qualifying path (a different route, or a verified
	// official signal) agreeing on the SAME value WITHIN its own freshness window —
	// the Verified precondition (§10.3 line 709 "corroborated within window"). It is
	// derived from in-window append-only evidence, never from a client claim.
	Corroborated bool
	// HasHistory marks that the current value has consistent recent history: at
	// least one prior in-window observation of the same value (§10.3 line 710
	// "one fresh qualifying path PLUS consistent recent history"). A first-ever
	// sighting has no history and cannot reach Supported.
	HasHistory bool
}

// DeriveQuality maps capture signals onto exactly one of the six states. The
// order encodes the §10.3 precedence: absence and staleness dominate; then a
// STRUCTURAL/IDENTITY quarantine gate (schema-invalid or identity-invalid
// evidence) floors to Unverified BEFORE conflict evaluation; then a live
// cross-route conflict BLOCKS (§16); then below-threshold confidence degrades to
// Unverified; then in-window cross-route corroboration yields Verified; then a
// single fresh valid path WITH consistent recent history yields Supported. A
// first-ever sighting (no history, no corroboration) is Unverified — a single
// client capture can never self-promote to Supported or Verified on its own word.
//
// Why schema/identity precedes conflict (#307, follow-up to #154): an
// unregistered/retired/malformed parser (registry miss → SchemaValid withheld) or
// an identity-invalid capture is UNTRUSTED evidence. Untrusted evidence must not
// be able to assert a §16 disagreement, or a client with a valid capture
// credential could send a bogus parserVersion plus a disagreeing value to force a
// legitimate offer to Conflicted (a signal-suppression / quarantine-isolation gap).
// Flooring it to Unverified first keeps it non-recommend / non-execute while the
// disagreement is still retained as append-only evidence — it just no longer
// blocks. Registered, schema-valid, identity-valid captures are unaffected: a
// genuine qualifying-route disagreement still reaches Conflicted below, and
// low-confidence precedence relative to conflict is unchanged.
func DeriveQuality(s QualitySignals) Quality {
	switch {
	case !s.HasValue || s.Disappeared:
		return Unavailable
	case !s.Fresh:
		return Stale
	case !s.SchemaValid || !s.IdentityValid:
		return Unverified
	case s.Conflicted:
		return Conflicted
	case s.LowConfidence:
		return Unverified
	case s.Corroborated:
		return Verified
	case s.HasHistory:
		return Supported
	default:
		return Unverified
	}
}
