package money

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

// TestCanonicalConformanceFixtures proves the Go core and the TS formatter
// (packages/locale) agree on the never-cut MONEY CORRECTNESS invariant
// (PRD §4.6 / §9.1): Value = mantissa × 10^exponent, for positive, zero, and
// negative exponents, large mantissas, negatives, and the zero-pad boundary.
//
// It reads the SAME shared fixture the TS test imports and, using math/big
// (no float64), asserts that mantissa × 10^exponent equals each fixture's
// locale-independent `rawDecimal`. Regression test for issue #17.
func TestCanonicalConformanceFixtures(t *testing.T) {
	// services/core/internal/money -> repo root is four levels up.
	path := filepath.Join("..", "..", "..", "..", "packages", "locale", "src", "format", "money.fixtures.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared fixture %s: %v", path, err)
	}

	var doc struct {
		Vectors []struct {
			Name       string `json:"name"`
			Mantissa   string `json:"mantissa"`
			Currency   string `json:"currency"`
			Exponent   int    `json:"exponent"`
			RawDecimal string `json:"rawDecimal"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse shared fixture: %v", err)
	}
	if len(doc.Vectors) == 0 {
		t.Fatal("shared fixture contains no vectors")
	}

	for _, v := range doc.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			mantissa, ok := new(big.Int).SetString(v.Mantissa, 10)
			if !ok {
				t.Fatalf("mantissa %q is not a base-10 integer", v.Mantissa)
			}
			if got := canonicalDecimal(mantissa, v.Exponent); got != v.RawDecimal {
				t.Errorf("mantissa %s × 10^%d = %q, want rawDecimal %q",
					v.Mantissa, v.Exponent, got, v.RawDecimal)
			}
		})
	}
}

// canonicalDecimal renders mantissa × 10^exponent as an exact decimal string
// with ASCII '-' and '.', using integer/string arithmetic only (no float64).
// It mirrors the TS `scale()` algorithm so both implementations are provably
// the same canonical value.
func canonicalDecimal(mantissa *big.Int, exponent int) string {
	neg := mantissa.Sign() < 0
	abs := new(big.Int).Abs(mantissa)

	sign := ""
	if neg && abs.Sign() != 0 {
		sign = "-"
	}

	if exponent > 0 {
		scaled := new(big.Int).Mul(abs, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil))
		return sign + scaled.String()
	}
	if exponent == 0 {
		return sign + abs.String()
	}

	places := -exponent
	s := abs.String()
	for len(s) < places+1 {
		s = "0" + s
	}
	cut := len(s) - places
	return sign + s[:cut] + "." + s[cut:]
}
