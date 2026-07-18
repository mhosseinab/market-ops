package event

import (
	"math/big"
	"sort"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// RankFactors exposes the three EVT-004 ranking inputs for one event, so the UI
// can show WHY an event ranks where it does. Exposure is reported as a distinct
// Known flag plus its Money triple — never coerced to a number when unknown
// (EVT-005). Confidence and Urgency are basis points (0..10000).
type RankFactors struct {
	ExposureKnown    bool
	ExposureMantissa int64
	ExposureCurrency string
	ExposureExponent int16
	ConfidenceBp     int32
	UrgencyBp        int32
}

// Ranked is one event placed in the deterministic Today order (EVT-004). Rank is
// 1-based. Score is the composite exposure×confidence×urgency magnitude for a
// KNOWN-exposure event and is nil for an unknown-exposure event (which is ranked
// in a separate band by confidence×urgency only — EVT-005, never a fabricated 0).
type Ranked struct {
	Event   db.MarketEvent
	Rank    int
	Factors RankFactors
	Score   *big.Int // nil ⇒ unknown exposure (ranked by confidence×urgency band)
}

// factorsOf lifts the persisted ranking factors off an event row. It reads the
// exposure exactly as stored: unknown stays unknown, with no numeric fallback.
func factorsOf(e db.MarketEvent) RankFactors {
	f := RankFactors{
		ExposureKnown:    e.ExposureKnown,
		ExposureCurrency: e.ExposureCurrency,
		ExposureExponent: e.ExposureExponent,
		ConfidenceBp:     e.ConfidenceBp,
		UrgencyBp:        e.UrgencyBp,
	}
	if e.ExposureKnown && e.ExposureMantissa.Valid {
		f.ExposureMantissa = e.ExposureMantissa.Int64
	}
	return f
}

// knownScore is the composite ranking magnitude for a KNOWN-exposure event:
// exposureMantissa × confidenceBp × urgencyBp, computed in big.Int so it is exact
// and cannot overflow (no float ever touches the exposure/money path — EVT-004).
// A non-positive exposure mantissa clamps to a zero magnitude so a credit can
// never outrank a real loss.
func knownScore(f RankFactors) *big.Int {
	mant := f.ExposureMantissa
	if mant < 0 {
		mant = 0
	}
	score := big.NewInt(mant)
	score.Mul(score, big.NewInt(int64(f.ConfidenceBp)))
	score.Mul(score, big.NewInt(int64(f.UrgencyBp)))
	return score
}

// unknownScore ranks unknown-exposure events among themselves by confidence ×
// urgency (dimensionless). It is NEVER compared against a known-exposure score —
// unknown events form a separate, lower band so a missing impact is never treated
// as a numeric exposure (EVT-005).
func unknownScore(f RankFactors) int64 {
	return int64(f.ConfidenceBp) * int64(f.UrgencyBp)
}

// Rank orders events for the Today feed (EVT-004) with a DETERMINISTIC final rank.
// Known-exposure events come first, ordered by exposure×confidence×urgency; then
// unknown-exposure events, ordered by confidence×urgency. Ties break by severity
// (desc), then first-detected time (asc), then id (asc) — a total, stable order
// independent of the input order. The input slice is not mutated.
func Rank(events []db.MarketEvent) []Ranked {
	ranked := make([]Ranked, len(events))
	for i, e := range events {
		f := factorsOf(e)
		r := Ranked{Event: e, Factors: f}
		if f.ExposureKnown {
			r.Score = knownScore(f)
		}
		ranked[i] = r
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		// Band 1: known exposure ranks ahead of unknown.
		if a.Score == nil != (b.Score == nil) {
			return a.Score != nil
		}
		if a.Score != nil { // both known: compare composite magnitude desc
			if cmp := a.Score.Cmp(b.Score); cmp != 0 {
				return cmp > 0
			}
		} else { // both unknown: compare confidence×urgency desc
			as, bs := unknownScore(a.Factors), unknownScore(b.Factors)
			if as != bs {
				return as > bs
			}
		}
		return lessByTieBreak(a.Event, b.Event)
	})

	for i := range ranked {
		ranked[i].Rank = i + 1
	}
	return ranked
}

// lessByTieBreak is the deterministic final tie-break: severity desc, then
// first-detected time asc (older first), then id asc. It yields a total order so
// the rank is reproducible for identical factors.
func lessByTieBreak(a, b db.MarketEvent) bool {
	ar, br := Severity(a.Severity).Rank(), Severity(b.Severity).Rank()
	if ar != br {
		return ar > br
	}
	if !a.FirstDetectedAt.Equal(b.FirstDetectedAt) {
		return a.FirstDetectedAt.Before(b.FirstDetectedAt)
	}
	return a.ID.String() < b.ID.String()
}
