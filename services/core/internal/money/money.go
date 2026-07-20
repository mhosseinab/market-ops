// Package money implements the authoritative money representation for the DK
// Marketplace Intelligence core (PRD §9.1).
//
//	Money { mantissa int64, currency ISO-4217 code, exponent int8 }
//	Value = mantissa × 10^exponent currency units.
//
// Invariants enforced here (never-cut, PRD §4.6 / §9.1):
//   - Fields are private; arithmetic is available only through Money methods.
//   - No floating-point value enters a money path (there is no float in this
//     package; the static guard forbids it).
//   - Addition, comparison, and netting reject different currencies or
//     incompatible exponents at runtime — this is a runtime property test
//     target, not a Go compile-time guarantee.
//   - Cross-currency conversion does not exist in P0.
//   - Raw marketplace text/value/unit is preserved separately (see Evidence),
//     never conflated with an authoritative Money.
//
// The package imports no other internal package (money isolation, enforced by
// the depguard `money-isolation` rule).
package money

import (
	"strconv"
	"strings"
)

// Money is an authoritative monetary amount. The zero value is not a valid
// Money (it has an empty currency); construct with New or Zero.
type Money struct {
	mantissa int64
	currency string
	exponent int8
}

// New constructs a Money, validating that currency is a recognised active
// ISO-4217 code. It returns ErrInvalidCurrency otherwise.
func New(mantissa int64, currency string, exponent int8) (Money, error) {
	if !ValidCurrency(currency) {
		return Money{}, ErrInvalidCurrency
	}
	return Money{mantissa: mantissa, currency: currency, exponent: exponent}, nil
}

// Zero returns a zero-valued Money in the given currency and exponent.
func Zero(currency string, exponent int8) (Money, error) {
	return New(0, currency, exponent)
}

// Mantissa returns the integer mantissa. The value equals mantissa × 10^exponent
// currency units.
func (m Money) Mantissa() int64 { return m.mantissa }

// Currency returns the ISO-4217 currency code.
func (m Money) Currency() string { return m.currency }

// Exponent returns the base-10 exponent applied to the mantissa.
func (m Money) Exponent() int8 { return m.exponent }

// IsZero reports whether the mantissa is zero.
func (m Money) IsZero() bool { return m.mantissa == 0 }

// invalidMoneyMarker is the canonical String() rendering of an invalid Money. It
// is deliberately NOT a parseable "CURRENCY:mantissa:exponent" encoding and
// announces invalidity explicitly, so a downstream reader can never mistake a
// missing/uninitialised amount for a real numeric zero (PRD §9.1).
const invalidMoneyMarker = "money(invalid)"

// valid reports whether m is an authoritative value — i.e. constructed through
// New/Zero and therefore carrying a recognised ISO-4217 currency. The Go zero
// value (empty currency) is invalid: a missing amount, not a real zero.
func (m Money) valid() bool { return ValidCurrency(m.currency) }

// compatible reports whether o may be combined with m. An invalid receiver or
// operand (the Go zero value, or any value not built via New/Zero) is rejected
// FIRST with the actionable ErrInvalidMoney — before the currency/exponent
// checks — so an uninitialised operand names its own fault rather than surfacing
// as a misleading currency mismatch. Otherwise it requires the same currency and
// exponent, returning the typed error identifying which invariant was violated.
func (m Money) compatible(o Money) error {
	if !m.valid() || !o.valid() {
		return ErrInvalidMoney
	}
	if m.currency != o.currency {
		return ErrCurrencyMismatch
	}
	if m.exponent != o.exponent {
		return ErrExponentMismatch
	}
	return nil
}

// Add returns m+o. It rejects operands of a different currency
// (ErrCurrencyMismatch) or a different exponent (ErrExponentMismatch), and
// ErrOverflow if the sum leaves int64 range.
func (m Money) Add(o Money) (Money, error) {
	if err := m.compatible(o); err != nil {
		return Money{}, err
	}
	sum, err := iadd(m.mantissa, o.mantissa)
	if err != nil {
		return Money{}, err
	}
	return Money{mantissa: sum, currency: m.currency, exponent: m.exponent}, nil
}

// Sub returns m-o under the same compatibility and overflow rules as Add.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.compatible(o); err != nil {
		return Money{}, err
	}
	diff, err := isub(m.mantissa, o.mantissa)
	if err != nil {
		return Money{}, err
	}
	return Money{mantissa: diff, currency: m.currency, exponent: m.exponent}, nil
}

// Neg returns -m. It rejects an invalid receiver with ErrInvalidMoney, and
// returns ErrOverflow only for the single unrepresentable case
// (mantissa == math.MinInt64).
func (m Money) Neg() (Money, error) {
	if !m.valid() {
		return Money{}, ErrInvalidMoney
	}
	n, err := ineg(m.mantissa)
	if err != nil {
		return Money{}, err
	}
	return Money{mantissa: n, currency: m.currency, exponent: m.exponent}, nil
}

// Compare reports whether m is less than (-1), equal to (0), or greater than
// (+1) o. It rejects incompatible operands with the same typed errors as Add.
func (m Money) Compare(o Money) (int, error) {
	if err := m.compatible(o); err != nil {
		return 0, err
	}
	return icmp(m.mantissa, o.mantissa), nil
}

// Equal reports whether m and o are compatible and numerically equal. Any
// incompatibility (currency or exponent) makes them not equal and returns the
// reason; callers that only care about the boolean can ignore the error.
func (m Money) Equal(o Money) (bool, error) {
	if err := m.compatible(o); err != nil {
		return false, err
	}
	return icmp(m.mantissa, o.mantissa) == 0, nil
}

// Net sums a slice of Money values. It rejects the empty slice caller-side by
// requiring at least the first element to seed currency/exponent; every
// subsequent element must be compatible. This is the netting operation the PRD
// calls out as subject to the same currency/exponent rejection as Add.
func Net(values ...Money) (Money, error) {
	if len(values) == 0 {
		return Money{}, ErrMalformed
	}
	acc := values[0]
	if !acc.valid() {
		return Money{}, ErrInvalidMoney
	}
	for _, v := range values[1:] {
		next, err := acc.Add(v)
		if err != nil {
			return Money{}, err
		}
		acc = next
	}
	return acc, nil
}

// MarshalText encodes Money as "CURRENCY:mantissa:exponent" (e.g. "IRR:12345:-2").
// The encoding is exact and lossless — no float is involved. It rejects an
// invalid (uninitialised) receiver with ErrInvalidMoney rather than emitting a
// currency-less encoding a decoder would treat as a real amount. It implements
// encoding.TextMarshaler.
func (m Money) MarshalText() ([]byte, error) {
	if !m.valid() {
		return nil, ErrInvalidMoney
	}
	var b strings.Builder
	b.WriteString(m.currency)
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(m.mantissa, 10))
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(int64(m.exponent), 10))
	return []byte(b.String()), nil
}

// String returns the canonical text encoding (see MarshalText). String cannot
// return an error, so an invalid (uninitialised) receiver renders the explicit
// invalidMoneyMarker — never an empty string or a "0"-like encoding that a
// reader could mistake for a real amount.
func (m Money) String() string {
	t, err := m.MarshalText()
	if err != nil {
		return invalidMoneyMarker
	}
	return string(t)
}

// UnmarshalText decodes the encoding produced by MarshalText, validating the
// currency code and exponent range. It implements encoding.TextUnmarshaler.
func (m *Money) UnmarshalText(text []byte) error {
	parts := strings.Split(string(text), ":")
	if len(parts) != 3 {
		return ErrMalformed
	}
	currency := parts[0]
	if !ValidCurrency(currency) {
		return ErrInvalidCurrency
	}
	mantissa, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ErrMalformed
	}
	exp, err := strconv.ParseInt(parts[2], 10, 8)
	if err != nil {
		return ErrMalformed
	}
	m.mantissa = mantissa
	m.currency = currency
	m.exponent = int8(exp)
	return nil
}

// Decode is the value-returning counterpart to UnmarshalText.
func Decode(text string) (Money, error) {
	var m Money
	if err := m.UnmarshalText([]byte(text)); err != nil {
		return Money{}, err
	}
	return m, nil
}
