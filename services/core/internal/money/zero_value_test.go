package money

import (
	"errors"
	"strings"
	"testing"
)

// TestZeroValueMoneyIsRejected is the regression test for issue #4 (money
// correctness never-cut, PRD §9.1): the Go zero value of Money is documented
// INVALID (empty currency), so every domain operation that CONSUMES or EMITS an
// authoritative Money must reject an invalid receiver OR operand rather than
// silently treating a missing amount as a real numeric zero.
//
// Table-driven across the full surface: Add, Sub, Compare, Equal, Neg, rate
// application (ApplyRate), netting (Net), and text serialization (MarshalText,
// String). The positive path — values built via New/Zero — must stay accepted.
func TestZeroValueMoneyIsRejected(t *testing.T) {
	valid := mustNew(t, 100, "USD", 0)
	var zero Money // Go zero value: currency == "" ⇒ invalid.

	// The issue's minimal reproduction.
	if _, err := zero.Add(zero); err == nil {
		t.Fatal("Add accepted invalid zero-value Money")
	}

	// Binary ops: an invalid receiver, an invalid operand, or both must be
	// rejected with the actionable ErrInvalidMoney (not a misleading
	// currency/exponent mismatch).
	binary := []struct {
		name string
		call func(a, b Money) error
	}{
		{"Add", func(a, b Money) error { _, err := a.Add(b); return err }},
		{"Sub", func(a, b Money) error { _, err := a.Sub(b); return err }},
		{"Compare", func(a, b Money) error { _, err := a.Compare(b); return err }},
		{"Equal", func(a, b Money) error { _, err := a.Equal(b); return err }},
	}
	operands := []struct {
		name string
		a, b Money
	}{
		{"invalid-receiver", zero, valid},
		{"invalid-operand", valid, zero},
		{"both-invalid", zero, zero},
	}
	for _, op := range binary {
		for _, tc := range operands {
			if err := op.call(tc.a, tc.b); !errors.Is(err, ErrInvalidMoney) {
				t.Errorf("%s(%s): err = %v, want ErrInvalidMoney", op.name, tc.name, err)
			}
		}
	}

	// Value-carrying / emitting ops on an invalid receiver.
	if _, err := zero.Neg(); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Neg(invalid): err = %v, want ErrInvalidMoney", err)
	}
	if _, err := zero.ApplyRate(NewBasisPoints(5000), RoundHalfEven); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("ApplyRate(invalid): err = %v, want ErrInvalidMoney", err)
	}
	if _, err := zero.MarshalText(); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("MarshalText(invalid): err = %v, want ErrInvalidMoney", err)
	}
	if _, err := Net(zero); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Net(invalid): err = %v, want ErrInvalidMoney", err)
	}

	// IsZero must NOT report an invalid (empty-currency) zero-value as a
	// legitimate monetary zero (issue #4 reopen residual). It rejects the invalid
	// receiver with ErrInvalidMoney, and — even for callers that ignore the error
	// — the boolean must be false so a missing amount never reads as a real zero.
	if isZero, err := zero.IsZero(); !errors.Is(err, ErrInvalidMoney) || isZero {
		t.Errorf("IsZero(invalid) = (%v, %v), want (false, ErrInvalidMoney)", isZero, err)
	}

	// String cannot return an error, so it must render an EXPLICIT invalid
	// marker — never an empty string or a fake "0"/valid-looking encoding that a
	// downstream reader could mistake for a real amount.
	got := zero.String()
	if !strings.Contains(got, "invalid") {
		t.Errorf("String(invalid) = %q, want an explicit invalid marker", got)
	}
	if m, err := Decode(got); err == nil {
		t.Errorf("String(invalid) = %q decoded as a valid Money %v; must not parse as an amount", got, m)
	}
}

// TestConstructedMoneyStillAccepted keeps the positive path green: values built
// through New/Zero add, subtract, compare, negate, rate-apply, and serialize as
// before. Guarding invalid values must not regress valid ones.
func TestConstructedMoneyStillAccepted(t *testing.T) {
	a := mustNew(t, 250, "USD", -2)
	b, err := Zero("USD", -2)
	if err != nil {
		t.Fatalf("Zero: %v", err)
	}

	if _, err := a.Add(b); err != nil {
		t.Fatalf("Add(valid,valid): %v", err)
	}
	if _, err := a.Sub(b); err != nil {
		t.Fatalf("Sub(valid,valid): %v", err)
	}
	if _, err := a.Compare(b); err != nil {
		t.Fatalf("Compare(valid,valid): %v", err)
	}
	if _, err := a.Equal(b); err != nil {
		t.Fatalf("Equal(valid,valid): %v", err)
	}
	if _, err := a.Neg(); err != nil {
		t.Fatalf("Neg(valid): %v", err)
	}
	if _, err := a.ApplyRate(NewBasisPoints(5000), RoundHalfEven); err != nil {
		t.Fatalf("ApplyRate(valid): %v", err)
	}
	if _, err := Net(a, b); err != nil {
		t.Fatalf("Net(valid,valid): %v", err)
	}
	txt, err := a.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText(valid): %v", err)
	}
	if _, err := Decode(string(txt)); err != nil {
		t.Fatalf("Decode round-trip: %v", err)
	}
	if got := a.String(); got != "USD:250:-2" {
		t.Fatalf("String(valid) = %q, want USD:250:-2", got)
	}
}

// TestIsZeroDistinguishesLegitimateFromInvalid pins issue #4's reopen residual:
// a deliberately constructed Zero(currency, exponent) is a legitimate monetary
// zero, while the Go zero value (var m Money, empty currency) is INVALID and must
// never be reported as a legitimate zero — it surfaces ErrInvalidMoney and a
// false boolean (PRD §9.1, money correctness never-cut).
func TestIsZeroDistinguishesLegitimateFromInvalid(t *testing.T) {
	deliberateZero, err := Zero("USD", -2)
	if err != nil {
		t.Fatalf("Zero: %v", err)
	}
	if isZero, err := deliberateZero.IsZero(); err != nil || !isZero {
		t.Errorf("IsZero(Zero(USD,-2)) = (%v, %v), want (true, nil)", isZero, err)
	}

	nonZero := mustNew(t, 250, "USD", -2)
	if isZero, err := nonZero.IsZero(); err != nil || isZero {
		t.Errorf("IsZero(constructed nonzero) = (%v, %v), want (false, nil)", isZero, err)
	}

	// A constructed zero in another currency is still a legitimate zero: validity,
	// not currency, is what distinguishes it from the invalid Go zero value.
	irrZero, err := Zero("IRR", 0)
	if err != nil {
		t.Fatalf("Zero(IRR): %v", err)
	}
	if isZero, err := irrZero.IsZero(); err != nil || !isZero {
		t.Errorf("IsZero(Zero(IRR,0)) = (%v, %v), want (true, nil)", isZero, err)
	}

	var invalid Money // empty currency ⇒ invalid, not a legitimate zero.
	if isZero, err := invalid.IsZero(); !errors.Is(err, ErrInvalidMoney) || isZero {
		t.Errorf("IsZero(var Money) = (%v, %v), want (false, ErrInvalidMoney)", isZero, err)
	}
}
