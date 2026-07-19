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

// NEGATIVE (fail-closed): description and image are fields the connector does not
// yet surface. They MUST always report not_observed → warn and can NEVER become a
// fabricated pass, even when every input field is fully populated. This is the
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
