package cost

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestParseCSV_AutoDetectAndNormalize(t *testing.T) {
	content := "sku,cogs,commission\nABC,۱۲۳۴,۵۰\nDEF,999,\n"
	entries, detected, err := ParseCSV(content, Mapping{})
	if err != nil {
		t.Fatalf("ParseCSV error: %v", err)
	}
	if detected.SKUColumn != "sku" {
		t.Errorf("detected sku column = %q", detected.SKUColumn)
	}
	if len(detected.ComponentColumns) != 2 {
		t.Fatalf("detected component columns = %d, want 2", len(detected.ComponentColumns))
	}
	// ABC has cogs+commission, DEF has cogs only (empty commission skipped) => 3.
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	// First entry cogs normalized from Persian digits.
	if entries[0].Normalized != "1234" {
		t.Errorf("normalized = %q, want 1234", entries[0].Normalized)
	}
	if entries[0].RawValue != "۱۲۳۴" {
		t.Errorf("raw value not preserved verbatim: %q", entries[0].RawValue)
	}
}

func TestParseCSV_StructuralErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    error
	}{
		{"empty file", "", ErrEmptyCSV},
		{"no sku column", "cogs,commission\n10,20\n", ErrNoSKUColumn},
		{"no component column", "sku,color\nABC,red\n", ErrNoComponentColumn},
		{"invalid utf8", "sku,cogs\n\xff\xfe,10\n", ErrNotUTF8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParseCSV(tt.content, Mapping{})
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestBuildPreviewRows_DispositionsAndReasons(t *testing.T) {
	v1 := uuid.New()
	entries := []ParsedEntry{
		{RowNumber: 1, RawSKU: "OK", Component: ComponentCOGS, Normalized: "100"},
		{RowNumber: 2, RawSKU: "MISS", Component: ComponentCOGS, Normalized: "100"},
		{RowNumber: 3, RawSKU: "AMB", Component: ComponentCOGS, Normalized: "100"},
		{RowNumber: 4, RawSKU: "OK", Component: ComponentCommission, Normalized: "abc"},
		// duplicate (SKU, component) group of two:
		{RowNumber: 5, RawSKU: "DUP", Component: ComponentCOGS, Normalized: "1"},
		{RowNumber: 6, RawSKU: "DUP", Component: ComponentCOGS, Normalized: "2"},
	}
	resolved := map[string]ResolvedSKU{
		"OK":   {VariantID: v1, Count: 1},
		"MISS": {Count: 0},
		"AMB":  {Count: 2},
		"DUP":  {VariantID: uuid.New(), Count: 1},
	}
	rows, counts := BuildPreviewRows(entries, resolved, "IRR", 0)

	byRow := map[int]PreviewRow{}
	for _, r := range rows {
		byRow[r.RowNumber] = r
	}

	assertRow := func(n int, disp Disposition, reason string) {
		r := byRow[n]
		if r.Disposition != disp {
			t.Errorf("row %d disposition = %q, want %q", n, r.Disposition, disp)
		}
		// CST-001: every non-accept row carries a reason.
		if disp != DispositionAccept && r.Reason == "" {
			t.Errorf("row %d is %q but has no reason", n, disp)
		}
		if reason != "" && r.Reason != reason {
			t.Errorf("row %d reason = %q, want %q", n, r.Reason, reason)
		}
	}

	assertRow(1, DispositionAccept, "")
	assertRow(2, DispositionReject, "sku_not_found")
	assertRow(3, DispositionReject, "ambiguous_sku")
	assertRow(4, DispositionReject, "invalid_amount")
	assertRow(5, DispositionDuplicate, "duplicate_in_file")
	assertRow(6, DispositionDuplicate, "duplicate_in_file")

	if counts.Accept != 1 || counts.Reject != 3 || counts.Duplicate != 2 {
		t.Errorf("counts = %+v, want accept 1 reject 3 duplicate 2", counts)
	}
	// Accepted row carries the parsed amount.
	if r := byRow[1]; !r.HasAmount || r.Mantissa != 100 || !r.HasVariant {
		t.Errorf("accepted row missing amount/variant: %+v", r)
	}
}

// TestBuildPreviewRows_PercentRejected proves the #40 fix reaches the CSV preview
// seam: a percent-bearing cost cell rejects with the stable percent_not_money
// reason instead of silently stripping the sign and committing the value as Money.
func TestBuildPreviewRows_PercentRejected(t *testing.T) {
	v1 := uuid.New()
	entries := []ParsedEntry{
		// Normalized mirrors csv.go: digit-folded (LOC-007) but the percent sign
		// is preserved verbatim, so ۱۰٪ folds to "10٪".
		{RowNumber: 1, RawSKU: "OK", Component: ComponentCOGS, Normalized: "10٪"},
		{RowNumber: 2, RawSKU: "OK", Component: ComponentCommission, Normalized: "10%"},
	}
	resolved := map[string]ResolvedSKU{"OK": {VariantID: v1, Count: 1}}
	rows, counts := BuildPreviewRows(entries, resolved, "IRR", 0)
	for _, r := range rows {
		if r.Disposition != DispositionReject {
			t.Errorf("row %d disposition = %q, want reject", r.RowNumber, r.Disposition)
		}
		if r.Reason != "percent_not_money" {
			t.Errorf("row %d reason = %q, want percent_not_money", r.RowNumber, r.Reason)
		}
		if r.HasAmount {
			t.Errorf("row %d must not carry a parsed amount", r.RowNumber)
		}
	}
	if counts.Accept != 0 || counts.Reject != 2 {
		t.Errorf("counts = %+v, want accept 0 reject 2", counts)
	}
}

func TestBuildPreviewRows_EveryNonAcceptHasReason(t *testing.T) {
	// Invariant sweep: no matter the outcome, a non-accept row always has a reason.
	entries := []ParsedEntry{
		{RowNumber: 1, RawSKU: "X", Component: ComponentCOGS, Normalized: "-5"},
	}
	rows, _ := BuildPreviewRows(entries, map[string]ResolvedSKU{"X": {VariantID: uuid.New(), Count: 1}}, "IRR", 0)
	if rows[0].Disposition == DispositionAccept {
		t.Fatal("negative value should not accept")
	}
	if rows[0].Reason == "" {
		t.Fatal("non-accept row must carry a reason (CST-001)")
	}
	if rows[0].Reason != "negative_amount" {
		t.Errorf("reason = %q, want negative_amount", rows[0].Reason)
	}
}
