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
	"github.com/jackc/pgx/v5"
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

// AccountLevel reports whether f's canonical entity IS the marketplace account itself
// (an account-lifecycle signal), so its envelope entity_id MUST equal its account and
// no sub-entity lookup is required or permitted (issue #125 reopen residual). The
// classification follows the family definitions above: connection/sync are
// account/connector-lifecycle families and briefing is the account-level daily digest
// (the sole production emitter, cmd/core), so their entity is the account. Every OTHER
// family is ENTITY-LEVEL: its entity is a sub-account resource whose ownership and
// family are resolved through an EntityResolver. Moving a family across this boundary
// is a deliberate, reviewed edit — never improvised — because it changes which guard
// (equality vs. resolver) authorizes the entity.
func (f Family) AccountLevel() bool {
	switch f {
	case FamilyConnection, FamilySync, FamilyBriefing:
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

// ErrCrossTenant is returned when an event envelope pairs an organization with a
// marketplace account that organization does NOT own (issue #125, §18 envelope +
// §4.6 tenant-integrity never-cut). The §18 envelope must identify ONE coherent
// tenant aggregate; a cross-tenant pairing is rejected server-side and never
// persisted. The rejection is fail-closed and carries only the caller-supplied
// account id — never the authoritative owning organization — so it cannot become an
// existence oracle for another tenant's account. An UNKNOWN account and a FOREIGN
// account are deliberately indistinguishable (same error, same detail).
var ErrCrossTenant = errors.New("analytics: event envelope pairs an account with a foreign organization")

// ErrEntityScope is returned when an event envelope's entity_id does not resolve to
// an entity OWNED by the envelope's account, or whose native family is INCOMPATIBLE
// with the event family (issue #125 reopen residual, §18 envelope + §4.6 tenant-
// integrity never-cut). The account->organization guard (ErrCrossTenant) proves the
// envelope names a coherent TENANT; this guard proves the envelope's ENTITY belongs
// to that tenant and matches the event family — a cross-account or wrong-family
// entity reference can otherwise ride inside an envelope whose org/account pair is
// perfectly valid. Like ErrCrossTenant the rejection is fail-closed and carries ONLY
// the caller-supplied entity id — never the entity's owning account or native family
// — so it cannot become an ownership or existence oracle for another tenant's entity.
// An UNKNOWN entity, a FOREIGN-account entity, and a FAMILY-mismatched entity are
// deliberately indistinguishable (same error, same detail).
var ErrEntityScope = errors.New("analytics: event envelope entity is not owned by the account or is incompatible with the family")

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

// store is the narrow persistence seam the emitter needs (ISP): resolve an
// account's AUTHORITATIVE owning organization, and append an event row. *db.Queries
// satisfies it; tests inject a double. Keeping this interface small keeps the
// tenant-integrity resolution unit-testable without a live Postgres.
type store interface {
	GetMarketplaceAccount(ctx context.Context, id uuid.UUID) (db.MarketplaceAccount, error)
	InsertAnalyticsEvent(ctx context.Context, arg db.InsertAnalyticsEventParams) (db.AnalyticsEvent, error)
}

// EntityScope is the AUTHORITATIVE tenant scope of an analytics entity_id: the account
// that OWNS the entity and the family that CLASSIFIES it. An EntityResolver returns it;
// Emit admits an entity-level event only when BOTH the owning account AND the
// classifying family match the envelope (issue #125 reopen residual).
type EntityScope struct {
	Account uuid.UUID
	Family  Family
}

// EntityResolver resolves an entity-LEVEL entity_id to its AUTHORITATIVE EntityScope
// (owning account + classifying family) via a per-family, account-bound lookup. It is
// the seam that closes the ENTITY half of tenant integrity: an entity that does not
// resolve returns pgx.ErrNoRows and Emit rejects it fail-closed; any other error is a
// genuine infrastructure failure surfaced as-is (never a tenant-reject signal). It is
// consumer-defined (ISP) and account/marketplace-agnostic — the Go core owns the
// contract; a DB-backed implementation is injected by the caller that activates an
// entity-level family (see WithEntityResolver). Account-LEVEL families never consult
// it (their entity is the account itself, checked by equality).
type EntityResolver interface {
	ResolveEntity(ctx context.Context, entityID uuid.UUID) (EntityScope, error)
}

// Emitter writes §18 events to analytics_events and increments the matching OTel
// counters (the "same pipe" the cost counters ride). A nil store yields a counter-
// only emitter (telemetry without persistence), for callers that only meter. A nil
// entities resolver leaves entity-LEVEL families fail-closed (they cannot be persisted
// until a resolver is wired); account-LEVEL families never need one.
type Emitter struct {
	store    store
	entities EntityResolver
	tel      *telemetry
}

// WithEntityResolver returns em wired with an EntityResolver that authorizes
// entity-LEVEL families (issue #125 reopen residual). Without it, an entity-level
// event fails closed at Emit — an explicitly-planned stub the caller activating such a
// family completes by injecting a per-family, account-bound resolver. Account-level
// families (Family.AccountLevel) do not require it.
func (em *Emitter) WithEntityResolver(r EntityResolver) *Emitter {
	em.entities = r
	return em
}

// NewEmitter builds an emitter over the pool and the global OTel meter. A nil pool
// yields a meter-only emitter (no persistence, no account resolution).
func NewEmitter(pool *pgxpool.Pool) *Emitter {
	var s store
	if pool != nil {
		s = db.New(pool)
	}
	return &Emitter{store: s, tel: newTelemetry()}
}

// newEmitterWithStore builds an emitter over an injected store (tests). It is the
// seam the tenant-integrity unit tests use to exercise account->org resolution and
// the cross-tenant fail-closed path without a live database.
func newEmitterWithStore(s store) *Emitter {
	return &Emitter{store: s, tel: newTelemetry()}
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
	if em.store != nil {
		// TENANT INTEGRITY (issue #125, §4.6 never-cut): resolve the AUTHORITATIVE
		// organization from the account row server-side and reject a disagreeing
		// supplied org, so a cross-tenant envelope can never be persisted. The row is
		// written with the RESOLVED org, never the blindly-trusted caller value. The
		// database's composite (marketplace_account_id, organization_id) foreign key
		// is the second, concurrency-safe guard (migration 0036) — both boundaries
		// reject an incoherent pair.
		org, err := em.resolveOwnerOrg(ctx, ev.Organization, ev.Account)
		if err != nil {
			return err
		}
		// TENANT INTEGRITY — ENTITY HALF (issue #125 reopen residual): the org/account
		// pair is now proven coherent; also prove the envelope's ENTITY belongs to that
		// account and matches the family BEFORE any row or event telemetry. A
		// cross-account or family-mismatched entity fails closed here (no row, no event
		// counter) — it can never ride inside an otherwise tenant-valid envelope.
		if err := em.validateEntityScope(ctx, ev.Envelope, ev.Family); err != nil {
			return err
		}
		if _, err := em.store.InsertAnalyticsEvent(ctx, db.InsertAnalyticsEventParams{
			OrganizationID:          org,
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

// resolveOwnerOrg returns the AUTHORITATIVE organization that owns account, and
// fails closed (ErrCrossTenant) when the account is unknown OR is owned by a
// DIFFERENT organization than the caller supplied. The two rejection cases are
// deliberately indistinguishable and expose only the caller-supplied account id —
// never the owning organization — so the error is not an existence oracle for
// another tenant's account. A genuine infrastructure error (not a tenant conflict)
// is surfaced as-is, without the tenant-reject signal.
func (em *Emitter) resolveOwnerOrg(ctx context.Context, suppliedOrg, account uuid.UUID) (uuid.UUID, error) {
	acct, err := em.store.GetMarketplaceAccount(ctx, account)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			em.tel.tenantReject(ctx)
			return uuid.Nil, fmt.Errorf("%w: account %s", ErrCrossTenant, account)
		}
		return uuid.Nil, fmt.Errorf("analytics: resolve account owner: %w", err)
	}
	if suppliedOrg != acct.OrganizationID {
		em.tel.tenantReject(ctx)
		return uuid.Nil, fmt.Errorf("%w: account %s", ErrCrossTenant, account)
	}
	return acct.OrganizationID, nil
}

// validateEntityScope closes the ENTITY half of tenant integrity (issue #125 reopen
// residual): the envelope's entity_id must reference an entity OWNED by the account and
// COMPATIBLE with the family, resolved BEFORE any row or event telemetry. It fails
// closed and UNIFORMLY (ErrEntityScope carrying only the caller-supplied entity id): an
// unknown, foreign-account, or family-mismatched entity are indistinguishable, so the
// rejection is no ownership/existence oracle. A genuine infrastructure error from the
// resolver (not a not-found) is surfaced as-is, WITHOUT the entity-reject signal.
func (em *Emitter) validateEntityScope(ctx context.Context, env Envelope, fam Family) error {
	if fam.AccountLevel() {
		// The canonical entity of an account-level family IS the account; any other
		// entity_id is a cross-account/foreign reference. No resolver is consulted.
		if env.Entity == env.Account {
			return nil
		}
		em.tel.entityReject(ctx)
		return fmt.Errorf("%w: entity %s", ErrEntityScope, env.Entity)
	}
	// Entity-level family: the entity is a sub-account resource; resolve its
	// authoritative scope. With no resolver we CANNOT authorize the entity, so we fail
	// closed (never infer ownership) — the explicitly-planned stub for families whose
	// per-family resolver is not yet wired.
	if em.entities == nil {
		em.tel.entityReject(ctx)
		return fmt.Errorf("%w: entity %s", ErrEntityScope, env.Entity)
	}
	scope, err := em.entities.ResolveEntity(ctx, env.Entity)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			em.tel.entityReject(ctx)
			return fmt.Errorf("%w: entity %s", ErrEntityScope, env.Entity)
		}
		return fmt.Errorf("analytics: resolve entity scope: %w", err)
	}
	if scope.Account != env.Account || scope.Family != fam {
		em.tel.entityReject(ctx)
		return fmt.Errorf("%w: entity %s", ErrEntityScope, env.Entity)
	}
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
