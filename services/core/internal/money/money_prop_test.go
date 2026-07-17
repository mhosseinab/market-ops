package money

import (
	"errors"
	"testing"

	"pgregory.net/rapid"
)

// PRD §9.1 is explicit that currency/exponent rejection is a *runtime* property
// (not a Go compile-time guarantee). These property tests are that verification.

var propCurrencies = []string{"USD", "EUR", "IRR", "JPY", "GBP"}

// genMoney draws an arbitrary valid Money. mantissa is bounded well inside int64
// so that pairwise sums used by the associativity property cannot overflow; the
// overflow path is covered separately by example tests.
func genMoney(t *rapid.T) Money {
	mantissa := rapid.Int64Range(-(1<<40), 1<<40).Draw(t, "mantissa")
	currency := rapid.SampledFrom(propCurrencies).Draw(t, "currency")
	exponent := int8(rapid.IntRange(-6, 6).Draw(t, "exponent"))
	return Money{mantissa: mantissa, currency: currency, exponent: exponent}
}

// genCompatible draws a Money sharing c's currency and exponent.
func genCompatible(t *rapid.T, c Money) Money {
	mantissa := rapid.Int64Range(-(1<<40), 1<<40).Draw(t, "mantissa2")
	return Money{mantissa: mantissa, currency: c.currency, exponent: c.exponent}
}

func TestProp_RoundTripEncodeDecode(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := genMoney(t)
		text, err := m.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText: %v", err)
		}
		got, err := Decode(string(text))
		if err != nil {
			t.Fatalf("Decode(%q): %v", text, err)
		}
		if got.mantissa != m.mantissa || got.currency != m.currency || got.exponent != m.exponent {
			t.Fatalf("round-trip mismatch: got %+v, want %+v", got, m)
		}
	})
}

func TestProp_AddAssociativeWhereDefined(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genMoney(t)
		b := genCompatible(t, a)
		c := genCompatible(t, a)

		ab, err := a.Add(b)
		if err != nil {
			t.Fatalf("a+b: %v", err)
		}
		left, err := ab.Add(c)
		if err != nil {
			t.Fatalf("(a+b)+c: %v", err)
		}
		bc, err := b.Add(c)
		if err != nil {
			t.Fatalf("b+c: %v", err)
		}
		right, err := a.Add(bc)
		if err != nil {
			t.Fatalf("a+(b+c): %v", err)
		}
		if left.mantissa != right.mantissa {
			t.Fatalf("associativity broke: (a+b)+c=%d, a+(b+c)=%d", left.mantissa, right.mantissa)
		}
	})
}

func TestProp_AddCommutativeWhereDefined(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genMoney(t)
		b := genCompatible(t, a)
		ab, err := a.Add(b)
		if err != nil {
			t.Fatalf("a+b: %v", err)
		}
		ba, err := b.Add(a)
		if err != nil {
			t.Fatalf("b+a: %v", err)
		}
		if ab.mantissa != ba.mantissa {
			t.Fatalf("commutativity broke: %d vs %d", ab.mantissa, ba.mantissa)
		}
	})
}

func TestProp_AddSubInverseWhereDefined(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genMoney(t)
		b := genCompatible(t, a)
		sum, err := a.Add(b)
		if err != nil {
			t.Fatalf("a+b: %v", err)
		}
		back, err := sum.Sub(b)
		if err != nil {
			t.Fatalf("(a+b)-b: %v", err)
		}
		if back.mantissa != a.mantissa {
			t.Fatalf("(a+b)-b = %d, want %d", back.mantissa, a.mantissa)
		}
	})
}

func TestProp_CurrencyMismatchAlwaysRejected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genMoney(t)
		// Draw a different currency, same exponent so the currency is the sole
		// difference.
		other := rapid.SampledFrom(propCurrencies).Filter(func(s string) bool {
			return s != a.currency
		}).Draw(t, "otherCurrency")
		b := Money{mantissa: a.mantissa, currency: other, exponent: a.exponent}

		if _, err := a.Add(b); !errors.Is(err, ErrCurrencyMismatch) {
			t.Fatalf("Add err = %v, want ErrCurrencyMismatch", err)
		}
		if _, err := a.Sub(b); !errors.Is(err, ErrCurrencyMismatch) {
			t.Fatalf("Sub err = %v, want ErrCurrencyMismatch", err)
		}
		if _, err := a.Compare(b); !errors.Is(err, ErrCurrencyMismatch) {
			t.Fatalf("Compare err = %v, want ErrCurrencyMismatch", err)
		}
	})
}

func TestProp_ExponentMismatchAlwaysRejected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genMoney(t)
		otherExp := int8(rapid.IntRange(-6, 6).Filter(func(e int) bool {
			return e != int(a.exponent)
		}).Draw(t, "otherExp"))
		b := Money{mantissa: a.mantissa, currency: a.currency, exponent: otherExp}

		if _, err := a.Add(b); !errors.Is(err, ErrExponentMismatch) {
			t.Fatalf("Add err = %v, want ErrExponentMismatch", err)
		}
		if _, err := a.Compare(b); !errors.Is(err, ErrExponentMismatch) {
			t.Fatalf("Compare err = %v, want ErrExponentMismatch", err)
		}
	})
}

func TestProp_CompareTotalOrderWhereDefined(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genMoney(t)
		b := genCompatible(t, a)
		ab, err := a.Compare(b)
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
		ba, err := b.Compare(a)
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
		// Antisymmetry: cmp(a,b) is the mirror of cmp(b,a). Expressed without
		// arithmetic so the money guard can cover test files too.
		ok := (ab == 0 && ba == 0) || (ab == 1 && ba == -1) || (ab == -1 && ba == 1)
		if !ok {
			t.Fatalf("antisymmetry broke: cmp(a,b)=%d cmp(b,a)=%d", ab, ba)
		}
	})
}
