package money

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, mantissa int64, currency string, exponent int8) Money {
	t.Helper()
	m, err := New(mantissa, currency, exponent)
	if err != nil {
		t.Fatalf("New(%d,%q,%d): %v", mantissa, currency, exponent, err)
	}
	return m
}

func TestNewRejectsInvalidCurrency(t *testing.T) {
	for _, code := range []string{"", "us", "USDD", "us1", "ZZZ", "irr", " USD"} {
		if _, err := New(1, code, 0); !errors.Is(err, ErrInvalidCurrency) {
			t.Errorf("New(_, %q, _) err = %v, want ErrInvalidCurrency", code, err)
		}
	}
	// Region-neutral sanity: IRR and USD are both accepted, neither privileged.
	for _, code := range []string{"IRR", "USD", "EUR", "JPY"} {
		if _, err := New(1, code, 0); err != nil {
			t.Errorf("New(_, %q, _) unexpected err %v", code, err)
		}
	}
}

func TestAddRejectsCurrencyMismatch(t *testing.T) {
	a := mustNew(t, 100, "USD", 0)
	b := mustNew(t, 100, "EUR", 0)
	if _, err := a.Add(b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Add cross-currency err = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := a.Sub(b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Sub cross-currency err = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := a.Compare(b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Compare cross-currency err = %v, want ErrCurrencyMismatch", err)
	}
}

func TestAddRejectsExponentMismatch(t *testing.T) {
	a := mustNew(t, 100, "USD", 0)
	b := mustNew(t, 100, "USD", -2)
	if _, err := a.Add(b); !errors.Is(err, ErrExponentMismatch) {
		t.Fatalf("Add exponent-mismatch err = %v, want ErrExponentMismatch", err)
	}
	if _, err := a.Compare(b); !errors.Is(err, ErrExponentMismatch) {
		t.Fatalf("Compare exponent-mismatch err = %v, want ErrExponentMismatch", err)
	}
}

func TestAddOverflow(t *testing.T) {
	a := mustNew(t, 1<<61, "USD", 0) // large but valid
	b := mustNew(t, 1<<61, "USD", 0)
	sum, err := a.Add(b) // = 1<<62, still valid
	if err != nil {
		t.Fatalf("Add near-max unexpected err %v", err)
	}
	// 1<<62 + 1<<62 = 1<<63 overflows int64.
	if _, err := sum.Add(sum); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Add overflow err = %v, want ErrOverflow", err)
	}
}

func TestNegMinInt64Overflows(t *testing.T) {
	m := mustNew(t, minInt64, "USD", 0)
	if _, err := m.Neg(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Neg(minInt64) err = %v, want ErrOverflow", err)
	}
}

func TestNetRejectsIncompatible(t *testing.T) {
	usd := mustNew(t, 10, "USD", 0)
	eur := mustNew(t, 10, "EUR", 0)
	if _, err := Net(usd, eur); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Net mixed err = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := Net(); !errors.Is(err, ErrMalformed) {
		t.Fatalf("Net() err = %v, want ErrMalformed", err)
	}
	got, err := Net(usd, usd, usd)
	if err != nil {
		t.Fatalf("Net same-currency err %v", err)
	}
	if got.Mantissa() != 30 {
		t.Fatalf("Net sum = %d, want 30", got.Mantissa())
	}
}

func TestApplyRateRoundingModes(t *testing.T) {
	// 12345 * 2500bp (25%) = 3086.25 -> mantissa 3086 exact + remainder.
	// Use a value with a clean half to exercise half-* modes: 10 * 5000bp = 5.0
	// -> exact. Use 5 * 5000bp = 2.5 to exercise the half rule.
	base := mustNew(t, 5, "USD", 0)
	half := NewBasisPoints(5000) // 50%
	cases := []struct {
		name  string
		round Rounder
		want  int64
	}{
		{"down", RoundDown, 2},
		{"up", RoundUp, 3},
		{"ceil", RoundCeil, 3},
		{"floor", RoundFloor, 2},
		{"halfup", RoundHalfUp, 3},
		{"halfeven->even(2)", RoundHalfEven, 2}, // 2.5 -> 2 (even)
	}
	for _, tc := range cases {
		got, err := base.ApplyRate(half, tc.round)
		if err != nil {
			t.Fatalf("%s: ApplyRate err %v", tc.name, err)
		}
		if got.Mantissa() != tc.want {
			t.Errorf("%s: mantissa = %d, want %d", tc.name, got.Mantissa(), tc.want)
		}
		if got.Currency() != "USD" || got.Exponent() != 0 {
			t.Errorf("%s: currency/exponent not preserved: %s", tc.name, got)
		}
	}
}

func TestApplyRateNegativeHalfEven(t *testing.T) {
	// -3 * 5000bp = -1.5 -> half-even to even neighbour -2.
	base := mustNew(t, -3, "USD", 0)
	got, err := base.ApplyRate(NewBasisPoints(5000), RoundHalfEven)
	if err != nil {
		t.Fatalf("ApplyRate err %v", err)
	}
	if got.Mantissa() != -2 {
		t.Fatalf("half-even(-1.5) = %d, want -2", got.Mantissa())
	}
}

func TestApplyRateFullRateIsIdentity(t *testing.T) {
	base := mustNew(t, 123456789, "IRR", 0)
	got, err := base.ApplyRate(NewBasisPoints(BasisPointScale), RoundHalfEven) // 100%
	if err != nil {
		t.Fatalf("ApplyRate 100%% err %v", err)
	}
	if got.Mantissa() != base.Mantissa() {
		t.Fatalf("100%% of %d = %d, want identity", base.Mantissa(), got.Mantissa())
	}
}

func TestPercentPoints(t *testing.T) {
	bp, err := PercentPoints(5)
	if err != nil {
		t.Fatalf("PercentPoints err %v", err)
	}
	if bp.Value() != 500 {
		t.Fatalf("PercentPoints(5) = %d bp, want 500", bp.Value())
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "USD", "USD:1", "USD:x:0", "USD:1:0:0", "ZZZ:1:0", "USD:1:999"} {
		if _, err := Decode(s); err == nil {
			t.Errorf("Decode(%q) = nil err, want error", s)
		}
	}
}

func TestRawAmountIsSeparateFromMoney(t *testing.T) {
	r := NewRawAmount("۱٬۲۳۴ تومان", "۱۲۳۴", "تومان")
	if r.IsEmpty() {
		t.Fatal("RawAmount should not be empty")
	}
	if r.Text == "" || r.Value == "" || r.Unit == "" {
		t.Fatal("RawAmount must preserve captured fields verbatim")
	}
}
