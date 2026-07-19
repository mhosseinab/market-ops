package cost

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"testing/quick"
)

var persianDigits = []rune{'۰', '۱', '۲', '۳', '۴', '۵', '۶', '۷', '۸', '۹'}

func toPersian(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(persianDigits[r-'0'])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestParseAmount_PersianEqualsLatin is the LOC-007 money-path property: a cost
// value entered in Persian digits parses to the SAME Money as the Latin form.
func TestParseAmount_PersianEqualsLatin(t *testing.T) {
	prop := func(n uint32) bool {
		latin := strconv.FormatUint(uint64(n), 10)
		a, err1 := ParseAmount(latin, "IRR", 0)
		b, err2 := ParseAmount(toPersian(latin), "IRR", 0)
		if err1 != nil || err2 != nil {
			return false
		}
		return a.Mantissa() == b.Mantissa() && a.Currency() == b.Currency() && a.Exponent() == b.Exponent()
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 2000}); err != nil {
		t.Fatalf("persian==latin money property failed: %v", err)
	}
}

func TestParseAmount_Exponents(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		currency string
		exp      int8
		mantissa int64
	}{
		{"integer IRR", "1500000", "IRR", 0, 1500000},
		{"thousands separators", "1,500,000", "IRR", 0, 1500000},
		{"persian thousands sep", "۱٬۵۰۰٬۰۰۰", "IRR", 0, 1500000},
		{"two-decimal USD", "12.34", "USD", -2, 1234},
		{"pad fraction to exponent", "12.3", "USD", -2, 1230},
		{"whole into minor units", "12", "USD", -2, 1200},
		{"arabic decimal separator", "12٫34", "USD", -2, 1234},
		{"zero", "0", "IRR", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ParseAmount(tt.raw, tt.currency, tt.exp)
			if err != nil {
				t.Fatalf("ParseAmount(%q) unexpected error: %v", tt.raw, err)
			}
			if m.Mantissa() != tt.mantissa {
				t.Errorf("mantissa = %d, want %d", m.Mantissa(), tt.mantissa)
			}
			if m.Exponent() != tt.exp || m.Currency() != tt.currency {
				t.Errorf("got %s, want currency %s exp %d", m.String(), tt.currency, tt.exp)
			}
		})
	}
}

func TestParseAmount_Rejections(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		exp  int8
		want error
	}{
		{"empty", "", 0, ErrEmptyAmount},
		{"blank whitespace", "   ", 0, ErrEmptyAmount},
		{"letters", "12abc", 0, ErrInvalidAmount},
		{"negative", "-5", 0, ErrNegativeAmount},
		{"too many decimals for exponent", "12.34", 0, ErrTooManyDecimals},
		{"more decimals than USD exponent", "12.345", -2, ErrTooManyDecimals},
		{"double dot", "1.2.3", -2, ErrInvalidAmount},
		{"trailing dot", "12.", -2, ErrInvalidAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseAmount(tt.raw, "IRR", tt.exp)
			if !errors.Is(err, tt.want) {
				t.Errorf("ParseAmount(%q) err = %v, want %v", tt.raw, err, tt.want)
			}
		})
	}
}

// TestParseAmount_PercentRejected is the #40 regression: a percent-bearing token
// is NOT Money and must be rejected with a stable percent reason code — never
// stripped and parsed as the currency amount (§9.1: percentages are basis points
// on a distinct path, never coerced into Money). The percent characters covered
// are ASCII %, Arabic ٪ (U+066A), and fullwidth ％ (U+FF05), in Persian, Arabic,
// and Latin digit families.
func TestParseAmount_PercentRejected(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		exp  int8
	}{
		{"persian digit arabic percent", "۱۰٪", 0}, // the exact issue reproduction
		{"latin ascii percent", "10%", 0},          // 10%
		{"latin arabic percent", "10٪", 0},         // 10٪
		{"latin fullwidth percent", "10％", 0},      // 10％
		{"arabic digit arabic percent", "٠٥٪", 0},  // Arabic-Indic digits + ٪
		{"percent with decimals", "12.5%", -2},     // percent on a minor-unit currency
		{"leading percent", "%10", 0},              // percent anywhere in the token
		{"persian fullwidth percent", "۲۵％", 0},    // Persian digits + fullwidth
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseAmount(tt.raw, "IRR", tt.exp)
			if !errors.Is(err, ErrPercentNotMoney) {
				t.Errorf("ParseAmount(%q) err = %v, want ErrPercentNotMoney", tt.raw, err)
			}
			if got := amountReason(err); got != "percent_not_money" {
				t.Errorf("amountReason for %q = %q, want percent_not_money", tt.raw, got)
			}
		})
	}
}

func TestParseAmount_NegativeZeroIsZero(t *testing.T) {
	// "-0" is not a meaningful negative; it normalizes to zero, not a rejection.
	m, err := ParseAmount("-0", "IRR", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Mantissa() != 0 {
		t.Errorf("mantissa = %d, want 0", m.Mantissa())
	}
}
