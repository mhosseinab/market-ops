package cost

import (
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/mhosseinab/market-ops/services/core/internal/normalize"
)

// CSV structural errors (CST-001). A malformed file is rejected as a whole
// before any preview row is produced — the seller fixes the file and re-uploads.
var (
	// ErrNotUTF8 — the uploaded bytes are not valid UTF-8.
	ErrNotUTF8 = errors.New("cost: csv is not valid UTF-8")
	// ErrEmptyCSV — the file has no header row.
	ErrEmptyCSV = errors.New("cost: csv is empty")
	// ErrNoSKUColumn — no column maps to the SKU (required, design §5).
	ErrNoSKUColumn = errors.New("cost: csv has no SKU column")
	// ErrNoComponentColumn — no column maps to a known cost component.
	ErrNoComponentColumn = errors.New("cost: csv has no cost-component column")
	// ErrMalformedCSV — the CSV could not be parsed (ragged rows, bad quoting).
	ErrMalformedCSV = errors.New("cost: malformed csv")
)

// skuHeaderAliases are the header tokens (lower-cased) that identify the SKU
// column. Technical identifiers only; locale-neutral.
var skuHeaderAliases = map[string]bool{
	"sku":           true,
	"supplier_code": true,
	"supplier code": true,
}

// Mapping is an optional explicit column→role mapping. When a field is empty the
// parser auto-detects it from the header row. Explicit mapping supports the
// design's "mapping preview" where the user corrects a mis-detected column.
type Mapping struct {
	// SKUColumn is the header of the SKU column; empty ⇒ auto-detect.
	SKUColumn string
	// Components maps a header to the component it supplies; empty ⇒ auto-detect
	// columns whose header equals a component name.
	Components map[string]Component
}

// DetectedColumn records how one header column was interpreted, for the preview
// echo so the seller can confirm the mapping.
type DetectedColumn struct {
	Header    string
	Component Component
}

// DetectedMapping is the resolved column interpretation returned alongside the
// parsed rows.
type DetectedMapping struct {
	SKUColumn        string
	ComponentColumns []DetectedColumn
}

// ParsedEntry is one (file line, cost component) value from the CSV, before SKU
// resolution and money parsing (which need account context). RawSKU and RawValue
// are verbatim; Normalized is the digit-folded numeric token (LOC-007).
type ParsedEntry struct {
	RowNumber  int
	RawSKU     string
	Component  Component
	RawValue   string
	Normalized string
}

// ParseCSV parses a UTF-8 cost CSV into per-(line, component) entries plus the
// detected column mapping. It performs NO SKU resolution, money parsing, or
// disposition assignment — those are the service's job with account context. A
// structurally invalid file is rejected as a whole (no partial preview).
func ParseCSV(content string, m Mapping) ([]ParsedEntry, DetectedMapping, error) {
	if !utf8.ValidString(content) {
		return nil, DetectedMapping{}, ErrNotUTF8
	}
	r := csv.NewReader(strings.NewReader(content))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1 // tolerate ragged rows; we index defensively

	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, DetectedMapping{}, ErrEmptyCSV
	}
	if err != nil {
		return nil, DetectedMapping{}, ErrMalformedCSV
	}

	skuIdx, compCols, detected, err := resolveColumns(header, m)
	if err != nil {
		return nil, DetectedMapping{}, err
	}

	var entries []ParsedEntry
	line := 0
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, DetectedMapping{}, ErrMalformedCSV
		}
		if isBlankRecord(rec) {
			continue
		}
		line++
		rawSKU := ""
		if skuIdx < len(rec) {
			rawSKU = strings.TrimSpace(rec[skuIdx])
		}
		for _, cc := range compCols {
			if cc.idx >= len(rec) {
				continue
			}
			cell := strings.TrimSpace(rec[cc.idx])
			if cell == "" {
				continue // no value supplied for this component on this line
			}
			entries = append(entries, ParsedEntry{
				RowNumber:  line,
				RawSKU:     rawSKU,
				Component:  cc.component,
				RawValue:   cell,
				Normalized: strings.TrimSpace(normalize.Digits(cell)),
			})
		}
	}
	return entries, detected, nil
}

type componentColumn struct {
	idx       int
	component Component
}

// resolveColumns maps header columns to the SKU column and component columns,
// honouring an explicit Mapping and falling back to header-name auto-detection.
func resolveColumns(header []string, m Mapping) (int, []componentColumn, DetectedMapping, error) {
	skuIdx := -1
	skuHeader := strings.ToLower(strings.TrimSpace(m.SKUColumn))
	var compCols []componentColumn
	detected := DetectedMapping{}

	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		// SKU column: explicit mapping wins, else alias auto-detect.
		if skuIdx == -1 {
			if skuHeader != "" && key == skuHeader {
				skuIdx = i
				detected.SKUColumn = h
				continue
			}
			if skuHeader == "" && skuHeaderAliases[key] {
				skuIdx = i
				detected.SKUColumn = h
				continue
			}
		}
		// Component column: explicit mapping wins, else header==component name.
		if comp, ok := m.Components[h]; ok && comp.Valid() {
			compCols = append(compCols, componentColumn{idx: i, component: comp})
			detected.ComponentColumns = append(detected.ComponentColumns, DetectedColumn{Header: h, Component: comp})
			continue
		}
		if len(m.Components) == 0 {
			if comp, ok := ParseComponent(key); ok {
				compCols = append(compCols, componentColumn{idx: i, component: comp})
				detected.ComponentColumns = append(detected.ComponentColumns, DetectedColumn{Header: h, Component: comp})
			}
		}
	}

	if skuIdx == -1 {
		return -1, nil, DetectedMapping{}, ErrNoSKUColumn
	}
	if len(compCols) == 0 {
		return -1, nil, DetectedMapping{}, ErrNoComponentColumn
	}
	return skuIdx, compCols, detected, nil
}

// isBlankRecord reports whether every field in a CSV record is empty/whitespace.
func isBlankRecord(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}
