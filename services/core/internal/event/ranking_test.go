package event_test

import (
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// mkEventCE builds a KNOWN-exposure market-event row with an explicit currency
// and exponent, so the money-correctness ranking tests (issue #71) can exercise
// exponent normalization and mixed-currency quarantine.
func mkEventCE(id uuid.UUID, sev string, mantissa int64, currency string, exponent int16, confBp, urgBp int32, detected time.Time) db.MarketEvent {
	return db.MarketEvent{
		ID:               id,
		Severity:         sev,
		ExposureKnown:    true,
		ExposureCurrency: currency,
		ExposureExponent: exponent,
		ExposureMantissa: pgtype.Int8{Int64: mantissa, Valid: true},
		ConfidenceBp:     confBp,
		UrgencyBp:        urgBp,
		FirstDetectedAt:  detected,
	}
}

// TestExposureUnknownIsNotZero is the EVT-005 unit invariant: an unknown exposure
// is distinct from a zero amount, and its Amount() never yields a usable number.
func TestExposureUnknownIsNotZero(t *testing.T) {
	unknown := event.UnknownExposure()
	if unknown.Known() {
		t.Fatal("UnknownExposure must report Known()=false")
	}
	if _, ok := unknown.Amount(); ok {
		t.Fatal("UnknownExposure.Amount() must return ok=false — never a fabricated number")
	}
	// The zero value must ALSO be unknown (the safety default).
	var zeroValue event.Exposure
	if zeroValue.Known() {
		t.Fatal("the zero-value Exposure must be Unknown by default")
	}

	// A KNOWN zero amount is different from Unknown: it IS a number (0), reported ok.
	zeroMoney, err := money.Zero("IRR", 0)
	if err != nil {
		t.Fatal(err)
	}
	known := event.KnownExposure(zeroMoney)
	amt, ok := known.Amount()
	if !ok || amt.Mantissa() != 0 {
		t.Fatalf("KnownExposure(0) must be a known zero amount, got ok=%v mant=%d", ok, amt.Mantissa())
	}
}

// mkEvent builds a market-event row for ranking tests.
func mkEvent(id uuid.UUID, sev string, exposureKnown bool, mantissa int64, confBp, urgBp int32, detected time.Time) db.MarketEvent {
	e := db.MarketEvent{
		ID:               id,
		Severity:         sev,
		ExposureKnown:    exposureKnown,
		ExposureCurrency: "",
		ConfidenceBp:     confBp,
		UrgencyBp:        urgBp,
		FirstDetectedAt:  detected,
	}
	if exposureKnown {
		e.ExposureCurrency = "IRR"
		e.ExposureMantissa = pgtype.Int8{Int64: mantissa, Valid: true}
	}
	return e
}

// TestRankIsDeterministicAndFactorExposing proves EVT-004: the rank uses all three
// factors, exposes them, and is deterministic (independent of input order).
func TestRankIsDeterministicAndFactorExposing(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	big := mkEvent(uuid.MustParse("00000000-0000-0000-0000-0000000000a1"), "critical", true, 1_000_000, 10000, 10000, base)
	mid := mkEvent(uuid.MustParse("00000000-0000-0000-0000-0000000000b2"), "warning", true, 500_000, 8000, 6000, base)
	small := mkEvent(uuid.MustParse("00000000-0000-0000-0000-0000000000c3"), "info", true, 10_000, 4000, 3000, base)

	order1 := event.Rank([]db.MarketEvent{small, big, mid})
	order2 := event.Rank([]db.MarketEvent{mid, small, big})

	// Deterministic: both input orders produce the same ranked id sequence.
	for i := range order1 {
		if order1[i].Event.ID != order2[i].Event.ID {
			t.Fatalf("rank not deterministic at %d: %v vs %v", i, order1[i].Event.ID, order2[i].Event.ID)
		}
		if order1[i].Rank != i+1 {
			t.Errorf("rank field = %d, want %d", order1[i].Rank, i+1)
		}
	}
	// Highest exposure×confidence×urgency ranks first.
	if order1[0].Event.ID != big.ID || order1[2].Event.ID != small.ID {
		t.Fatalf("ranking order wrong: %v", []uuid.UUID{order1[0].Event.ID, order1[1].Event.ID, order1[2].Event.ID})
	}
	// All three factors exposed on each item.
	if order1[0].Factors.ConfidenceBp != 10000 || order1[0].Factors.UrgencyBp != 10000 {
		t.Error("confidence/urgency factors not exposed")
	}
	if !order1[0].Factors.ExposureKnown || order1[0].Factors.ExposureMantissa != 1_000_000 {
		t.Error("exposure factor not exposed")
	}
	if order1[0].Score == nil {
		t.Error("known-exposure event must carry a composite score")
	}
}

// TestUnknownExposureNeverBecomesNumber is the EVT-005 ranking negative test: an
// unknown-exposure event is ranked WITHOUT coercing its exposure to 0, and it
// sits in a separate band after known-exposure events — its factors still report
// ExposureKnown=false and no mantissa.
func TestUnknownExposureNeverBecomesNumber(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// A tiny KNOWN exposure and a maximally urgent UNKNOWN exposure.
	tiny := mkEvent(uuid.MustParse("00000000-0000-0000-0000-0000000000d4"), "info", true, 1, 1000, 1000, base)
	unknown := mkEvent(uuid.MustParse("00000000-0000-0000-0000-0000000000e5"), "critical", false, 0, 10000, 10000, base)

	ranked := event.Rank([]db.MarketEvent{unknown, tiny})

	// EVT-005: the unknown exposure is NOT turned into a number, so it cannot be
	// multiplied into a huge score that outranks a real (known) loss. Known first.
	if ranked[0].Event.ID != tiny.ID {
		t.Fatalf("a known-exposure event must rank ahead of an unknown-exposure one; got %v first", ranked[0].Event.ID)
	}
	unknownRanked := ranked[1]
	if unknownRanked.Event.ID != unknown.ID {
		t.Fatalf("unexpected order: %v", unknownRanked.Event.ID)
	}
	if unknownRanked.Score != nil {
		t.Fatal("an unknown-exposure event must carry a nil composite score (no fabricated number)")
	}
	if unknownRanked.Factors.ExposureKnown {
		t.Fatal("unknown exposure must stay ExposureKnown=false in the exposed factors")
	}
	if unknownRanked.Factors.ExposureMantissa != 0 || unknownRanked.Factors.ExposureCurrency != "" {
		// mantissa defaults to 0 in the struct, but ExposureKnown=false means the UI
		// must never read it as a value; assert currency stays empty so it cannot be
		// rendered as a real amount.
		if unknownRanked.Factors.ExposureCurrency != "" {
			t.Fatal("unknown exposure must not expose a currency (would imply a real amount)")
		}
	}
}

// TestRankEquivalentExponentsTieExactly is the issue #71 money-correctness
// invariant: two exposures with the SAME real value expressed at different
// exponents (1000@exp2 == 100000@exp0 in the same currency) must produce an
// EXACTLY equal composite score — never ordered by raw mantissa. The prior bug
// discarded the exponent, so 100000@exp0 outranked its equal 1000@exp2.
func TestRankEquivalentExponentsTieExactly(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// 1000 × 10^2 = 100000 ; 100000 × 10^0 = 100000 — identical real value.
	a := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000101"), "warning", 1000, "IRR", 2, 5000, 5000, base)
	b := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000102"), "warning", 100000, "IRR", 0, 5000, 5000, base)

	ranked := event.Rank([]db.MarketEvent{a, b})
	if ranked[0].Score == nil || ranked[1].Score == nil {
		t.Fatal("both known-exposure events must carry a composite score")
	}
	if ranked[0].Score.Cmp(ranked[1].Score) != 0 {
		t.Fatalf("equal real values at different exponents must score EXACTLY equal; got %s vs %s",
			ranked[0].Score, ranked[1].Score)
	}
}

// TestRankLargerCanonicalValueRanksHigher proves ordering follows the REAL money
// magnitude (mantissa × 10^exponent) across positive, zero, AND negative
// exponents — not the raw mantissa (issue #71).
func TestRankLargerCanonicalValueRanksHigher(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Real values: e1 = 5×10^3 = 5000 ; e2 = 100×10^0 = 100 ; e3 = 3000×10^-2 = 30.
	// Raw-mantissa ordering (3000 > 100 > 5) is the OPPOSITE of the real ordering.
	e1 := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000201"), "warning", 5, "IRR", 3, 5000, 5000, base)
	e2 := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000202"), "warning", 100, "IRR", 0, 5000, 5000, base)
	e3 := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000203"), "warning", 3000, "IRR", -2, 5000, 5000, base)

	ranked := event.Rank([]db.MarketEvent{e3, e1, e2})
	got := []uuid.UUID{ranked[0].Event.ID, ranked[1].Event.ID, ranked[2].Event.ID}
	want := []uuid.UUID{e1.ID, e2.ID, e3.ID}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ranking must follow real money magnitude, got %v want %v", got, want)
		}
	}
}

// TestRankNonPositiveMantissaClampsToZero keeps the credit-never-outranks-a-loss
// invariant across exponent normalization: a negative-mantissa exposure clamps to
// a zero magnitude and cannot outrank even a tiny real loss.
func TestRankNonPositiveMantissaClampsToZero(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	loss := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000301"), "info", 1, "IRR", 0, 1000, 1000, base)
	credit := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000302"), "critical", -1_000_000, "IRR", 0, 10000, 10000, base)

	ranked := event.Rank([]db.MarketEvent{credit, loss})
	if ranked[0].Event.ID != loss.ID {
		t.Fatalf("a real loss must outrank a (clamped) credit; got %v first", ranked[0].Event.ID)
	}
	if ranked[1].Score == nil || ranked[1].Score.Sign() != 0 {
		t.Fatalf("non-positive mantissa must clamp to a zero magnitude, got %v", ranked[1].Score)
	}
}

// TestRankLargeInt64MantissaStaysExact proves the composite score is EXACT big.Int
// arithmetic near math.MaxInt64 — no float, no overflow.
func TestRankLargeInt64MantissaStaysExact(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	big1 := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000401"), "warning", math.MaxInt64, "IRR", 0, 10000, 10000, base)
	half := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000402"), "warning", math.MaxInt64/2, "IRR", 0, 10000, 10000, base)

	ranked := event.Rank([]db.MarketEvent{half, big1})
	if ranked[0].Event.ID != big1.ID {
		t.Fatalf("larger exact mantissa must rank first, got %v", ranked[0].Event.ID)
	}
	// Exact expected score: MaxInt64 × 10000 × 10000, computed in big.Int.
	want := new(big.Int).SetInt64(math.MaxInt64)
	want.Mul(want, big.NewInt(10000))
	want.Mul(want, big.NewInt(10000))
	if ranked[0].Score.Cmp(want) != 0 {
		t.Fatalf("large-mantissa score must be exact; got %s want %s", ranked[0].Score, want)
	}
}

// TestRankMixedCurrencyQuarantine is the issue #71 fail-closed invariant: a
// known-exposure event whose currency is NOT the canonical (majority) currency is
// QUARANTINED — it keeps ExposureKnown=true but gets a nil Score, is placed in a
// non-comparable band AFTER the comparable-known band, and is never magnitude-
// compared against the canonical band (even with a huge mantissa). The full order
// is deterministic regardless of input order.
func TestRankMixedCurrencyQuarantine(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Canonical = IRR (2 known events). USD is the minority → quarantined, despite a
	// mantissa large enough to dominate if it were (wrongly) compared cross-currency.
	irrA := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000501"), "warning", 100, "IRR", 0, 9000, 9000, base)
	irrB := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000502"), "warning", 50, "IRR", 0, 9000, 9000, base)
	usd := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000503"), "critical", math.MaxInt64, "USD", 0, 10000, 10000, base)

	inputs := [][]db.MarketEvent{
		{irrA, irrB, usd},
		{usd, irrB, irrA},
		{irrB, usd, irrA},
	}
	var first []uuid.UUID
	for idx, in := range inputs {
		ranked := event.Rank(in)
		// Comparable IRR band first (magnitude order irrA > irrB), then quarantined USD.
		if ranked[0].Event.ID != irrA.ID || ranked[1].Event.ID != irrB.ID || ranked[2].Event.ID != usd.ID {
			t.Fatalf("input %d: quarantined non-canonical currency must sit after the comparable band; got %v",
				idx, []uuid.UUID{ranked[0].Event.ID, ranked[1].Event.ID, ranked[2].Event.ID})
		}
		q := ranked[2]
		if q.Score != nil {
			t.Fatalf("input %d: quarantined event must carry a nil Score (never cross-currency compared)", idx)
		}
		if !q.Factors.ExposureKnown {
			t.Fatalf("input %d: quarantined event must KEEP ExposureKnown=true", idx)
		}
		if q.Factors.ExposureCurrency != "USD" {
			t.Fatalf("input %d: quarantined event must retain its own currency", idx)
		}
		seq := []uuid.UUID{ranked[0].Event.ID, ranked[1].Event.ID, ranked[2].Event.ID}
		if first == nil {
			first = seq
		} else {
			for i := range seq {
				if seq[i] != first[i] {
					t.Fatalf("input %d: order not deterministic across input permutations", idx)
				}
			}
		}
	}
}

// TestRankSingleCurrencyQuarantinesNothing proves that when only one currency is
// present, no event is quarantined (the majority IS canonical).
func TestRankSingleCurrencyQuarantinesNothing(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	a := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000601"), "warning", 10, "IRR", 0, 5000, 5000, base)
	b := mkEventCE(uuid.MustParse("00000000-0000-0000-0000-000000000602"), "warning", 20, "IRR", 0, 5000, 5000, base)
	for _, r := range event.Rank([]db.MarketEvent{a, b}) {
		if r.Score == nil {
			t.Fatalf("single-currency known events must all be comparable (non-nil score)")
		}
	}
}

// TestRankTieBreakIsTotal proves the tie-break is a total order: identical factors
// break by severity, then first-detected time, then id — never nondeterministic.
func TestRankTieBreakIsTotal(t *testing.T) {
	early := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	// Same factors, different (severity, time). Higher severity then older first.
	a := mkEvent(uuid.MustParse("00000000-0000-0000-0000-000000000f01"), "warning", true, 100, 5000, 5000, late)
	b := mkEvent(uuid.MustParse("00000000-0000-0000-0000-000000000f02"), "critical", true, 100, 5000, 5000, late)
	c := mkEvent(uuid.MustParse("00000000-0000-0000-0000-000000000f03"), "critical", true, 100, 5000, 5000, early)

	ranked := event.Rank([]db.MarketEvent{a, b, c})
	// critical+early (c), then critical+late (b), then warning (a).
	if ranked[0].Event.ID != c.ID || ranked[1].Event.ID != b.ID || ranked[2].Event.ID != a.ID {
		t.Fatalf("tie-break order wrong: %v", []uuid.UUID{ranked[0].Event.ID, ranked[1].Event.ID, ranked[2].Event.ID})
	}
}
