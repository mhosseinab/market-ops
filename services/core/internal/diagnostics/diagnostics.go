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

	// Description is the captured listing description content, distinguishing the
	// three evidence-quality observed states (§4.6):
	//   nil            → NOT OBSERVED: the connector has not surfaced this field for
	//                    the variant → not_observed/warn (fail closed, never inferred).
	//   &""            → OBSERVED EMPTY: the field was observed and is genuinely
	//                    absent → empty/warn (distinct from not-yet-observed).
	//   &"<non-empty>" → OBSERVED PRESENT → present/pass, carrying LENGTH only.
	// The pointer is the ONLY signal that separates genuinely-absent from
	// not-yet-observed; the raw description text never leaves this boundary.
	Description *string

	// ImageCount is the captured number of listing images, with the same three-state
	// semantics as Description:
	//   nil  → NOT OBSERVED → not_observed/warn (fail closed).
	//   &0   → OBSERVED EMPTY: observed with zero images → empty/warn.
	//   &n>0 → OBSERVED PRESENT → present/pass. Image is not a text field, so no
	//          character-length metadata is recorded. A captured count is
	//          non-negative; a non-positive count is treated as observed-empty.
	ImageCount *int
}

// Derive computes the deterministic, READ-ONLY diagnostics for one variant. The
// order is stable (title, description, image). It NAMES every observed field and
// rule; it never generates content and never infers an unobserved value into a
// pass.
func Derive(in Input) []Diagnostic {
	return []Diagnostic{
		titleDiagnostic(in),
		descriptionDiagnostic(in),
		imageDiagnostic(in),
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

// listingFieldBase builds the NAMED, referenced skeleton (entity, field, rule,
// evidence ref, capture time) shared by every listing-level diagnostic. The
// observed state and result are filled in by the field-specific deriver.
func listingFieldBase(in Input, field Field, ruleID string) Diagnostic {
	return Diagnostic{
		Entity:      EntityListing,
		Field:       field,
		RuleID:      ruleID,
		RuleVersion: RuleVersionV1,
		EvidenceRef: listingEvidenceRef(in),
		CapturedAt:  in.CapturedAt,
	}
}

// descriptionDiagnostic derives the listing description diagnostic from the
// CAPTURED observation (Input.Description), honouring the three evidence-quality
// states (§4.6, never-cut): not-observed (nil) fails closed to not_observed/warn;
// observed-empty ("") is genuinely-absent → empty/warn; observed-present passes
// with LENGTH metadata only — the description text itself never leaves the input.
// It NAMES its field + rule and NEVER fabricates a pass from an unobserved field.
func descriptionDiagnostic(in Input) Diagnostic {
	d := listingFieldBase(in, FieldDescription, RuleDescriptionPresent)
	if in.Description == nil {
		d.Result = ResultWarn
		d.Observed = ObservedMeta{State: StateNotObserved}
		return d
	}
	if *in.Description == "" {
		length := 0
		d.Result = ResultWarn
		d.Observed = ObservedMeta{State: StateEmpty, CharacterLength: &length}
		return d
	}
	length := utf8.RuneCountInString(*in.Description)
	d.Result = ResultPass
	d.Observed = ObservedMeta{State: StatePresent, CharacterLength: &length}
	return d
}

// imageDiagnostic derives the listing image diagnostic from the CAPTURED
// observation (Input.ImageCount), honouring the same three evidence-quality states:
// not-observed (nil) fails closed to not_observed/warn; observed with no images
// (<= 0) is genuinely-absent → empty/warn; observed with images passes. Image is
// not a text field, so no character-length metadata is recorded. It NAMES its field
// + rule and NEVER fabricates a pass from an unobserved field.
func imageDiagnostic(in Input) Diagnostic {
	d := listingFieldBase(in, FieldImage, RuleImagePresent)
	if in.ImageCount == nil {
		d.Result = ResultWarn
		d.Observed = ObservedMeta{State: StateNotObserved}
		return d
	}
	if *in.ImageCount <= 0 {
		d.Result = ResultWarn
		d.Observed = ObservedMeta{State: StateEmpty}
		return d
	}
	d.Result = ResultPass
	d.Observed = ObservedMeta{State: StatePresent}
	return d
}
