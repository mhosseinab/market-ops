package event

import (
	"context"
	"log/slog"
	"math/big"
	"sort"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

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

// exposureBand is the comparability class an event lands in. Bands are a TOTAL
// ordering key: comparable-known ranks ahead of quarantined-known, which ranks
// ahead of unknown. A magnitude Score is only ever compared WITHIN the comparable
// band (same currency, exponent-normalized) — never across bands (issue #71).
type exposureBand int

const (
	// bandComparable: known exposure in the canonical currency; carries an
	// exponent-normalized composite Score comparable to its peers.
	bandComparable exposureBand = iota
	// bandQuarantined: known exposure in a NON-canonical currency. Fail closed —
	// no cross-currency magnitude is invented; Score stays nil and it is ordered by
	// the deterministic tie-break only (money-correctness never-cut, PRD §9.1).
	bandQuarantined
	// bandUnknown: exposure is unknown (EVT-005); ordered by confidence×urgency.
	bandUnknown
)

// Ranked is one event placed in the deterministic Today order (EVT-004). Rank is
// 1-based. Score is the composite exposure×confidence×urgency magnitude for a
// KNOWN-exposure event in the canonical currency (exponent-normalized so equal
// real values tie exactly). Score is nil for an unknown-exposure event (EVT-005,
// never a fabricated 0) AND for a known-exposure event quarantined into a
// non-canonical currency band (issue #71 — never cross-currency compared).
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

// comparableScore is the composite ranking magnitude for a KNOWN-exposure event in
// the canonical currency: clamp(mantissa) × 10^(exponent − minExp) × confidenceBp ×
// urgencyBp, computed in big.Int so it is exact and cannot overflow. Because minExp
// is the MINIMUM exponent among the canonical-currency events, (exponent − minExp)
// is a NON-NEGATIVE integer, so scaling is a whole power-of-ten multiplication — no
// division, no float ever touches the exposure/money path (issue #71, PRD §9.1).
// Equal real values expressed at different exponents (1000@2 vs 100000@0) therefore
// scale to EXACTLY equal scores. A non-positive mantissa clamps to a zero magnitude
// BEFORE scaling so a credit can never outrank a real loss.
func comparableScore(f RankFactors, minExp int16) *big.Int {
	mant := f.ExposureMantissa
	if mant < 0 {
		mant = 0
	}
	score := big.NewInt(mant)
	if shift := int64(f.ExposureExponent) - int64(minExp); shift > 0 {
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(shift), nil)
		score.Mul(score, scale)
	}
	score.Mul(score, big.NewInt(int64(f.ConfidenceBp)))
	score.Mul(score, big.NewInt(int64(f.UrgencyBp)))
	return score
}

// canonicalCurrency picks the reference currency for the comparable band: the
// currency with the MOST known-exposure events, breaking ties by the smallest
// ISO-4217 code (lexicographic ascending) so the choice is deterministic and
// input-order independent. Returns ok=false when no event has a known exposure
// (nothing is comparable, nothing is quarantined).
func canonicalCurrency(events []db.MarketEvent) (string, bool) {
	counts := make(map[string]int)
	for _, e := range events {
		if e.ExposureKnown {
			counts[e.ExposureCurrency]++
		}
	}
	if len(counts) == 0 {
		return "", false
	}
	best, bestCount := "", -1
	for cur, c := range counts {
		if c > bestCount || (c == bestCount && cur < best) {
			best, bestCount = cur, c
		}
	}
	return best, true
}

// minCanonicalExponent is the minimum ExposureExponent among the known-exposure
// events sharing the canonical currency — the normalization floor that makes every
// (exponent − minExp) shift non-negative (issue #71).
func minCanonicalExponent(events []db.MarketEvent, canonical string) int16 {
	found := false
	var min int16
	for _, e := range events {
		if e.ExposureKnown && e.ExposureCurrency == canonical {
			if !found || e.ExposureExponent < min {
				min, found = e.ExposureExponent, true
			}
		}
	}
	return min
}

// unknownScore ranks unknown-exposure events among themselves by confidence ×
// urgency (dimensionless). It is NEVER compared against a known-exposure score —
// unknown events form a separate, lower band so a missing impact is never treated
// as a numeric exposure (EVT-005).
func unknownScore(f RankFactors) int64 {
	return int64(f.ConfidenceBp) * int64(f.UrgencyBp)
}

// Ranker orders events for the Today feed (EVT-004) and observes the money-
// correctness quarantine boundary (issue #71). It carries an instance logger and
// telemetry (no package-global singleton) so the observability seam is testable in
// isolation, mirroring newProducerTelemetry.
type Ranker struct {
	logger *slog.Logger
	tel    *rankingTelemetry
}

// NewRanker wires a Ranker with an instance logger + telemetry. A nil logger
// degrades to slog.Default (never nil-deref on the observability path).
func NewRanker(logger *slog.Logger) *Ranker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Ranker{
		logger: logger.With("component", "event_ranker"),
		tel:    newRankingTelemetry(),
	}
}

// Rank is the package-level entry point kept for existing callers. It delegates to
// a default Ranker (slog.Default) so behaviour is unchanged for callers that do not
// need to inject observability.
func Rank(events []db.MarketEvent) []Ranked {
	return NewRanker(nil).Rank(context.Background(), events)
}

// rankable is the internal sort carrier: a Ranked plus its comparability band, so
// the sort can enforce a total cross-band ordering without leaking the band onto
// the public Ranked type.
type rankable struct {
	Ranked
	band exposureBand
}

// Rank orders events for the Today feed (EVT-004) with a DETERMINISTIC final rank.
// Events are separated into three bands (comparable-known in the canonical currency,
// quarantined-known in a non-canonical currency, then unknown-exposure). Within the
// comparable band ordering is by the exponent-normalized exposure×confidence×urgency
// magnitude; within the unknown band by confidence×urgency; the quarantined band is
// ordered by the tie-break only (no invented cross-currency magnitude — issue #71).
// Ties break by severity (desc), first-detected time (asc), then id (asc) — a total,
// stable order independent of input order. The input slice is not mutated. Every
// quarantined event is counted on the telemetry counter and summarized on a warn log.
func (r *Ranker) Rank(ctx context.Context, events []db.MarketEvent) []Ranked {
	canonical, hasCanonical := canonicalCurrency(events)
	minExp := minCanonicalExponent(events, canonical)

	items := make([]rankable, len(events))
	quarantined := 0
	for i, e := range events {
		f := factorsOf(e)
		item := rankable{Ranked: Ranked{Event: e, Factors: f}}
		switch {
		case !f.ExposureKnown:
			item.band = bandUnknown
		case hasCanonical && f.ExposureCurrency == canonical:
			item.band = bandComparable
			item.Score = comparableScore(f, minExp)
		default:
			// Known exposure in a non-canonical currency: fail closed. Keep
			// ExposureKnown=true (factorsOf already set it), leave Score nil, and
			// order by tie-break only — never a fabricated cross-currency number.
			item.band = bandQuarantined
			quarantined++
		}
		items[i] = item
	}

	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.band != b.band {
			return a.band < b.band
		}
		switch a.band {
		case bandComparable:
			if cmp := a.Score.Cmp(b.Score); cmp != 0 {
				return cmp > 0
			}
		case bandUnknown:
			as, bs := unknownScore(a.Factors), unknownScore(b.Factors)
			if as != bs {
				return as > bs
			}
		}
		return lessByTieBreak(a.Event, b.Event)
	})

	out := make([]Ranked, len(items))
	for i := range items {
		items[i].Rank = i + 1
		out[i] = items[i].Ranked
	}

	if quarantined > 0 {
		r.tel.quarantinedCurrency.Add(ctx, int64(quarantined))
		r.logger.WarnContext(ctx, "event ranking quarantined non-canonical currency exposures",
			"canonical_currency", canonical, "quarantined", quarantined)
	}
	return out
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

// rankingTelemetry holds the OTel counter on the money-correctness ranking
// boundary: known-exposure events dropped from the comparable band because their
// currency is not canonical (a quarantine, PRD §9.1). Counter construction failures
// degrade to no-op so a telemetry hiccup never breaks ranking. Instance-scoped (no
// package-global singleton) so the emission seam is testable in isolation, mirroring
// newProducerTelemetry.
type rankingTelemetry struct {
	quarantinedCurrency metric.Int64Counter
}

func newRankingTelemetry() *rankingTelemetry {
	m := otel.Meter(producerInstrumentation)
	c, err := m.Int64Counter("event.ranking.quarantined_currency",
		metric.WithDescription("known-exposure events dropped from the comparable Today band because their currency is not the canonical one (money-correctness quarantine, PRD §9.1 / issue #71)"))
	if err != nil {
		c, _ = otel.Meter("noop").Int64Counter("event.ranking.quarantined_currency")
	}
	return &rankingTelemetry{quarantinedCurrency: c}
}
