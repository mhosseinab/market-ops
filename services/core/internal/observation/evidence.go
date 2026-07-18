package observation

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// ErrIncompleteEvidence is returned when a capture is missing an OBS-002 required
// field. Schema validation REJECTS incomplete evidence — it is never accepted and
// silently downgraded.
var ErrIncompleteEvidence = errors.New("observation: incomplete evidence")

// Route is the capture route provenance (PRD §10.1). The roles are fixed: A is
// the official connector, C carries P0 competitor freshness, B is corroboration /
// opportunistic refresh only.
type Route string

const (
	RouteA Route = "route_a" // official connector (owned)
	RouteB Route = "route_b" // extension (corroboration only)
	RouteC Route = "route_c" // server observation (competitor freshness)
)

func (r Route) valid() bool { return r == RouteA || r == RouteB || r == RouteC }

// SourceType is the observation envelope source discriminator (docs/08).
type SourceType string

const (
	SourcePublicWebEndpoint SourceType = "public-web-endpoint"
	SourceEmbeddedJSON      SourceType = "embedded-json"
	SourceDOM               SourceType = "dom"
	SourceUserTriggered     SourceType = "user-triggered-request"
	SourceOfficialAPI       SourceType = "official-api"
)

func (s SourceType) valid() bool {
	switch s {
	case SourcePublicWebEndpoint, SourceEmbeddedJSON, SourceDOM, SourceUserTriggered, SourceOfficialAPI:
		return true
	default:
		return false
	}
}

// Availability is the normalized availability status (docs/11). 'unavailable' is
// the DISTINCT temporary-out state (§16, "no assumed permanent removal");
// 'disappeared' is the permanent close (§16, "close with end time, never zero").
type Availability string

const (
	InStock     Availability = "in_stock"
	OutOfStock  Availability = "out_of_stock"
	Limited     Availability = "limited"
	TempUnavail Availability = "unavailable"
	Disappeared Availability = "disappeared"
)

func (a Availability) valid() bool {
	switch a {
	case InStock, OutOfStock, Limited, TempUnavail, Disappeared:
		return true
	default:
		return false
	}
}

// hasValue reports whether an availability state carries a usable current offer
// value. A disappeared offer has no current value (§16).
func (a Availability) hasValue() bool { return a != Disappeared }

// Confidence is the capture parser/unit confidence (docs/08). Only 'verified' and
// 'partially_verified' qualify; 'unverified' degrades the state to Unverified.
type Confidence string

const (
	ConfVerified          Confidence = "verified"
	ConfPartiallyVerified Confidence = "partially_verified"
	ConfUnverified        Confidence = "unverified"
)

func (c Confidence) valid() bool {
	return c == ConfVerified || c == ConfPartiallyVerified || c == ConfUnverified
}

func (c Confidence) low() bool { return c != ConfVerified && c != ConfPartiallyVerified }

// Tier is a target's cadence/freshness tier (PRD §10.1, plan §4.5). Freshness
// windows are DATA, not a hardcoded branch: priority 60 min, standard 6 h,
// background 24 h.
type Tier string

const (
	TierPriority   Tier = "priority"
	TierStandard   Tier = "standard"
	TierBackground Tier = "background"
)

// tierWindows maps a tier to its cadence and freshness deadline window.
var tierWindows = map[Tier]struct {
	Cadence   time.Duration
	Freshness time.Duration
}{
	TierPriority:   {Cadence: 60 * time.Minute, Freshness: 60 * time.Minute},
	TierStandard:   {Cadence: 6 * time.Hour, Freshness: 6 * time.Hour},
	TierBackground: {Cadence: 24 * time.Hour, Freshness: 24 * time.Hour},
}

// TierWindow returns the cadence and freshness durations for a tier, defaulting
// to standard for an unknown tier (fail safe: a shorter-than-nothing window).
func TierWindow(t Tier) (cadence, freshness time.Duration) {
	w, ok := tierWindows[t]
	if !ok {
		w = tierWindows[TierStandard]
	}
	return w.Cadence, w.Freshness
}

// Capture is a single validated observation input (OBS-002). It carries the full
// evidence envelope. Price is money.RawAmount ONLY — never a Money (quarantine).
type Capture struct {
	TargetID uuid.UUID
	Account  uuid.UUID

	// Observed offer identity (OBS-002).
	NativeVariantID int64
	NativeSellerID  string
	// OfferIdentity is derived when empty (see canonicalOfferIdentity).
	OfferIdentity string

	Route      Route
	SubRoute   string
	SourceType SourceType
	SourceURL  string

	ParserVersion    string
	ConnectorVersion string
	EvidenceRef      string
	RawFixtureRef    string

	// Raw price evidence (money quarantine). Price is the effective price; ListPrice
	// is the pre-promotion list price with source semantics preserved (§16).
	Price     money.RawAmount
	ListPrice money.RawAmount

	Availability Availability
	StockSignal  *int64

	CapturedAt      time.Time
	Confidence      Confidence
	ParsingWarnings []string

	// SchemaValid is a SERVER-SIDE signal: the capture conformed to the parser's
	// schema / allow-listed contract. It is set by the trusted producer (the Route C
	// parser canary or the gateway after allow-list validation), NEVER by the remote
	// client. Identity validity and cross-route conflict are NOT capture fields: the
	// service derives identity validity from the target's native id (rejecting a
	// mismatch) and conflict from in-window append-only evidence.
	SchemaValid bool
}

// canonicalOfferIdentity is the stable per-offer key the current view is keyed on:
// the native variant id plus the seller. Native ids are LTR technical identifiers.
func canonicalOfferIdentity(nativeVariantID int64, sellerID string) string {
	return fmt.Sprintf("v%d:s%s", nativeVariantID, strings.TrimSpace(sellerID))
}

// resolvedOfferIdentity returns the capture's offer identity, deriving it from the
// native ids when not explicitly provided.
func (c Capture) resolvedOfferIdentity() string {
	if strings.TrimSpace(c.OfferIdentity) != "" {
		return c.OfferIdentity
	}
	return canonicalOfferIdentity(c.NativeVariantID, c.NativeSellerID)
}

// Validate rejects incomplete evidence (OBS-002). Every required envelope field
// must be present; a missing field returns ErrIncompleteEvidence with the list of
// what is missing. This is the gate the "schema validation rejects incomplete
// evidence" acceptance criterion drives.
func (c Capture) Validate() error {
	var missing []string
	if c.TargetID == uuid.Nil {
		missing = append(missing, "targetId")
	}
	if c.Account == uuid.Nil {
		missing = append(missing, "marketplaceAccountId")
	}
	if c.NativeVariantID == 0 {
		missing = append(missing, "nativeVariantId")
	}
	if !c.Route.valid() {
		missing = append(missing, "route")
	}
	if !c.SourceType.valid() {
		missing = append(missing, "sourceType")
	}
	if strings.TrimSpace(c.ParserVersion) == "" {
		missing = append(missing, "parserVersion")
	}
	if strings.TrimSpace(c.EvidenceRef) == "" {
		missing = append(missing, "evidenceRef")
	}
	if !c.Availability.valid() {
		missing = append(missing, "availabilityStatus")
	}
	if !c.Confidence.valid() {
		missing = append(missing, "confidence")
	}
	if c.CapturedAt.IsZero() {
		missing = append(missing, "capturedAt")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrIncompleteEvidence, strings.Join(missing, ", "))
	}
	return nil
}
