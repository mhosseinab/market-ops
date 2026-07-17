package money

// checked.go is the ONLY file in internal/money permitted to use raw integer
// operators. Every operator line is annotated with a `// nosemgrep` marker so
// the money guard (tools/semgrep/money.yml) can enforce "raw integer arithmetic
// lives here and nowhere else" by auditing `nosemgrep` markers. All other files
// in this package — and every file in internal/{margin,policy,approval} — must
// route integer math through these overflow-checked helpers or through Money
// methods. Do not weaken the guard by adding operators outside this file.
//
// PRD §9.1: "A static rule forbids raw integer arithmetic in money, margin,
// policy, and card packages." These primitives are that rule's single, audited,
// overflow-checked exception.

// minInt64 is the one int64 value with no positive counterpart; ineg/imul guard
// against it explicitly.
const minInt64 int64 = -1 << 63 // nosemgrep

// iadd returns a+b, or ErrOverflow if the true sum is outside int64.
func iadd(a, b int64) (int64, error) {
	sum := a + b // nosemgrep
	// Overflow iff the addends share a sign that the result does not.
	if (b > 0 && sum < a) || (b < 0 && sum > a) { // nosemgrep
		return 0, ErrOverflow
	}
	return sum, nil
}

// isub returns a-b, or ErrOverflow if the true difference is outside int64.
func isub(a, b int64) (int64, error) {
	diff := a - b                                   // nosemgrep
	if (b < 0 && diff < a) || (b > 0 && diff > a) { // nosemgrep
		return 0, ErrOverflow
	}
	return diff, nil
}

// ineg returns -a, or ErrOverflow when a == minInt64 (which has no positive
// counterpart in two's complement int64).
func ineg(a int64) (int64, error) {
	if a == minInt64 { // nosemgrep
		return 0, ErrOverflow
	}
	return -a, nil // nosemgrep
}

// imul returns a*b, or ErrOverflow if the true product is outside int64.
func imul(a, b int64) (int64, error) {
	if a == 0 || b == 0 { // nosemgrep
		return 0, nil
	}
	if a == minInt64 || b == minInt64 { // nosemgrep
		// minInt64 can only be multiplied by 0 or 1 without overflow.
		if a == 1 { // nosemgrep
			return b, nil
		}
		if b == 1 { // nosemgrep
			return a, nil
		}
		return 0, ErrOverflow
	}
	p := a * b    // nosemgrep
	if p/b != a { // nosemgrep
		return 0, ErrOverflow
	}
	return p, nil
}

// icmp reports whether a is less than (-1), equal to (0), or greater than (+1) b.
func icmp(a, b int64) int {
	switch {
	case a < b: // nosemgrep
		return -1
	case a > b: // nosemgrep
		return 1
	default:
		return 0
	}
}

// iabs returns the absolute value of a, or ErrOverflow when a == minInt64.
func iabs(a int64) (int64, error) {
	if a < 0 { // nosemgrep
		return ineg(a)
	}
	return a, nil
}

// isEven reports whether a is even.
func isEven(a int64) bool {
	return a%2 == 0 // nosemgrep
}
