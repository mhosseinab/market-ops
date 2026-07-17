package money

import "errors"

// Typed, comparable errors for the money domain. PRD §9.1 requires that
// addition, comparison, and netting reject different currencies or incompatible
// exponents; these sentinels make that rejection assertable at runtime (the PRD
// is explicit that this is a runtime property, not a Go compile-time guarantee).
var (
	// ErrCurrencyMismatch is returned when a binary Money operation is given
	// operands whose currency codes differ. Cross-currency conversion does not
	// exist in P0 (PRD §9.1).
	ErrCurrencyMismatch = errors.New("money: currency mismatch")

	// ErrExponentMismatch is returned when a binary Money operation is given
	// operands whose exponents differ (incompatible scale). Callers must align
	// scale explicitly before combining values.
	ErrExponentMismatch = errors.New("money: exponent mismatch")

	// ErrInvalidCurrency is returned by constructors when the currency code is
	// not a recognised active ISO-4217 alphabetic code.
	ErrInvalidCurrency = errors.New("money: invalid ISO-4217 currency code")

	// ErrOverflow is returned when an integer operation would exceed the range
	// of int64. No money operation silently wraps.
	ErrOverflow = errors.New("money: int64 overflow")

	// ErrMalformed is returned when decoding a Money from its text encoding
	// fails structurally.
	ErrMalformed = errors.New("money: malformed encoding")
)
