package money

// Rounding rules for converting an exact rational (from applying a fixed-point
// rate) into an integer mantissa. PRD §9.1 forbids float on any money path, so
// rounding is defined purely over the truncated quotient and its remainder.
//
// A Rounder receives, for an exact numerator N and positive divisor D:
//
//	quotient  = trunc(N / D)      (rounded toward zero)
//	remainder = N - quotient*D    (same sign as N, |remainder| < D)
//	divisor   = D                 (> 0)
//
// and returns the final integer mantissa. Every Rounder routes its own integer
// math through the checked primitives in checked.go — no raw operators here.
//
// The named rounding functions are the explicit, selectable rounding rules the
// step requires; there is deliberately no implicit default at call sites other
// than banker's rounding, which callers get by passing RoundHalfEven.
type Rounder func(quotient, remainder, divisor int64) (int64, error)

// awayFromZero moves quotient one step in the direction of the fractional part,
// whose sign equals the sign of remainder.
func awayFromZero(quotient, remainder int64) (int64, error) {
	switch icmp(remainder, 0) {
	case 1:
		return iadd(quotient, 1)
	case -1:
		return isub(quotient, 1)
	default:
		return quotient, nil
	}
}

// RoundDown truncates toward zero (drops the fractional part).
func RoundDown(quotient, remainder, divisor int64) (int64, error) {
	return quotient, nil
}

// RoundUp rounds away from zero whenever there is any fractional part.
func RoundUp(quotient, remainder, divisor int64) (int64, error) {
	return awayFromZero(quotient, remainder)
}

// RoundFloor rounds toward negative infinity.
func RoundFloor(quotient, remainder, divisor int64) (int64, error) {
	if icmp(remainder, 0) < 0 {
		return isub(quotient, 1)
	}
	return quotient, nil
}

// RoundCeil rounds toward positive infinity.
func RoundCeil(quotient, remainder, divisor int64) (int64, error) {
	if icmp(remainder, 0) > 0 {
		return iadd(quotient, 1)
	}
	return quotient, nil
}

// RoundHalfUp rounds to nearest; exact halves round away from zero.
func RoundHalfUp(quotient, remainder, divisor int64) (int64, error) {
	cmp, err := compareTwiceRemToDivisor(remainder, divisor)
	if err != nil {
		return 0, err
	}
	if cmp >= 0 {
		return awayFromZero(quotient, remainder)
	}
	return quotient, nil
}

// RoundHalfEven rounds to nearest; exact halves round to the even neighbour
// (banker's rounding). This is the default statisticians'-rounding rule used
// when a caller does not select another.
func RoundHalfEven(quotient, remainder, divisor int64) (int64, error) {
	cmp, err := compareTwiceRemToDivisor(remainder, divisor)
	if err != nil {
		return 0, err
	}
	switch {
	case cmp > 0:
		return awayFromZero(quotient, remainder)
	case cmp < 0:
		return quotient, nil
	default:
		// Exact half: keep quotient if it is already even, else step away.
		if isEven(quotient) {
			return quotient, nil
		}
		return awayFromZero(quotient, remainder)
	}
}

// compareTwiceRemToDivisor compares 2·|remainder| with divisor, returning -1, 0
// or +1. It is the shared nearest-neighbour test for the half-* rounders.
func compareTwiceRemToDivisor(remainder, divisor int64) (int, error) {
	absRem, err := iabs(remainder)
	if err != nil {
		return 0, err
	}
	twice, err := imul(absRem, 2)
	if err != nil {
		return 0, err
	}
	return icmp(twice, divisor), nil
}
