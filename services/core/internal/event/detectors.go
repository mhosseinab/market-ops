package event

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Threshold is the in-force, versioned materiality configuration a detector fires
// against (EVT-002). ID/Version are recorded on the event so the trigger is
// reproducible. Only the knobs relevant to a type are read; the rest are ignored.
type Threshold struct {
	ID                uuid.UUID
	Version           int32
	MoveBp            money.BasisPoints // competitor price movement threshold
	SellerCountDelta  int               // seller-count movement threshold
	ChallengeMarginBp money.BasisPoints // winning-state "challenged" proximity
}

// confidenceOf maps an observed evidence quality to the confidence ranking factor
// (EVT-004), in basis points. Conflicted/stale/unavailable evidence yields a low
// confidence — the event still surfaces, but ranks below well-corroborated ones.
// The quality is NEVER upgraded here; this only weights ranking.
func confidenceOf(q Quality) money.BasisPoints {
	switch q {
	case QualityVerified:
		return money.NewBasisPoints(10000)
	case QualitySupported:
		return money.NewBasisPoints(8000)
	case QualityStale:
		return money.NewBasisPoints(3000)
	case QualityUnverified:
		return money.NewBasisPoints(4000)
	case QualityConflicted:
		return money.NewBasisPoints(2000)
	case QualityUnavailable:
		return money.NewBasisPoints(1000)
	default:
		return money.NewBasisPoints(0)
	}
}

// urgencyOf maps severity to the urgency ranking factor (EVT-004), in basis
// points. It is deterministic in severity so ranking is reproducible.
func urgencyOf(s Severity) money.BasisPoints {
	switch s {
	case SeverityCritical:
		return money.NewBasisPoints(10000)
	case SeverityWarning:
		return money.NewBasisPoints(6000)
	case SeverityInfo:
		return money.NewBasisPoints(3000)
	default:
		return money.NewBasisPoints(0)
	}
}

// dedupKey builds a type-specific dedup identity (EVT-003). The scope string
// distinguishes the sub-identity a type dedups on (e.g. a competitor offer). The
// key is stable across repeated detections of the same condition, so a duplicate
// collides on the open record instead of creating a new one.
func dedupKey(t Type, variant uuid.UUID, scope string) string {
	key := string(t) + ":" + variant.String()
	if scope != "" {
		key += ":" + scope
	}
	return key
}

// candidate fills the derived ranking factors (confidence from evidence quality,
// urgency from severity) so every detector produces them identically.
func candidate(
	t Type, variant, target uuid.UUID, scope string, sev Severity,
	exposure Exposure, ev Evidence, now time.Time, ttl time.Duration, thr Threshold,
) Candidate {
	return Candidate{
		Type:             t,
		Variant:          variant,
		Target:           target,
		DedupKey:         dedupKey(t, variant, scope),
		Severity:         sev,
		Exposure:         exposure,
		Confidence:       confidenceOf(ev.Quality),
		Urgency:          urgencyOf(sev),
		Evidence:         ev,
		DetectedAt:       now,
		ExpiresAt:        now.Add(ttl),
		ThresholdID:      thr.ID,
		ThresholdVersion: thr.Version,
	}
}

// --- (1) Winning state lost/challenged ------------------------------------

// WinningStateInput is the resolved owned-vs-competitor winning state. Exposure is
// supplied by the caller (from margin/sales context) or left Unknown (EVT-005):
// the detector never invents an impact number.
type WinningStateInput struct {
	Variant    uuid.UUID
	Target     uuid.UUID
	WasWinning bool
	IsWinning  bool
	Challenged bool // still winning but a competitor is within the challenge margin
	Exposure   Exposure
	Evidence   Evidence
	Now        time.Time
	TTL        time.Duration
	Threshold  Threshold
}

// DetectWinningState fires when the owned offer LOST the winning position
// (critical) or is CHALLENGED while still winning (warning). Steady winning or
// steady non-winning is not material.
func DetectWinningState(in WinningStateInput) (Candidate, bool) {
	switch {
	case in.WasWinning && !in.IsWinning:
		return candidate(TypeWinningState, in.Variant, in.Target, "", SeverityCritical,
			in.Exposure, in.Evidence, in.Now, in.TTL, in.Threshold), true
	case in.IsWinning && in.Challenged:
		return candidate(TypeWinningState, in.Variant, in.Target, "", SeverityWarning,
			in.Exposure, in.Evidence, in.Now, in.TTL, in.Threshold), true
	default:
		return Candidate{}, false
	}
}

// --- (2) Qualifying competitor price movement -----------------------------

// CompetitorPriceInput carries two SAME-UNIT raw price tokens (money quarantine —
// never a Money). PrevValue/CurrValue are the digit-normalized integer tokens
// (LOC-007). If the unit is empty or the tokens don't parse, movement is
// unknowable and the detector does not fire (it never guesses).
type CompetitorPriceInput struct {
	Variant       uuid.UUID
	Target        uuid.UUID
	OfferIdentity string
	PrevValue     string
	CurrValue     string
	Unit          string
	Exposure      Exposure
	Evidence      Evidence
	Now           time.Time
	TTL           time.Duration
	Threshold     Threshold
}

// DetectCompetitorPrice fires when a competitor's price moved by at least the
// versioned move_bp threshold (EVT-002). The movement is a dimensionless
// basis-point ratio of same-unit raw tokens — no Money, no float. A movement of
// twice the threshold or more is critical; otherwise warning.
func DetectCompetitorPrice(in CompetitorPriceInput) (Candidate, bool) {
	moveBp, ok := movementBasisPoints(in.PrevValue, in.CurrValue, in.Unit)
	if !ok {
		return Candidate{}, false
	}
	threshold := in.Threshold.MoveBp.Value()
	if threshold <= 0 || moveBp < threshold {
		return Candidate{}, false
	}
	sev := SeverityWarning
	if moveBp >= 2*threshold {
		sev = SeverityCritical
	}
	ev := in.Evidence
	if ev.Detail == nil {
		ev.Detail = map[string]string{}
	}
	// Preserve the raw before/after tokens verbatim (money quarantine).
	ev.Detail["prev_value"] = in.PrevValue
	ev.Detail["curr_value"] = in.CurrValue
	ev.Detail["unit"] = in.Unit
	ev.Detail["move_bp"] = strconv.FormatInt(moveBp, 10)
	return candidate(TypeCompetitorPrice, in.Variant, in.Target, in.OfferIdentity, sev,
		in.Exposure, ev, in.Now, in.TTL, in.Threshold), true
}

// movementBasisPoints returns |curr-prev|/prev in basis points using integer
// arithmetic only (no float). It reports ok=false when the unit is empty, the
// tokens are not integers, or prev is zero (an undefined ratio) — the detector
// then simply does not fire rather than fabricate a movement.
func movementBasisPoints(prev, curr, unit string) (int64, bool) {
	if strings.TrimSpace(unit) == "" {
		return 0, false
	}
	p, err := strconv.ParseInt(strings.TrimSpace(prev), 10, 64)
	if err != nil || p <= 0 {
		return 0, false
	}
	c, err := strconv.ParseInt(strings.TrimSpace(curr), 10, 64)
	if err != nil || c < 0 {
		return 0, false
	}
	diff := c - p
	if diff < 0 {
		diff = -diff
	}
	// basis points = diff * 10000 / prev, integer division (toward zero).
	return diff * 10000 / p, true
}

// --- (3) Seller-count movement --------------------------------------------

// SellerCountInput carries the previous and current competing-seller counts.
type SellerCountInput struct {
	Variant   uuid.UUID
	Target    uuid.UUID
	PrevCount int
	CurrCount int
	Exposure  Exposure
	Evidence  Evidence
	Now       time.Time
	TTL       time.Duration
	Threshold Threshold
}

// DetectSellerCount fires when the seller count changed by at least the versioned
// seller_count_delta threshold (EVT-002). A change of twice the threshold or more
// is critical; otherwise warning.
func DetectSellerCount(in SellerCountInput) (Candidate, bool) {
	threshold := in.Threshold.SellerCountDelta
	if threshold <= 0 {
		return Candidate{}, false
	}
	delta := in.CurrCount - in.PrevCount
	if delta < 0 {
		delta = -delta
	}
	if delta < threshold {
		return Candidate{}, false
	}
	sev := SeverityWarning
	if delta >= 2*threshold {
		sev = SeverityCritical
	}
	ev := in.Evidence
	if ev.Detail == nil {
		ev.Detail = map[string]string{}
	}
	ev.Detail["prev_count"] = strconv.Itoa(in.PrevCount)
	ev.Detail["curr_count"] = strconv.Itoa(in.CurrCount)
	return candidate(TypeSellerCount, in.Variant, in.Target, "", sev,
		in.Exposure, ev, in.Now, in.TTL, in.Threshold), true
}

// --- (4) Suppression / boundary change ------------------------------------

// SuppressionBoundaryInput carries the owned-offer suppression state and whether
// the marketplace price boundary changed. Either transition is material.
type SuppressionBoundaryInput struct {
	Variant         uuid.UUID
	Target          uuid.UUID
	WasSuppressed   bool
	IsSuppressed    bool
	BoundaryChanged bool
	Exposure        Exposure
	Evidence        Evidence
	Now             time.Time
	TTL             time.Duration
	Threshold       Threshold
}

// DetectSuppressionBoundary fires when the owned offer became suppressed
// (critical — it cannot sell) or the price boundary changed (warning — executable
// range shifted). A steady state with no boundary change is not material.
func DetectSuppressionBoundary(in SuppressionBoundaryInput) (Candidate, bool) {
	switch {
	case !in.WasSuppressed && in.IsSuppressed:
		return candidate(TypeSuppressionBoundary, in.Variant, in.Target, "", SeverityCritical,
			in.Exposure, in.Evidence, in.Now, in.TTL, in.Threshold), true
	case in.BoundaryChanged:
		return candidate(TypeSuppressionBoundary, in.Variant, in.Target, "", SeverityWarning,
			in.Exposure, in.Evidence, in.Now, in.TTL, in.Threshold), true
	default:
		return Candidate{}, false
	}
}

// --- (5) Owned/proposed price below contribution floor --------------------

// ContributionFloorInput consumes the S16 margin/policy outputs. It is DORMANT
// unless cost readiness is Complete (do not fabricate a floor when readiness is
// not Complete) and a contribution was actually computed. When it fires, exposure
// is KNOWN and equals the shortfall below the floor — real economics, never a
// fabricated number.
type ContributionFloorInput struct {
	Variant         uuid.UUID
	Target          uuid.UUID
	Readiness       cost.State
	HasContribution bool
	Contribution    money.Money
	Floor           money.Money
	Evidence        Evidence
	Now             time.Time
	TTL             time.Duration
}

// DetectContributionFloor fires when the owned/proposed contribution is below the
// hard floor, but ONLY when cost readiness is Complete and a contribution exists
// (otherwise it stays dormant — EVT-001 "consumes S16 outputs when present, else
// dormant behind readiness"). Exposure = floor − contribution (the shortfall),
// a KNOWN money value. A non-positive contribution (crosses zero) is critical.
func DetectContributionFloor(in ContributionFloorInput) (Candidate, bool, error) {
	// Dormant behind readiness: only Complete drives this detector. Anything else
	// (Partial/Stale/Missing) means we do not have an authoritative contribution to
	// compare, so we NEVER fabricate a floor breach.
	if in.Readiness != cost.StateComplete || !in.HasContribution {
		return Candidate{}, false, nil
	}
	belowFloor, err := in.Contribution.Compare(in.Floor)
	if err != nil {
		return Candidate{}, false, err
	}
	if belowFloor >= 0 {
		return Candidate{}, false, nil // at or above floor — not material
	}
	// Shortfall = floor − contribution (positive since contribution < floor).
	shortfall, err := in.Floor.Sub(in.Contribution)
	if err != nil {
		return Candidate{}, false, err
	}
	zero, err := money.Zero(in.Contribution.Currency(), in.Contribution.Exponent())
	if err != nil {
		return Candidate{}, false, err
	}
	crossesZero, err := in.Contribution.Compare(zero)
	if err != nil {
		return Candidate{}, false, err
	}
	sev := SeverityWarning
	if crossesZero <= 0 {
		sev = SeverityCritical
	}
	return candidate(TypeContributionFloor, in.Variant, in.Target, "", sev,
		KnownExposure(shortfall), in.Evidence, in.Now, in.TTL, Threshold{}), true, nil
}
