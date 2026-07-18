// Package analytics is the §18 event pipe: a typed emitter for the eleven event
// families plus the §17.3 cost counters. Two disciplines this package exists to
// hold:
//
//   - Envelope completeness by construction (§18, never-cut). EVERY event carries
//     the full envelope — organization, account, entity, locale, region, currency
//     contract version, source surface, and timestamp. Emit VALIDATES the envelope
//     before it writes; a missing field is rejected (ErrIncompleteEnvelope), never
//     persisted as a partial row. The analytics_events columns are NOT NULL too, so
//     completeness is enforced at both the type boundary and the storage boundary.
//   - Append-only. analytics_events is INSERT/SELECT only; this package issues no
//     UPDATE/DELETE. The stream is the production signal the §18 dashboards read
//     (activation, WVRA, unit economics, …) — never a derived estimate.
//
// LOCALIZATION (LOC-001): locale/region/currency-contract are DATA carried on the
// envelope, never a branch in this package's logic. No locale copy is ever logged
// as a diagnostic identifier.
//
// MONEY (§9.1): cost counters are INTEGER minor units (int64); no float ever
// touches this pipe. Amounts placed in attributes are integer strings.
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// Family is one of the eleven §18 event families. The string is the persisted
// enum value (matched by the analytics_events CHECK) and the telemetry attribute.
type Family string

const (
	FamilyConnection     Family = "connection"     // connection and capability lifecycle
	FamilySync           Family = "sync"           // sync and import lifecycle
	FamilyMapping        Family = "mapping"        // mapping decisions
	FamilyObservation    Family = "observation"    // observation capture/quality/freshness/drift/route budget
	FamilyEvent          Family = "event"          // event lifecycle and relevance feedback
	FamilyRecommendation Family = "recommendation" // recommendation and simulation
	FamilyApproval       Family = "approval"       // approval card lifecycle and invalidation
	FamilyExecution      Family = "execution"      // execution/reconciliation/recommend-only/outcome
	FamilyConversation   Family = "conversation"   // conversation/intent/context/tool/grounding/deep-link/cost
	FamilyBriefing       Family = "briefing"       // briefing generation/open
	FamilyExtension      Family = "extension"      // extension capture/watchlist/circuit stop/queue
)

// AllFamilies is the closed §18 set, in declaration order. The envelope-
// completeness test samples EVERY family from this list.
var AllFamilies = []Family{
	FamilyConnection, FamilySync, FamilyMapping, FamilyObservation, FamilyEvent,
	FamilyRecommendation, FamilyApproval, FamilyExecution, FamilyConversation,
	FamilyBriefing, FamilyExtension,
}

// Valid reports whether f is one of the eleven families.
func (f Family) Valid() bool {
	switch f {
	case FamilyConnection, FamilySync, FamilyMapping, FamilyObservation, FamilyEvent,
		FamilyRecommendation, FamilyApproval, FamilyExecution, FamilyConversation,
		FamilyBriefing, FamilyExtension:
		return true
	default:
		return false
	}
}

// CostKind is one of the §17.3 variable-cost counters. Every counter is a granular,
// mandatory unit-economics signal; the amount is INTEGER minor units (no float).
type CostKind string

const (
	CostAccount          CostKind = "account"
	CostManagedSKU       CostKind = "managed_sku"
	CostTarget           CostKind = "target"
	CostObservation      CostKind = "successful_fresh_observation"
	CostBriefing         CostKind = "briefing"
	CostConversation     CostKind = "conversation"
	CostSimulation       CostKind = "simulation"
	CostApprovalFlow     CostKind = "approval_flow"
	CostExecutionAttempt CostKind = "execution_attempt"
)

// Valid reports whether k is one of the §17.3 cost kinds.
func (k CostKind) Valid() bool {
	switch k {
	case CostAccount, CostManagedSKU, CostTarget, CostObservation, CostBriefing,
		CostConversation, CostSimulation, CostApprovalFlow, CostExecutionAttempt:
		return true
	default:
		return false
	}
}

// Envelope is the §18 event envelope every analytics event carries. All eight
// fields are mandatory; Validate rejects any that is unset. Locale/region/currency
// are plain data here — never branched on (LOC-001).
type Envelope struct {
	Organization            uuid.UUID
	Account                 uuid.UUID
	Entity                  uuid.UUID
	Locale                  string
	Region                  string
	CurrencyContractVersion string
	SourceSurface           string
	Timestamp               time.Time
}

// ErrIncompleteEnvelope is returned when an event or cost record is missing any
// mandatory envelope field. A missing field is a bug (§18), never a partial write.
var ErrIncompleteEnvelope = errors.New("analytics: incomplete event envelope")

// ErrInvalidFamily is returned when an event names a family outside the §18 set.
var ErrInvalidFamily = errors.New("analytics: invalid event family")

// ErrInvalidCostKind is returned when a cost record names an unknown §17.3 kind.
var ErrInvalidCostKind = errors.New("analytics: invalid cost kind")

// Validate returns nil only when every mandatory envelope field is present. The
// error names the FIRST missing field so a misuse is actionable.
func (e Envelope) Validate() error {
	switch {
	case e.Organization == uuid.Nil:
		return fmt.Errorf("%w: organization", ErrIncompleteEnvelope)
	case e.Account == uuid.Nil:
		return fmt.Errorf("%w: account", ErrIncompleteEnvelope)
	case e.Entity == uuid.Nil:
		return fmt.Errorf("%w: entity", ErrIncompleteEnvelope)
	case e.Locale == "":
		return fmt.Errorf("%w: locale", ErrIncompleteEnvelope)
	case e.Region == "":
		return fmt.Errorf("%w: region", ErrIncompleteEnvelope)
	case e.CurrencyContractVersion == "":
		return fmt.Errorf("%w: currency_contract_version", ErrIncompleteEnvelope)
	case e.SourceSurface == "":
		return fmt.Errorf("%w: source_surface", ErrIncompleteEnvelope)
	case e.Timestamp.IsZero():
		return fmt.Errorf("%w: timestamp", ErrIncompleteEnvelope)
	default:
		return nil
	}
}

// Event is one §18 analytics event: the full envelope plus a family, a stable
// name within that family, and JSON-safe attributes (string values only — no
// money float ever enters the pipe).
type Event struct {
	Envelope
	Family     Family
	Name       string
	Attributes map[string]string
}

// Emitter writes §18 events to analytics_events and increments the matching OTel
// counters (the "same pipe" the cost counters ride). A nil pool yields a counter-
// only emitter (telemetry without persistence), for callers that only meter.
type Emitter struct {
	pool *pgxpool.Pool
	tel  *telemetry
}

// NewEmitter builds an emitter over the pool and the global OTel meter.
func NewEmitter(pool *pgxpool.Pool) *Emitter {
	return &Emitter{pool: pool, tel: newTelemetry()}
}

// Emit validates the envelope, persists the event append-only, and increments the
// per-family OTel counter. It FAILS CLOSED on an incomplete envelope or unknown
// family — a partial event is never written. When the emitter has no pool it only
// meters (still validated), so a metrics-only wiring cannot smuggle a bad envelope.
func (em *Emitter) Emit(ctx context.Context, ev Event) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	if !ev.Family.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidFamily, ev.Family)
	}
	if ev.Name == "" {
		return fmt.Errorf("%w: name", ErrIncompleteEnvelope)
	}
	attrs, err := marshalAttributes(ev.Attributes)
	if err != nil {
		return err
	}
	if em.pool != nil {
		if _, err := db.New(em.pool).InsertAnalyticsEvent(ctx, db.InsertAnalyticsEventParams{
			OrganizationID:          ev.Organization,
			MarketplaceAccountID:    ev.Account,
			EntityID:                ev.Entity,
			Locale:                  ev.Locale,
			Region:                  ev.Region,
			CurrencyContractVersion: ev.CurrencyContractVersion,
			SourceSurface:           ev.SourceSurface,
			OccurredAt:              ev.Timestamp.UTC(),
			Family:                  string(ev.Family),
			Name:                    ev.Name,
			Attributes:              attrs,
		}); err != nil {
			return fmt.Errorf("analytics: insert %s/%s: %w", ev.Family, ev.Name, err)
		}
	}
	em.tel.event(ctx, ev.Envelope, ev.Family, ev.Name)
	return nil
}

// RecordCost increments a §17.3 variable-cost counter by an INTEGER amount of
// minor currency units (no float, §9.1). It validates the envelope (cost must be
// attributed to a real account/org) and the kind, then meters. Cost is a counter
// signal on the same pipe — it is not persisted as an event row (the §18 families
// are the persisted stream; cost rides the metric channel for the unit-economics
// dashboard). A negative amount is rejected (a cost is never negative).
func (em *Emitter) RecordCost(ctx context.Context, env Envelope, kind CostKind, minorUnits int64) error {
	if err := env.Validate(); err != nil {
		return err
	}
	if !kind.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidCostKind, kind)
	}
	if minorUnits < 0 {
		return fmt.Errorf("analytics: negative cost amount for %q", kind)
	}
	em.tel.cost(ctx, env, kind, minorUnits)
	return nil
}

// marshalAttributes renders the JSON-safe attribute map. A nil/empty map becomes
// an empty object so the column default and the wire shape agree.
func marshalAttributes(a map[string]string) ([]byte, error) {
	if len(a) == 0 {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("analytics: marshal attributes: %w", err)
	}
	return b, nil
}
