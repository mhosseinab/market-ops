package diagnostics

import (
	"testing"
	"time"
)

func capturedAt() time.Time { return time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC) }

// A populated title is observed present and PASSES, naming its field + rule and
// carrying the title LENGTH (never the title text).
func TestDeriveTitlePresentPasses(t *testing.T) {
	items := Derive(Input{NativeVariantID: 7719004, VariantTitle: "Kettle 1.7L", CapturedAt: capturedAt()})

	title := findField(t, items, FieldTitle)
	if title.Result != ResultPass {
		t.Fatalf("expected pass, got %q", title.Result)
	}
	if title.Entity != EntityVariant {
		t.Fatalf("expected entity variant, got %q", title.Entity)
	}
	if title.RuleID != RuleTitlePresent || title.RuleVersion != RuleVersionV1 {
		t.Fatalf("title diagnostic must NAME its rule; got %q/%q", title.RuleID, title.RuleVersion)
	}
	if title.Observed.State != StatePresent {
		t.Fatalf("expected observed present, got %q", title.Observed.State)
	}
	if title.Observed.CharacterLength == nil || *title.Observed.CharacterLength != 11 {
		t.Fatalf("expected character length 11 metadata, got %v", title.Observed.CharacterLength)
	}
	if title.EvidenceRef != "catalog/variant/7719004" {
		t.Fatalf("unexpected evidence ref %q", title.EvidenceRef)
	}
	if !title.CapturedAt.Equal(capturedAt()) {
		t.Fatalf("expected capture time carried through, got %v", title.CapturedAt)
	}
}

// An empty title is observed empty and WARNS (never dropped, never inferred).
func TestDeriveTitleEmptyWarns(t *testing.T) {
	items := Derive(Input{NativeVariantID: 1, VariantTitle: "", CapturedAt: capturedAt()})

	title := findField(t, items, FieldTitle)
	if title.Result != ResultWarn {
		t.Fatalf("expected warn for empty title, got %q", title.Result)
	}
	if title.Observed.State != StateEmpty {
		t.Fatalf("expected observed empty, got %q", title.Observed.State)
	}
}

// NEGATIVE (fail-closed): when the description/image observation is ABSENT from the
// input (nil = the connector has not surfaced that field for the variant), the
// diagnostics report not_observed → warn and can NEVER become a fabricated pass,
// even when every OTHER input field is fully populated. This is the
// quarantine-over-inference guard for the LST-001 dark posture.
func TestDeriveUnobservedFieldsNeverFabricatePass(t *testing.T) {
	fullyPopulated := Input{
		NativeVariantID: 7719004,
		VariantTitle:    "Kettle 1.7L",
		ProductTitle:    "Electric Kettle",
		ListingPresent:  true,
		NativeListingID: 8842213,
		CapturedAt:      capturedAt(),
	}
	items := Derive(fullyPopulated)

	for _, field := range []Field{FieldDescription, FieldImage} {
		d := findField(t, items, field)
		if d.Result != ResultWarn {
			t.Fatalf("%s must fail closed to warn, got %q", field, d.Result)
		}
		if d.Observed.State != StateNotObserved {
			t.Fatalf("%s must be not_observed (never inferred), got %q", field, d.Observed.State)
		}
		if d.Observed.CharacterLength != nil {
			t.Fatalf("%s must carry no fabricated length metadata", field)
		}
		if d.Entity != EntityListing {
			t.Fatalf("%s expected entity listing, got %q", field, d.Entity)
		}
		if d.RuleID == "" || d.RuleVersion == "" {
			t.Fatalf("%s must NAME its rule (LST-001)", field)
		}
		if d.EvidenceRef != "catalog/listing/8842213" {
			t.Fatalf("%s expected listing evidence ref, got %q", field, d.EvidenceRef)
		}
	}
}

// The unobserved diagnostics reference the VARIANT when no listing presence row
// exists (still a reference, never content).
func TestDeriveUnobservedEvidenceFallsBackToVariant(t *testing.T) {
	items := Derive(Input{NativeVariantID: 42, ListingPresent: false, CapturedAt: capturedAt()})
	desc := findField(t, items, FieldDescription)
	if desc.EvidenceRef != "catalog/variant/42" {
		t.Fatalf("expected variant fallback evidence ref, got %q", desc.EvidenceRef)
	}
}

// Derive is deterministic and complete: exactly title, description, image in a
// stable order.
func TestDeriveStableOrderAndCoverage(t *testing.T) {
	items := Derive(Input{NativeVariantID: 1, VariantTitle: "x", CapturedAt: capturedAt()})
	if len(items) != 3 {
		t.Fatalf("expected 3 diagnostics, got %d", len(items))
	}
	want := []Field{FieldTitle, FieldDescription, FieldImage}
	for i, f := range want {
		if items[i].Field != f {
			t.Fatalf("position %d: expected %q, got %q", i, f, items[i].Field)
		}
	}
}

// REGRESSION (issue #80): a captured, non-empty description is OBSERVED PRESENT and
// PASSES — it must NOT be flattened to not_observed. The observed metadata carries
// the description LENGTH (rune count), never the description text itself.
func TestDeriveDescriptionObservedPresentPasses(t *testing.T) {
	desc := "Boils fast"
	items := Derive(Input{NativeVariantID: 7719004, ListingPresent: true, NativeListingID: 8842213, Description: &desc, CapturedAt: capturedAt()})

	d := findField(t, items, FieldDescription)
	if d.Result != ResultPass {
		t.Fatalf("observed description must pass, got %q", d.Result)
	}
	if d.Observed.State != StatePresent {
		t.Fatalf("expected observed present, got %q", d.Observed.State)
	}
	if d.Observed.CharacterLength == nil || *d.Observed.CharacterLength != 10 {
		t.Fatalf("expected description length 10 metadata, got %v", d.Observed.CharacterLength)
	}
	if d.Entity != EntityListing {
		t.Fatalf("expected entity listing, got %q", d.Entity)
	}
	if d.RuleID != RuleDescriptionPresent || d.RuleVersion != RuleVersionV1 {
		t.Fatalf("description diagnostic must NAME its rule; got %q/%q", d.RuleID, d.RuleVersion)
	}
}

// REGRESSION (issue #80): an OBSERVED-but-empty description is genuinely-absent →
// empty → warn, and is DISTINCT from not_observed. Distinguishing genuinely-absent
// from not-yet-observed is the evidence-quality state model (§4.6).
func TestDeriveDescriptionObservedEmptyWarns(t *testing.T) {
	empty := ""
	items := Derive(Input{NativeVariantID: 1, Description: &empty, CapturedAt: capturedAt()})

	d := findField(t, items, FieldDescription)
	if d.Result != ResultWarn {
		t.Fatalf("observed-empty description must warn, got %q", d.Result)
	}
	if d.Observed.State != StateEmpty {
		t.Fatalf("expected observed empty (genuinely absent), got %q", d.Observed.State)
	}
	if d.Observed.CharacterLength == nil || *d.Observed.CharacterLength != 0 {
		t.Fatalf("expected observed empty length 0 metadata, got %v", d.Observed.CharacterLength)
	}
}

// REGRESSION (issue #80): when the description is NOT observed (nil), the state is
// not_observed → warn — the fail-closed quarantine state, DISTINCT from the
// genuinely-absent empty state, and carrying no fabricated length metadata.
func TestDeriveDescriptionNotObservedWarns(t *testing.T) {
	items := Derive(Input{NativeVariantID: 1, CapturedAt: capturedAt()})

	d := findField(t, items, FieldDescription)
	if d.Result != ResultWarn {
		t.Fatalf("not-observed description must warn, got %q", d.Result)
	}
	if d.Observed.State != StateNotObserved {
		t.Fatalf("expected not_observed, got %q", d.Observed.State)
	}
	if d.Observed.CharacterLength != nil {
		t.Fatalf("not_observed must carry no fabricated length metadata, got %v", d.Observed.CharacterLength)
	}
}

// REGRESSION (issue #80): a captured image count > 0 is OBSERVED PRESENT and PASSES.
// Image is not a text field, so it carries no character-length metadata.
func TestDeriveImageObservedPresentPasses(t *testing.T) {
	count := 3
	items := Derive(Input{NativeVariantID: 7719004, ListingPresent: true, NativeListingID: 8842213, ImageCount: &count, CapturedAt: capturedAt()})

	d := findField(t, items, FieldImage)
	if d.Result != ResultPass {
		t.Fatalf("observed image must pass, got %q", d.Result)
	}
	if d.Observed.State != StatePresent {
		t.Fatalf("expected observed present, got %q", d.Observed.State)
	}
	if d.Observed.CharacterLength != nil {
		t.Fatalf("image is not text; must carry no length metadata, got %v", d.Observed.CharacterLength)
	}
	if d.RuleID != RuleImagePresent || d.RuleVersion != RuleVersionV1 {
		t.Fatalf("image diagnostic must NAME its rule; got %q/%q", d.RuleID, d.RuleVersion)
	}
}

// REGRESSION (issue #80): an OBSERVED image count of 0 is genuinely-absent → empty →
// warn, DISTINCT from not_observed (evidence-quality state model, §4.6).
func TestDeriveImageObservedEmptyWarns(t *testing.T) {
	zero := 0
	items := Derive(Input{NativeVariantID: 1, ImageCount: &zero, CapturedAt: capturedAt()})

	d := findField(t, items, FieldImage)
	if d.Result != ResultWarn {
		t.Fatalf("observed-empty image must warn, got %q", d.Result)
	}
	if d.Observed.State != StateEmpty {
		t.Fatalf("expected observed empty (genuinely absent), got %q", d.Observed.State)
	}
}

// REGRESSION (issue #80): when the image observation is NOT available (nil), the
// state is not_observed → warn — the fail-closed quarantine state, DISTINCT from the
// genuinely-absent empty state.
func TestDeriveImageNotObservedWarns(t *testing.T) {
	items := Derive(Input{NativeVariantID: 1, CapturedAt: capturedAt()})

	d := findField(t, items, FieldImage)
	if d.Result != ResultWarn {
		t.Fatalf("not-observed image must warn, got %q", d.Result)
	}
	if d.Observed.State != StateNotObserved {
		t.Fatalf("expected not_observed, got %q", d.Observed.State)
	}
}

// REGRESSION (issue #80) — the core distinction: genuinely-absent (empty) is NEVER
// collapsed into not-yet-observed. The same field derives DIFFERENT observed states
// from observed-empty input vs unobserved input.
func TestDeriveEmptyIsDistinctFromNotObserved(t *testing.T) {
	empty := ""
	zero := 0
	observedEmpty := Derive(Input{NativeVariantID: 1, Description: &empty, ImageCount: &zero, CapturedAt: capturedAt()})
	unobserved := Derive(Input{NativeVariantID: 1, CapturedAt: capturedAt()})

	for _, field := range []Field{FieldDescription, FieldImage} {
		e := findField(t, observedEmpty, field)
		u := findField(t, unobserved, field)
		if e.Observed.State != StateEmpty {
			t.Fatalf("%s observed-empty must be StateEmpty, got %q", field, e.Observed.State)
		}
		if u.Observed.State != StateNotObserved {
			t.Fatalf("%s unobserved must be StateNotObserved, got %q", field, u.Observed.State)
		}
		if e.Observed.State == u.Observed.State {
			t.Fatalf("%s: genuinely-absent must be DISTINCT from not-observed", field)
		}
	}
}

func findField(t *testing.T, items []Diagnostic, field Field) Diagnostic {
	t.Helper()
	for _, d := range items {
		if d.Field == field {
			return d
		}
	}
	t.Fatalf("no diagnostic for field %q", field)
	return Diagnostic{}
}
