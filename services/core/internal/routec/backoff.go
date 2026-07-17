package routec

import (
	"math/rand"
	"time"
)

// Backoff is exponential backoff with full jitter (docs/10-scraping-workflows.md:
// "at most three retries with 2-second exponential backoff"). It is a value type
// so a Config carries it by copy; Delay is pure aside from the injected rand. All
// math is integer (nanoseconds) — no floats on any path (repo guard, PRD §9.1).
type Backoff struct {
	// Base is the first-attempt delay.
	Base time.Duration
	// Max caps the delay so a long outage never schedules an absurd sleep.
	Max time.Duration
	// Factor is the integer exponential multiplier per attempt (>= 1).
	Factor int
}

// Delay returns the backoff for a zero-based attempt number using full jitter:
// a uniformly random duration in [0, exp) where exp = min(Base*Factor^attempt,
// Max). Full jitter avoids synchronized retry storms across targets. attempt < 0
// is treated as 0.
func (b Backoff) Delay(attempt int, rng *rand.Rand) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	factor := int64(b.Factor)
	if factor < 1 {
		factor = 1
	}
	exp := int64(b.Base)
	maxNs := int64(b.Max)
	for i := 0; i < attempt; i++ {
		exp *= factor
		if maxNs > 0 && exp >= maxNs {
			exp = maxNs
			break
		}
	}
	if maxNs > 0 && exp > maxNs {
		exp = maxNs
	}
	if exp <= 0 {
		return 0
	}
	if rng == nil {
		return time.Duration(exp)
	}
	return time.Duration(rng.Int63n(exp + 1))
}

// Jitter returns base spread by ± jitterBP basis points using rng: a duration
// uniformly in [base*(1-jitterBP/10000), base*(1+jitterBP/10000)]. It spreads the
// TIER SCHEDULE cadence (jitteredSchedule) so Route C never fires on a fixed,
// fingerprintable interval; retry spacing uses Backoff.Delay's own full jitter.
// A non-positive jitterBP returns base unchanged. Basis points keep the spread
// integer (repo guard: no floats).
func Jitter(base time.Duration, jitterBP int, rng *rand.Rand) time.Duration {
	if jitterBP <= 0 || base <= 0 || rng == nil {
		return base
	}
	if jitterBP > 10000 {
		jitterBP = 10000
	}
	span := int64(base) * int64(jitterBP) / 10000
	if span <= 0 {
		return base
	}
	// delta uniformly in [-span, +span].
	delta := rng.Int63n(2*span+1) - span
	return time.Duration(int64(base) + delta)
}
