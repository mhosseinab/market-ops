// Package diagnostics derives the READ-ONLY listing/image diagnostics for a
// variant (S26, LST-001). It reads ONLY already-captured canonical catalog data
// (Product / Variant / Listing) and produces pass/warn results that NAME the
// observed entity + field and the rule id/version they were evaluated against.
//
// LST-001 (never-cut, PRD §4.6): a diagnostic is strictly READ-ONLY — it reports,
// it never generates, rewrites, or publishes content, and there is no write/
// execute/remediation control anywhere in this package. Quarantine-over-inference
// (§9.1): a field whose source content the connector does not yet surface is
// reported observed-state not_observed → warn (fail closed), never inferred and
// never fabricated into a pass. Observed-value metadata carries presence/length
// only — never the raw listing text or an invented value.
package diagnostics

import (
	"time"
	"unicode/utf8"
)

// Entity is the canonical entity a diagnostic observed (§15.1). Named so a result
// is never an anonymous verdict.
type Entity string

const (
	EntityProduct Entity = "product"
	EntityVariant Entity = "variant"
	EntityListing Entity = "listing"
)

// Field is the named listing field a diagnostic evaluated (LST-001).
type Field string

const (
	FieldTitle       Field = "title"
	FieldDescription Field = "description"
	FieldImage       Field = "image"
)

// Result is the read-only pass/warn verdict.
type Result string

const (
	ResultPass Result = "pass"
	ResultWarn Result = "warn"
)

// ObservedState is the observed-value state a diagnostic recorded for its field.
type ObservedState string

const (
	StatePresent     ObservedState = "present"
	StateEmpty       ObservedState = "empty"
	StateNotObserved ObservedState = "not_observed"
)

// Rule identifiers + versions. Stable technical ids the result NAMES (LST-001).
const (
	RuleTitlePresent       = "listing.title.present"
	RuleDescriptionPresent = "listing.description.present"
	RuleImagePresent       = "listing.image.present"
	RuleVersionV1          = "v1"
)

// ObservedMeta is observed-value METADATA only — never the raw text or a
// fabricated value.
type ObservedMeta struct {
	State ObservedState
	// CharacterLength is the rune length of an observed text field; nil when the
	// field is not a captured text value (e.g. not_observed).
	CharacterLength *int
}

// Diagnostic is one read-only listing/image diagnostic result.
type Diagnostic struct {
	Entity      Entity
	Field       Field
	RuleID      string
	RuleVersion string
	Result      Result
	Observed    ObservedMeta
	EvidenceRef string
	CapturedAt  time.Time
}

// Report is the read-only diagnostics report for one variant.
type Report struct {
	VariantID            string
	MarketplaceAccountID string
	EvaluatedAt          time.Time
	Items                []Diagnostic
}

// Input is the already-captured canonical catalog data a report is derived from.
// It carries NO write handle and NO content-generation seam — deriving a report
// mutates nothing.
type Input struct {
	NativeVariantID int64
	VariantTitle    string
	ProductTitle    string
	ListingPresent  bool
	NativeListingID int64
	// CapturedAt is when the underlying catalog data was captured (variant
	// updated_at); every diagnostic reports it as its capture time.
	CapturedAt time.Time
}

// Derive computes the deterministic, READ-ONLY diagnostics for one variant. The
// order is stable (title, description, image). It NAMES every observed field and
// rule; it never generates content and never infers an unobserved value into a
// pass.
func Derive(in Input) []Diagnostic {
	return []Diagnostic{
		titleDiagnostic(in),
		unobservedDiagnostic(in, FieldDescription, RuleDescriptionPresent),
		unobservedDiagnostic(in, FieldImage, RuleImagePresent),
	}
}

// titleDiagnostic evaluates the seller's variant listing title from captured
// catalog data. A non-empty title passes; an empty one warns. The observed
// metadata carries the title's LENGTH, never the title text itself.
func titleDiagnostic(in Input) Diagnostic {
	d := Diagnostic{
		Entity:      EntityVariant,
		Field:       FieldTitle,
		RuleID:      RuleTitlePresent,
		RuleVersion: RuleVersionV1,
		EvidenceRef: variantEvidenceRef(in.NativeVariantID),
		CapturedAt:  in.CapturedAt,
	}
	if in.VariantTitle == "" {
		length := 0
		d.Result = ResultWarn
		d.Observed = ObservedMeta{State: StateEmpty, CharacterLength: &length}
		return d
	}
	length := utf8.RuneCountInString(in.VariantTitle)
	d.Result = ResultPass
	d.Observed = ObservedMeta{State: StatePresent, CharacterLength: &length}
	return d
}

// unobservedDiagnostic reports a field the connector does not yet surface as
// not_observed → warn (fail closed, quarantine-over-inference). It NAMES the
// field + rule and NEVER fabricates a pass. When the DK Seller connector begins
// surfacing listing description/image content (a go_connector_observer step),
// this is the seam that gains a real observed value — until then it honestly
// reports the field as unobserved.
func unobservedDiagnostic(in Input, field Field, ruleID string) Diagnostic {
	return Diagnostic{
		Entity:      EntityListing,
		Field:       field,
		RuleID:      ruleID,
		RuleVersion: RuleVersionV1,
		Result:      ResultWarn,
		Observed:    ObservedMeta{State: StateNotObserved},
		EvidenceRef: listingEvidenceRef(in),
		CapturedAt:  in.CapturedAt,
	}
}
