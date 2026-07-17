package money

// Evidence-side representation of a raw marketplace amount. PRD §9.1 requires
// that "raw marketplace text/value/unit and capture evidence are preserved
// separately" from authoritative Money — it is never conflated with a Money and
// never participates in arithmetic. The consumer of this type is the evidence /
// observation path landing in S13; it is defined here so the money boundary
// owns the separation from day one.
//
// Ambiguity is not resolved here: if the source unit is ambiguous the caller
// quarantines the capture (PRD §9.1) rather than inferring a Money. RawAmount
// deliberately exposes no conversion to Money — promotion to authoritative
// money happens only through the verified, versioned region transform (disabled
// until Gate 0a), which is not part of this package.
type RawAmount struct {
	// Text is the amount exactly as captured from the marketplace, before any
	// normalisation (digits, separators, unit words are all preserved).
	Text string
	// Value is the parsed numeric token as raw source text (not a number type),
	// kept verbatim so no precision or formatting is lost.
	Value string
	// Unit is the source unit token as captured (e.g. a currency word or
	// symbol). It is not interpreted as an ISO-4217 code here.
	Unit string
}

// NewRawAmount preserves a captured marketplace amount verbatim.
func NewRawAmount(text, value, unit string) RawAmount {
	return RawAmount{Text: text, Value: value, Unit: unit}
}

// IsEmpty reports whether nothing was captured.
func (r RawAmount) IsEmpty() bool {
	return r.Text == "" && r.Value == "" && r.Unit == ""
}
