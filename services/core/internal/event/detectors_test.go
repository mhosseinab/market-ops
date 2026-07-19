package event_test

import (
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// irr builds an IRR money value at exponent 0 for tests.
func irr(t *testing.T, mantissa int64) money.Money {
	t.Helper()
	m, err := money.New(mantissa, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return m
}

func goodEvidence() event.Evidence {
	return event.Evidence{
		ObservationID: uuid.New(),
		Quality:       event.QualityVerified,
		Ref:           "obs-ref",
		Detail:        map[string]string{},
	}
}

// --- (1) Winning state -----------------------------------------------------

func TestDetectWinningState(t *testing.T) {
	now := time.Now()
	variant := uuid.New()
	base := event.WinningStateInput{
		Variant: variant, Target: uuid.New(),
		Evidence: goodEvidence(), Now: now, TTL: time.Hour,
	}

	t.Run("lost is critical", func(t *testing.T) {
		in := base
		in.WasWinning, in.IsWinning = true, false
		c, ok := event.DetectWinningState(in)
		if !ok {
			t.Fatal("expected winning-state-lost to fire")
		}
		if c.Severity != event.SeverityCritical {
			t.Errorf("severity = %s, want critical", c.Severity)
		}
		if c.Type != event.TypeWinningState {
			t.Errorf("type = %s", c.Type)
		}
		if !c.ExpiresAt.Equal(now.Add(time.Hour)) {
			t.Errorf("expiry = %v, want %v", c.ExpiresAt, now.Add(time.Hour))
		}
	})

	t.Run("challenged while winning is warning", func(t *testing.T) {
		in := base
		in.WasWinning, in.IsWinning, in.Challenged = true, true, true
		c, ok := event.DetectWinningState(in)
		if !ok || c.Severity != event.SeverityWarning {
			t.Fatalf("expected challenged warning, got ok=%v sev=%s", ok, c.Severity)
		}
	})

	t.Run("steady winning is not material (resolution path)", func(t *testing.T) {
		in := base
		in.WasWinning, in.IsWinning, in.Challenged = true, true, false
		if _, ok := event.DetectWinningState(in); ok {
			t.Fatal("steady winning must not fire — this is the resolution condition")
		}
	})
}

// --- (2) Competitor price movement -----------------------------------------

func TestDetectCompetitorPrice(t *testing.T) {
	now := time.Now()
	thr := event.Threshold{ID: uuid.New(), Version: 3, MoveBp: money.NewBasisPoints(500)} // 5%
	base := event.CompetitorPriceInput{
		Variant: uuid.New(), Target: uuid.New(), OfferIdentity: "seller-9",
		Unit: "IRR", Evidence: goodEvidence(), Now: now, TTL: time.Hour, Threshold: thr,
	}

	t.Run("below threshold does not fire", func(t *testing.T) {
		in := base
		in.PrevValue, in.CurrValue = "1000000", "1030000" // 3% < 5%
		if _, ok := event.DetectCompetitorPrice(in); ok {
			t.Fatal("3% movement under a 5% threshold must not fire")
		}
	})

	t.Run("at threshold fires warning and preserves raw tokens", func(t *testing.T) {
		in := base
		in.PrevValue, in.CurrValue = "1000000", "1060000" // 6% >= 5%
		c, ok := event.DetectCompetitorPrice(in)
		if !ok {
			t.Fatal("6% movement must fire")
		}
		if c.Severity != event.SeverityWarning {
			t.Errorf("severity = %s, want warning", c.Severity)
		}
		if c.ThresholdVersion != 3 || c.ThresholdID != thr.ID {
			t.Errorf("threshold provenance not recorded: %d/%v", c.ThresholdVersion, c.ThresholdID)
		}
		if c.Evidence.Detail["prev_value"] != "1000000" || c.Evidence.Detail["curr_value"] != "1060000" {
			t.Errorf("raw before/after tokens not preserved verbatim: %v", c.Evidence.Detail)
		}
	})

	t.Run("double threshold is critical", func(t *testing.T) {
		in := base
		in.PrevValue, in.CurrValue = "1000000", "1120000" // 12% >= 2*5%
		c, ok := event.DetectCompetitorPrice(in)
		if !ok || c.Severity != event.SeverityCritical {
			t.Fatalf("12%% movement must be critical, got ok=%v sev=%s", ok, c.Severity)
		}
	})

	t.Run("empty unit cannot compute movement (quarantine — never guesses)", func(t *testing.T) {
		in := base
		in.Unit = ""
		in.PrevValue, in.CurrValue = "1000000", "2000000"
		if _, ok := event.DetectCompetitorPrice(in); ok {
			t.Fatal("a movement with no unit must not fire — money quarantine")
		}
	})

	// issue #72: an extreme but schema-valid movement whose basis-point value
	// exceeds int64 must NOT wrap through a narrowing conversion and be misread
	// as a below-threshold non-event. It is material and critical, and the
	// move_bp evidence carries the EXACT big-integer value (no truncation).
	t.Run("#72 extreme MaxInt64 movement is material and critical (no int64 narrowing)", func(t *testing.T) {
		in := base
		in.PrevValue = "1"
		in.CurrValue = strconv.FormatInt(math.MaxInt64, 10) // 9223372036854775807
		c, ok := event.DetectCompetitorPrice(in)
		if !ok {
			t.Fatal("an extreme MaxInt64 movement must fire — a wrapped int64 must not become a false non-event")
		}
		if c.Severity != event.SeverityCritical {
			t.Errorf("severity = %s, want critical", c.Severity)
		}
		// bp = |curr-prev| * 10000 / prev = 9223372036854775806 * 10000 / 1.
		wantBp, _ := new(big.Int).SetString("92233720368547758060000", 10)
		if got := c.Evidence.Detail["move_bp"]; got != wantBp.String() {
			t.Errorf("move_bp = %q, want exact %q (no int64 truncation)", got, wantBp.String())
		}
	})
}

// TestDetectCompetitorPriceMatchesBigIntReference is the property test the issue
// asks for: over a wide range of same-unit token pairs (including movements whose
// bp overflows int64), the detector's fire/severity classification and the move_bp
// evidence must match an independent big.Int reference — so the comparison boundary
// is exact and deterministic, never lossy (issue #72, EVT-002).
func TestDetectCompetitorPriceMatchesBigIntReference(t *testing.T) {
	now := time.Now()
	const thrBp int64 = 500 // 5%
	thr := event.Threshold{ID: uuid.New(), Version: 1, MoveBp: money.NewBasisPoints(thrBp)}
	base := event.CompetitorPriceInput{
		Variant: uuid.New(), Target: uuid.New(), OfferIdentity: "s",
		Unit: "IRR", Evidence: goodEvidence(), Now: now, TTL: time.Hour, Threshold: thr,
	}
	threshold := big.NewInt(thrBp)
	doubleThreshold := new(big.Int).Mul(threshold, big.NewInt(2))

	rng := rand.New(rand.NewSource(72))
	for i := 0; i < 5000; i++ {
		var prev, curr int64
		switch i % 4 {
		case 0: // tiny prev, huge curr — the overflow region
			prev = rng.Int63n(1000) + 1
			curr = rng.Int63n(math.MaxInt64)
		case 1: // near-equal values — the below-threshold region
			prev = rng.Int63n(math.MaxInt64-1000) + 1000
			curr = prev + rng.Int63n(2000) - 1000
			if curr < 0 {
				curr = 0
			}
		default:
			prev = rng.Int63n(math.MaxInt64-1) + 1
			curr = rng.Int63n(math.MaxInt64)
		}

		// Independent reference classification, entirely in big.Int.
		diff := new(big.Int).Sub(big.NewInt(curr), big.NewInt(prev))
		diff.Abs(diff)
		bp := new(big.Int).Mul(diff, big.NewInt(money.BasisPointScale))
		bp.Quo(bp, big.NewInt(prev))
		wantFire := bp.Cmp(threshold) >= 0
		wantCritical := bp.Cmp(doubleThreshold) >= 0

		in := base
		in.PrevValue = strconv.FormatInt(prev, 10)
		in.CurrValue = strconv.FormatInt(curr, 10)
		c, ok := event.DetectCompetitorPrice(in)
		if ok != wantFire {
			t.Fatalf("fire mismatch prev=%d curr=%d bp=%s: got %v want %v", prev, curr, bp, ok, wantFire)
		}
		if !ok {
			continue
		}
		if gotCritical := c.Severity == event.SeverityCritical; gotCritical != wantCritical {
			t.Fatalf("severity mismatch prev=%d curr=%d bp=%s: gotCritical=%v want %v", prev, curr, bp, gotCritical, wantCritical)
		}
		if got := c.Evidence.Detail["move_bp"]; got != bp.String() {
			t.Fatalf("move_bp mismatch prev=%d curr=%d: got %q want exact %q", prev, curr, got, bp.String())
		}
	}
}

// --- (3) Seller-count movement ---------------------------------------------

func TestDetectSellerCount(t *testing.T) {
	now := time.Now()
	thr := event.Threshold{ID: uuid.New(), Version: 1, SellerCountDelta: 2}
	base := event.SellerCountInput{
		Variant: uuid.New(), Evidence: goodEvidence(), Now: now, TTL: time.Hour, Threshold: thr,
	}

	t.Run("small change does not fire", func(t *testing.T) {
		in := base
		in.PrevCount, in.CurrCount = 5, 6
		if _, ok := event.DetectSellerCount(in); ok {
			t.Fatal("a delta of 1 under threshold 2 must not fire")
		}
	})
	t.Run("threshold change fires", func(t *testing.T) {
		in := base
		in.PrevCount, in.CurrCount = 5, 7
		c, ok := event.DetectSellerCount(in)
		if !ok || c.Type != event.TypeSellerCount {
			t.Fatalf("delta 2 must fire, got ok=%v", ok)
		}
	})
}

// --- (4) Suppression / boundary --------------------------------------------

func TestDetectSuppressionBoundary(t *testing.T) {
	now := time.Now()
	base := event.SuppressionBoundaryInput{
		Variant: uuid.New(), Evidence: goodEvidence(), Now: now, TTL: time.Hour,
	}
	t.Run("newly suppressed is critical", func(t *testing.T) {
		in := base
		in.WasSuppressed, in.IsSuppressed = false, true
		c, ok := event.DetectSuppressionBoundary(in)
		if !ok || c.Severity != event.SeverityCritical {
			t.Fatalf("suppression must be critical, got ok=%v sev=%s", ok, c.Severity)
		}
	})
	t.Run("boundary change is warning", func(t *testing.T) {
		in := base
		in.BoundaryChanged = true
		c, ok := event.DetectSuppressionBoundary(in)
		if !ok || c.Severity != event.SeverityWarning {
			t.Fatalf("boundary change must be warning, got ok=%v sev=%s", ok, c.Severity)
		}
	})
	t.Run("no change does not fire", func(t *testing.T) {
		if _, ok := event.DetectSuppressionBoundary(base); ok {
			t.Fatal("steady, unchanged state must not fire")
		}
	})
}

// --- (5) Contribution floor (consumes S16; dormant behind readiness) -------

func TestDetectContributionFloor(t *testing.T) {
	now := time.Now()
	variant := uuid.New()
	base := event.ContributionFloorInput{
		Variant: variant, Evidence: goodEvidence(), Now: now, TTL: time.Hour,
		Floor: irr(t, 100),
	}

	t.Run("dormant unless readiness is Complete", func(t *testing.T) {
		for _, st := range []cost.State{cost.StatePartial, cost.StateStale, cost.StateMissing} {
			in := base
			in.Readiness = st
			in.HasContribution = true
			in.Contribution = irr(t, 50) // below floor, but readiness not Complete
			_, ok, err := event.DetectContributionFloor(in)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if ok {
				t.Fatalf("floor detector must stay DORMANT at readiness %s — never fabricate a floor", st)
			}
		}
	})

	t.Run("dormant when no contribution present", func(t *testing.T) {
		in := base
		in.Readiness = cost.StateComplete
		in.HasContribution = false
		if _, ok, _ := event.DetectContributionFloor(in); ok {
			t.Fatal("no contribution ⇒ dormant")
		}
	})

	t.Run("at or above floor is not material", func(t *testing.T) {
		in := base
		in.Readiness = cost.StateComplete
		in.HasContribution = true
		in.Contribution = irr(t, 150)
		if _, ok, _ := event.DetectContributionFloor(in); ok {
			t.Fatal("contribution above floor must not fire")
		}
	})

	t.Run("below floor fires with KNOWN exposure = shortfall", func(t *testing.T) {
		in := base
		in.Readiness = cost.StateComplete
		in.HasContribution = true
		in.Contribution = irr(t, 60) // floor 100 → shortfall 40
		c, ok, err := event.DetectContributionFloor(in)
		if err != nil || !ok {
			t.Fatalf("below floor must fire: ok=%v err=%v", ok, err)
		}
		if !c.Exposure.Known() {
			t.Fatal("floor breach exposure must be KNOWN (real economics)")
		}
		amt, _ := c.Exposure.Amount()
		if amt.Mantissa() != 40 {
			t.Errorf("shortfall = %d, want 40", amt.Mantissa())
		}
		if c.Severity != event.SeverityWarning {
			t.Errorf("severity = %s, want warning (still positive)", c.Severity)
		}
	})

	t.Run("non-positive contribution is critical (crosses zero)", func(t *testing.T) {
		in := base
		in.Readiness = cost.StateComplete
		in.HasContribution = true
		in.Contribution = irr(t, -10)
		c, ok, err := event.DetectContributionFloor(in)
		if err != nil || !ok {
			t.Fatalf("negative contribution must fire: ok=%v err=%v", ok, err)
		}
		if c.Severity != event.SeverityCritical {
			t.Errorf("severity = %s, want critical", c.Severity)
		}
	})
}

// TestDedupKeyIsStablePerCondition proves a repeated detection of the same
// condition yields the SAME dedup key (so it collides on the open record) while a
// different sub-identity yields a different key.
func TestDedupKeyIsStablePerCondition(t *testing.T) {
	now := time.Now()
	variant := uuid.New()
	in := event.CompetitorPriceInput{
		Variant: variant, OfferIdentity: "seller-9", Unit: "IRR",
		PrevValue: "1000000", CurrValue: "1060000",
		Evidence: goodEvidence(), Now: now, TTL: time.Hour,
		Threshold: event.Threshold{MoveBp: money.NewBasisPoints(500)},
	}
	c1, _ := event.DetectCompetitorPrice(in)
	c2, _ := event.DetectCompetitorPrice(in)
	if c1.DedupKey != c2.DedupKey {
		t.Fatalf("same condition must share a dedup key: %q vs %q", c1.DedupKey, c2.DedupKey)
	}
	in.OfferIdentity = "seller-42"
	c3, _ := event.DetectCompetitorPrice(in)
	if c3.DedupKey == c1.DedupKey {
		t.Fatal("a different competitor must have a distinct dedup key")
	}
}
