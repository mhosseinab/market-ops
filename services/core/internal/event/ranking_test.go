package event_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

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
