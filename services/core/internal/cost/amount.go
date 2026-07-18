package cost

import (
	"errors"
	"strconv"
	"strings"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/normalize"
)

// Parse-failure reasons for a cost value. They are stable machine codes attached
// to a rejected preview row (CST-001 "every rejected row has a reason") and
// returned by ParseAmount for the single-value entry path.
var (
	// ErrEmptyAmount — the numeric cell was blank after normalization.
	ErrEmptyAmount = errors.New("cost: empty amount")
	// ErrInvalidAmount — the token was not a well-formed non-negative decimal.
	ErrInvalidAmount = errors.New("cost: invalid amount")
	// ErrNegativeAmount — the value was negative; a cost component is never negative.
	ErrNegativeAmount = errors.New("cost: negative amount")
	// ErrTooManyDecimals — the value has more fractional digits than the account's
	// configured exponent can represent WITHOUT rounding. Money is never silently
	// rounded (§9.1), so this is rejected rather than truncated.
	ErrTooManyDecimals = errors.New("cost: more decimal places than currency exponent allows")
)

// Reason returns the stable machine reason code for a parse error, for the
// preview-row disposition. Non-parse errors map to the generic invalid_amount.
func amountReason(err error) string {
	switch {
	case errors.Is(err, ErrEmptyAmount):
		return "empty_amount"
	case errors.Is(err, ErrNegativeAmount):
		return "negative_amount"
	case errors.Is(err, ErrTooManyDecimals):
		return "too_many_decimals"
	default:
		return "invalid_amount"
	}
}

// separatorFold removes grouping separators the seller may type between digits
// and folds the Arabic decimal separator to '.'. Only digit-grouping and decimal
// marks are handled; anything else remaining makes the token invalid downstream.
var separatorReplacer = strings.NewReplacer(
	",", "", // Latin thousands
	"٬", "", // Arabic thousands separator ٬
	"،", "", // Arabic comma ، used as grouping
	" ", "", // spaces / thin spaces used as grouping
	"٫", ".", // Arabic decimal separator ٫ → '.'
	"٪", "", // Arabic percent sign, if present as a stray mark
)

// ParseAmount converts a seller-entered numeric token into an authoritative
// money.Money in the account's configured currency and exponent, using ONLY
// integer arithmetic (no float ever touches a money path, §9.1). Digits are
// folded to ASCII first (LOC-007). A value with more fractional digits than the
// exponent can represent is REJECTED, never rounded.
//
// The returned Money is representable because the currency is known (the account
// is configured); it is still excluded from executable paths until S16+S35.
func ParseAmount(raw, currency string, exponent int8) (money.Money, error) {
	s := normalize.Digits(raw)
	s = strings.TrimSpace(s)
	s = separatorReplacer.Replace(s)
	if s == "" {
		return money.Money{}, ErrEmptyAmount
	}

	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}
	if s == "" {
		return money.Money{}, ErrInvalidAmount
	}

	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if strings.Contains(fracPart, ".") {
		// A second decimal point is malformed.
		return money.Money{}, ErrInvalidAmount
	}
	if !isASCIIDigits(intPart) || !isASCIIDigits(fracPart) {
		return money.Money{}, ErrInvalidAmount
	}
	if intPart == "" && fracPart == "" {
		return money.Money{}, ErrInvalidAmount
	}
	if hasDot && fracPart == "" {
		// A trailing dot with no fraction ("12.") is malformed input.
		return money.Money{}, ErrInvalidAmount
	}

	// targetFrac is the number of fractional digits the exponent represents.
	// exponent is <= 0 for a minor-unit representation (e.g. -2 ⇒ 2 places); a
	// positive or zero exponent represents whole units only.
	targetFrac := 0
	if exponent < 0 {
		targetFrac = int(-exponent)
	}
	if len(fracPart) > targetFrac {
		return money.Money{}, ErrTooManyDecimals
	}
	// Right-pad the fraction to the exponent so the combined digits form the
	// mantissa exactly (string assembly — no float, no rounding).
	padded := fracPart + strings.Repeat("0", targetFrac-len(fracPart))

	digits := intPart + padded
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	mantissa, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return money.Money{}, ErrInvalidAmount
	}
	if neg && mantissa != 0 {
		return money.Money{}, ErrNegativeAmount
	}
	return money.New(mantissa, currency, exponent)
}

// isASCIIDigits reports whether s is empty or contains only ASCII 0-9.
func isASCIIDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
