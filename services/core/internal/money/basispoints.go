package money

import "math/big"

// BasisPointScale is the denominator for basis points: 1 basis point = 1/10000.
// PRD §9.1: "Rates and percentages use fixed-point basis points." No float is
// ever used to represent a rate.
const BasisPointScale int64 = 10000

// BasisPoints is a fixed-point rate expressed in ten-thousandths. 100% is
// 10000 bp; 5% (the default movement cap, §9.3) is 500 bp. The value may be
// negative (e.g. a discount delta). The field is private so a rate can only be
// built through NewBasisPoints.
type BasisPoints struct {
	value int64
}

// NewBasisPoints constructs a rate of value ten-thousandths.
func NewBasisPoints(value int64) BasisPoints {
	return BasisPoints{value: value}
}

// PercentPoints constructs a rate from whole percentage points (5 -> 500 bp).
// It returns ErrOverflow if the conversion leaves int64 range.
func PercentPoints(percent int64) (BasisPoints, error) {
	v, err := imul(percent, 100)
	if err != nil {
		return BasisPoints{}, err
	}
	return BasisPoints{value: v}, nil
}

// Value returns the rate in basis points.
func (b BasisPoints) Value() int64 { return b.value }

// ApplyRate multiplies the amount by the basis-point rate, i.e.
// mantissa × bp / 10000, and rounds the exact rational result to an integer
// mantissa using the supplied Rounder. The intermediate product is computed in
// arbitrary precision (math/big) so it cannot overflow or lose precision; only
// the final rounded mantissa is range-checked back into int64 (ErrOverflow
// otherwise). Currency and exponent are preserved unchanged.
//
// This is the single sanctioned way to apply a rate to money — there is no
// float path and no caller-side integer arithmetic.
func (m Money) ApplyRate(bp BasisPoints, round Rounder) (Money, error) {
	if round == nil {
		round = RoundHalfEven
	}
	// exact = mantissa * bp / BasisPointScale, computed as a big rational split
	// into truncated quotient and remainder (remainder carries the dividend's
	// sign, matching round-toward-zero as the baseline).
	num := new(big.Int).Mul(big.NewInt(m.mantissa), big.NewInt(bp.value))
	den := big.NewInt(BasisPointScale)
	quo := new(big.Int)
	rem := new(big.Int)
	quo.QuoRem(num, den, rem)

	if !quo.IsInt64() {
		return Money{}, ErrOverflow
	}
	// den fits int64 and rem is strictly smaller in magnitude, so both are safe.
	adjusted, err := round(quo.Int64(), rem.Int64(), BasisPointScale)
	if err != nil {
		return Money{}, err
	}
	return Money{mantissa: adjusted, currency: m.currency, exponent: m.exponent}, nil
}
