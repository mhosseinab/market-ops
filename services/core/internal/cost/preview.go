package cost

import "github.com/google/uuid"

// Disposition is a preview-row outcome (CST-001, §16). The set is closed and
// matches the cost_import_rows CHECK constraint.
type Disposition string

const (
	// DispositionAccept — the row resolved to exactly one variant and parsed to a
	// valid Money; it will become a cost_profile version on commit.
	DispositionAccept Disposition = "accept"
	// DispositionReject — the row cannot commit; reason names why (CST-001).
	DispositionReject Disposition = "reject"
	// DispositionDuplicate — the row conflicts with another row for the same
	// (SKU, component) within the file (§16). A batch with any duplicate cannot
	// commit until resolved.
	DispositionDuplicate Disposition = "duplicate"
)

// ResolvedSKU is the outcome of resolving one raw SKU token to variants within
// the account: the matched variant (when exactly one) and the match count.
type ResolvedSKU struct {
	VariantID uuid.UUID
	Count     int
}

// PreviewRow is a fully-decided preview row: raw evidence, resolution, parsed
// Money (when acceptable), and the disposition + reason.
type PreviewRow struct {
	RowNumber  int
	RawSKU     string
	Component  Component
	RawValue   string
	Normalized string
	RawUnit    string

	VariantID  uuid.UUID
	HasVariant bool

	Mantissa  int64
	Currency  string
	Exponent  int8
	HasAmount bool

	Disposition Disposition
	Reason      string
}

// Counts is the disposition tally backing the preview cards and the commit guard
// (a batch with Duplicate > 0 cannot commit).
type Counts struct {
	Accept    int
	Reject    int
	Duplicate int
}

// BuildPreviewRows assigns a disposition to every parsed entry (pure). Duplicate
// detection runs first and takes precedence (§16): every row in a (SKU,
// component) group of size > 1 is a Duplicate conflict. Otherwise the row is
// resolved (unknown/ambiguous SKU ⇒ reject with a reason) and its value parsed
// (invalid/negative/over-precise amount ⇒ reject with a reason). Every non-accept
// row carries a stated reason (CST-001).
func BuildPreviewRows(entries []ParsedEntry, resolved map[string]ResolvedSKU, currency string, exponent int8) ([]PreviewRow, Counts) {
	dupCount := make(map[string]int, len(entries))
	for _, e := range entries {
		dupCount[dupKey(e.RawSKU, e.Component)]++
	}

	rows := make([]PreviewRow, 0, len(entries))
	var counts Counts
	for _, e := range entries {
		row := PreviewRow{
			RowNumber:  e.RowNumber,
			RawSKU:     e.RawSKU,
			Component:  e.Component,
			RawValue:   e.RawValue,
			Normalized: e.Normalized,
		}

		switch {
		case dupCount[dupKey(e.RawSKU, e.Component)] > 1:
			row.Disposition = DispositionDuplicate
			row.Reason = "duplicate_in_file"
			counts.Duplicate++
		default:
			res := resolved[e.RawSKU]
			switch {
			case res.Count == 0:
				row.Disposition = DispositionReject
				row.Reason = "sku_not_found"
				counts.Reject++
			case res.Count > 1:
				row.Disposition = DispositionReject
				row.Reason = "ambiguous_sku"
				counts.Reject++
			default:
				row.VariantID = res.VariantID
				row.HasVariant = true
				m, err := ParseAmount(e.Normalized, currency, exponent)
				if err != nil {
					row.Disposition = DispositionReject
					row.Reason = amountReason(err)
					counts.Reject++
				} else {
					row.Mantissa = m.Mantissa()
					row.Currency = m.Currency()
					row.Exponent = m.Exponent()
					row.HasAmount = true
					row.Disposition = DispositionAccept
					counts.Accept++
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, counts
}

// dupKey is the duplicate-detection key: raw SKU token + component. Using the raw
// SKU (before resolution) means duplicate conflicts are caught even when the SKU
// itself does not resolve.
func dupKey(rawSKU string, c Component) string {
	return rawSKU + "\x00" + string(c)
}
