// Package event implements the P0 market-event engine and Today ranking (PRD
// §7.4 EVT-001..005). It owns:
//
//   - The five P0 event TYPES (EVT-001), each with a fixture-covered trigger,
//     materiality, severity, expiry, and resolution: winning state lost/
//     challenged, qualifying competitor price movement, seller-count movement,
//     suppression/boundary change, and owned/proposed price below the
//     contribution floor. The floor detector CONSUMES the S16 margin/policy
//     outputs when present and otherwise stays DORMANT behind cost readiness — it
//     never fabricates a floor when readiness is not Complete.
//   - Versioned, category-specific materiality thresholds (EVT-002): a detector
//     fires against the in-force threshold and records the version, so a
//     historical event reproduces the exact threshold that triggered it.
//   - Type-specific DEDUP (EVT-003, §16 never-cut): a repeated signal UPDATES the
//     one open record — it never creates a duplicate Today item. The one-open-per-
//     dedup-key guarantee is structural (a partial unique index) plus enforced in
//     the service's Record path; the negative test proves zero duplicate rows.
//   - Today RANKING = exposure × confidence × urgency (EVT-004): all three factors
//     are exposed and the final rank is deterministic with a stable tie-break.
//   - UNKNOWN-impact handling (EVT-005): a missing sales/cost context never becomes
//     a numeric exposure — Exposure's zero value is Unknown, distinct from a zero
//     amount, and ranking never coerces it into a number.
//
// MONEY (PRD §9.1): exposure is a money.Money (never a float); competitor price
// signals stay quarantined as raw evidence, and price MOVEMENT is a dimensionless
// basis-point comparison of same-unit raw tokens, never an authoritative amount.
// Evidence is cited with its observed quality state AS-IS (never upgraded).
package event

import (
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Type is one of the five P0 event types (EVT-001). The string is the persisted
// enum value and the wire token.
type Type string

const (
	// TypeWinningState — the owned offer lost or is challenged for the winning
	// (buy-box) position.
	TypeWinningState Type = "winning_state"
	// TypeCompetitorPrice — a qualifying competitor price movement (materiality by
	// the versioned move_bp threshold).
	TypeCompetitorPrice Type = "competitor_price"
	// TypeSellerCount — a material change in the number of competing sellers.
	TypeSellerCount Type = "seller_count"
	// TypeSuppressionBoundary — the owned offer became suppressed, or the
	// marketplace price boundary changed.
	TypeSuppressionBoundary Type = "suppression_boundary"
	// TypeContributionFloor — the owned/proposed price sits below the contribution
	// floor. Dormant unless cost readiness is Complete (consumes S16 outputs).
	TypeContributionFloor Type = "contribution_floor"
)

// Valid reports whether t is one of the five P0 types.
func (t Type) Valid() bool {
	switch t {
	case TypeWinningState, TypeCompetitorPrice, TypeSellerCount,
		TypeSuppressionBoundary, TypeContributionFloor:
		return true
	default:
		return false
	}
}

// Severity is the closed, ordered severity set. Rank turns it into a deterministic
// integer for ranking tie-breaks (higher = more severe).
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Rank maps severity to a deterministic ordinal (info<warning<critical). An
// unknown severity ranks below everything (-1) so it can never outrank a known one.
func (s Severity) Rank() int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return -1
	}
}

// Lifecycle is the §15.1 market-event lifecycle state.
type Lifecycle string

const (
	LifecycleOpen     Lifecycle = "open"
	LifecycleUpdated  Lifecycle = "updated"
	LifecycleResolved Lifecycle = "resolved"
	LifecycleExpired  Lifecycle = "expired"
)

// Quality mirrors the six §10.3 observation quality states. An event CITES the
// observed quality as-is (never upgraded, evidence-quality never-cut). It is a
// distinct type so the confidence mapping is centralized here.
type Quality string

const (
	QualityVerified    Quality = "verified"
	QualitySupported   Quality = "supported"
	QualityUnverified  Quality = "unverified"
	QualityConflicted  Quality = "conflicted"
	QualityStale       Quality = "stale"
	QualityUnavailable Quality = "unavailable"
)

// Valid reports whether q is one of the six states.
func (q Quality) Valid() bool {
	switch q {
	case QualityVerified, QualitySupported, QualityUnverified,
		QualityConflicted, QualityStale, QualityUnavailable:
		return true
	default:
		return false
	}
}

// Exposure is an event's business impact. It is EITHER a KNOWN money.Money amount
// (derived from margin/sales context) OR explicitly UNKNOWN. The zero value is
// Unknown — a deliberate EVT-005 safety default: a caller that omits exposure gets
// Unknown, never a fabricated zero. Ranking treats Unknown distinctly and never
// turns it into a number.
type Exposure struct {
	known  bool
	amount money.Money
}

// KnownExposure wraps a computed money amount as a known exposure. Callers build
// it only from real margin/contribution figures (see the contribution-floor
// detector), so a number always traces to actual economics.
func KnownExposure(amount money.Money) Exposure {
	return Exposure{known: true, amount: amount}
}

// UnknownExposure is the explicit "impact unknown" value (EVT-005). It is the same
// as the zero value; the constructor exists so call sites read intentionally.
func UnknownExposure() Exposure { return Exposure{} }

// Known reports whether a numeric exposure exists.
func (e Exposure) Known() bool { return e.known }

// Amount returns the exposure money and true when known; the zero Money and false
// when unknown. Callers must check the boolean — there is no "0" fallback.
func (e Exposure) Amount() (money.Money, bool) {
	if !e.known {
		return money.Money{}, false
	}
	return e.amount, true
}

// Evidence is the observation an event cites. Quality is the observed §10.3 state
// AS-IS (never upgraded). Detail holds raw before/after tokens verbatim (money
// quarantine — never a Money), used for the audit-quality evidence display.
type Evidence struct {
	ObservationID uuid.UUID
	Quality       Quality
	Ref           string
	Detail        map[string]string
}

// Consumption binds a candidate to the durable observation-consumption seam
// (issue #212). It carries the input-transition identity (prev+curr observation
// evidence) plus the per-stream cursor position, so RecordFor can commit the event
// write, the append-only ingestion-idempotency claim, and the durable cursor
// advance in ONE transaction. It is set ONLY for competitor-price candidates
// derived from the ObservationSource; a nil Consumption means "not consumed from an
// observation stream" and RecordFor persists the event without touching the ledger
// or cursor (the four dormant legs and direct callers).
type Consumption struct {
	// InputKey is the deterministic ingestion-idempotency identity of the consumed
	// transition: target|native_seller_id|offer_identity|prevObsID|currObsID.
	InputKey       string
	Account        uuid.UUID
	Target         uuid.UUID
	NativeSellerID string
	OfferIdentity  string
	PrevObsID      uuid.UUID
	CurrObsID      uuid.UUID
	CurrCapturedAt time.Time
	// CurrValue is the raw quarantined price token of the newer observation — the
	// cursor's next pairing anchor ("before"). Never a Money.
	CurrValue string
}

// Candidate is a detector's output: a fully-derived event ready to be recorded.
// Confidence and Urgency are derived inside the detector (from evidence quality
// and severity respectively) so the EVT-004 factor derivation lives in one place.
// A detector returns (Candidate, true) only when the trigger is material.
type Candidate struct {
	Type             Type
	Variant          uuid.UUID
	Target           uuid.UUID // zero when the event has no observation target
	DedupKey         string
	Severity         Severity
	Exposure         Exposure
	Confidence       money.BasisPoints
	Urgency          money.BasisPoints
	Evidence         Evidence
	DetectedAt       time.Time
	ExpiresAt        time.Time
	ThresholdID      uuid.UUID // zero when no versioned threshold governs the type
	ThresholdVersion int32
	// Consumption, when non-nil, binds this candidate to the durable observation-
	// consumption seam (issue #212): RecordFor commits the append-only ingestion-
	// idempotency ledger row and the durable per-stream cursor advance in the SAME
	// transaction as the event write. nil for candidates not derived from an
	// observation stream.
	Consumption *Consumption
}
