package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// CapabilityCheck reports whether the account's price_write capability is
// Supported (§15.2). It is injected so the executor never hardcodes a capability;
// the default wiring returns false (Unknown never enables), keeping writes OFF.
type CapabilityCheck func(ctx context.Context, accountID uuid.UUID) (bool, error)

// PriceBaseline is the card's bound action price the live owned-offer price must
// still equal for the EXE-001 current_price gate to pass. It carries the typed
// money COMPONENTS only (mantissa/currency/exponent) — no float ever touches this
// path, and the resolver performs no arithmetic on them; the PriceSignal owns the
// comparison against the authoritative owned offer.
type PriceBaseline struct {
	Mantissa int64
	Currency string
	Exponent int8
}

// The signal-source seams below are the authoritative producers for the six
// external EXE-001 gate signals the version-comparison alone cannot cover. Each is
// injected (dependency inversion) so the executor never fabricates a signal, and
// each is resolved LIVE at the execution boundary. The contract for every source:
//
//   - A genuine failure returns a non-nil error, which the resolver PROPAGATES
//     (never swallowed into a passing gate).
//   - An unavailable/unknown state returns the BLOCKING value (identity not
//     confirmed, price does not match, unit ambiguous, boundary unknown, permission
//     absent, evidence stale) — NEVER a positive default.

// IdentitySignal resolves whether a variant's market-product identity is still
// Confirmed and active (§7.1 / §16 reopen).
type IdentitySignal interface {
	IdentityConfirmed(ctx context.Context, variantID uuid.UUID) (bool, error)
}

// PriceSignal resolves whether the live owned-offer price still equals the card's
// bound baseline (EXE-001 current_price).
type PriceSignal interface {
	CurrentPriceMatches(ctx context.Context, variantID uuid.UUID, baseline PriceBaseline) (bool, error)
}

// MoneyUnitSignal resolves whether the source money unit is verified/unambiguous
// (§9.1 quarantine). It returns true only when the unit is unambiguous; the
// resolver inverts it into the MoneyUnitAmbiguous gate input.
type MoneyUnitSignal interface {
	MoneyUnitUnambiguous(ctx context.Context, variantID uuid.UUID) (bool, error)
}

// BoundarySignal resolves whether the current marketplace price boundary is known.
type BoundarySignal interface {
	BoundaryKnown(ctx context.Context, variantID uuid.UUID) (bool, error)
}

// PermissionSignal resolves whether the account still holds an active L4 execute
// permission for the action.
type PermissionSignal interface {
	PermissionGranted(ctx context.Context, accountID uuid.UUID) (bool, error)
}

// EvidenceSignal resolves the CURRENT cited-evidence versions (so an add/remove/
// version-change is discoverable against the card's bound versions) AND whether a
// JIT refresh satisfies OBS-009 freshness (≤10min, in budget, within tolerance).
type EvidenceSignal interface {
	CurrentEvidenceVersions(ctx context.Context, variantID uuid.UUID) (map[uuid.UUID]int64, error)
	JITFresh(ctx context.Context, variantID uuid.UUID) (bool, error)
}

// SignalSources bundles the authoritative producers for the six external EXE-001
// gate signals. A live resolver may not be constructed with any field nil
// (validate) — "live" mode cannot exist without every source, so a missing source
// fails closed at construction rather than silently defaulting a gate to pass.
type SignalSources struct {
	Identity   IdentitySignal
	Price      PriceSignal
	MoneyUnit  MoneyUnitSignal
	Boundary   BoundarySignal
	Permission PermissionSignal
	Evidence   EvidenceSignal
}

// validate reports every missing source. A live resolver refuses to construct
// while any is absent (EXE-001: no gate may be sourced from a default positive).
func (s SignalSources) validate() error {
	var missing []string
	if s.Identity == nil {
		missing = append(missing, "identity")
	}
	if s.Price == nil {
		missing = append(missing, "price")
	}
	if s.MoneyUnit == nil {
		missing = append(missing, "money_unit")
	}
	if s.Boundary == nil {
		missing = append(missing, "boundary")
	}
	if s.Permission == nil {
		missing = append(missing, "permission")
	}
	if s.Evidence == nil {
		missing = append(missing, "evidence")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %v", ErrIncompleteSignalSources, missing)
	}
	return nil
}

// DefaultResolver is the production RevalidationContext resolver. It re-resolves
// the CURRENT binding SERVER-SIDE from the authoritative store — NEVER from a
// client-echoed request body (carry-forward from S17):
//
//   - It re-reads the card row (the authoritative BOUND binding), including the
//     card's bound cited-evidence versions.
//   - It re-resolves the CURRENT cost/policy/context/parameter versions from the
//     greatest-version recommendation in the card's lineage, so an out-of-band
//     version change that never passed through a state-machine invalidation is
//     caught at the gate (EXE-001).
//   - It re-resolves the six external gate signals (identity, current price, money
//     unit, boundary, permission, evidence/JIT) LIVE from injected authoritative
//     sources; an unavailable signal yields the BLOCKING value and a source error
//     is propagated — neither is ever a positive default.
//   - Write enablement reads the S35 region write-verification flag and the
//     injected capability check; both default OFF, so a real write is impossible
//     until S35 records verified parameters.
//
// Fail-closed contract (EXE-001, never-cut): a resolver with NO signal sources
// cannot revalidate identity/price/money-unit/boundary/permission/evidence live,
// so it REFUSES to hand back a context for EITHER the write or the recommend-only
// path (ErrSignalsStatic). This is the currently-reachable dark posture: it never
// fabricates a positive gate signal, and it never invalidates a card on fabricated
// truth — it fails closed with a typed error.
type DefaultResolver struct {
	pool       *pgxpool.Pool
	capability CapabilityCheck
	now        func() time.Time
	sources    *SignalSources
}

// ErrSignalsStatic is returned when a resolver has no live signal sources: it
// cannot resolve the external EXE-001 gates from authoritative state, so it refuses
// to authorize execution OR recommend-only matching on static/fabricated signals.
// The remedy is to construct the resolver with NewLiveResolver and the authoritative
// signal sources (wired by S35 region money-verification + write-verification).
var ErrSignalsStatic = errors.New("execution: external gate signals are not live-resolved; refusing to authorize execution or recommend-only matching on static signals (construct with NewLiveResolver + authoritative sources — S35)")

// ErrIncompleteSignalSources is returned by NewLiveResolver when any authoritative
// signal source is missing — "live" mode cannot be built without every source.
var ErrIncompleteSignalSources = errors.New("execution: live resolver requires every authoritative signal source; refusing to construct")

// NewDefaultResolver wires the DARK resolver: it has NO live signal sources, so it
// fails closed (ErrSignalsStatic) rather than fabricating the external gate
// signals. A nil capability check defaults to "never Supported" (fail closed).
// Production wires this until S35 supplies the authoritative signal sources.
func NewDefaultResolver(pool *pgxpool.Pool, capability CapabilityCheck) *DefaultResolver {
	if capability == nil {
		capability = func(context.Context, uuid.UUID) (bool, error) { return false, nil }
	}
	return &DefaultResolver{pool: pool, capability: capability, now: func() time.Time { return time.Now().UTC() }}
}

// NewLiveResolver wires a resolver that resolves every external EXE-001 gate signal
// LIVE from the supplied authoritative sources. It REFUSES to construct unless every
// source is present (ErrIncompleteSignalSources): "live" mode cannot exist without
// all of them, so a missing source fails closed here rather than defaulting a gate
// to pass. A nil capability check defaults to "never Supported" (fail closed).
func NewLiveResolver(pool *pgxpool.Pool, capability CapabilityCheck, sources SignalSources) (*DefaultResolver, error) {
	if err := sources.validate(); err != nil {
		return nil, err
	}
	r := NewDefaultResolver(pool, capability)
	r.sources = &sources
	return r, nil
}

// WithClock overrides the clock (tests).
func (r *DefaultResolver) WithClock(now func() time.Time) *DefaultResolver { r.now = now; return r }

// Resolve implements Resolver.
func (r *DefaultResolver) Resolve(ctx context.Context, card db.ApprovalCard) (RevalidationContext, error) {
	q := db.New(r.pool)

	// Re-read the card server-side: this row is the authoritative BOUND binding.
	fresh, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		return RevalidationContext{}, err
	}
	bound := bindingOf(fresh)
	boundEvidence, err := parseEvidenceVersions(fresh.EvidenceVersions)
	if err != nil {
		return RevalidationContext{}, err
	}
	bound.EvidenceVersions = boundEvidence

	// Re-resolve the CURRENT versions from the authoritative recommendation lineage.
	rc, err := q.GetCurrentExecutionContext(ctx, fresh.RecommendationID)
	if err != nil {
		return RevalidationContext{}, err
	}
	current := bound
	current.CostProfileVersion = rc.CurrentCostProfileVersion
	current.PolicyVersion = rc.CurrentPolicyVersion
	current.ContextVersion = rc.CurrentContextVersion
	current.ParameterVersion = rc.CurrentParameterVersion
	// Current evidence MUST be re-resolved LIVE below — never copied from bound, or
	// an evidence add/remove/version-change could not be discovered (EXE-001).
	current.EvidenceVersions = nil

	verified, err := q.IsWriteVerified(ctx, fresh.MarketplaceAccountID)
	if err != nil {
		return RevalidationContext{}, err
	}
	supported, err := r.capability(ctx, fresh.MarketplaceAccountID)
	if err != nil {
		return RevalidationContext{}, err
	}
	enablement := WriteEnablement{CapabilitySupported: supported, RegionWriteVerified: verified}

	// Fail closed: with no live signal sources this resolver cannot revalidate the
	// external gates, so it refuses to hand back a context for EITHER path (write or
	// recommend-only). It never fabricates a positive signal and never invalidates a
	// card on static truth (EXE-001 never-cut).
	if r.sources == nil {
		return RevalidationContext{}, ErrSignalsStatic
	}

	inputs := RevalidationInputs{Bound: bound, Current: current, Now: r.now()}
	if err := r.resolveSignals(ctx, &inputs, rc.VariantID, rc.AccountID, PriceBaseline{
		Mantissa: fresh.PriceMantissa,
		Currency: fresh.PriceCurrency,
		Exponent: int8(fresh.PriceExponent),
	}); err != nil {
		return RevalidationContext{}, err
	}

	return RevalidationContext{
		Inputs:          inputs,
		Enablement:      enablement,
		Actor:           audit.Actor{Surface: "system"},
		AccountID:       rc.AccountID,
		VariantID:       rc.VariantID,
		VariantNativeID: rc.NativeVariantID,
	}, nil
}

// resolveSignals fills the six external EXE-001 gate signals from the injected
// authoritative sources. A source error is wrapped and returned (never swallowed
// into a passing gate); an unavailable signal is expected to arrive as the BLOCKING
// value. It also re-resolves the CURRENT cited-evidence versions live so an
// evidence add/remove/version-change is discoverable by the gate matrix.
func (r *DefaultResolver) resolveSignals(ctx context.Context, in *RevalidationInputs, variantID, accountID uuid.UUID, baseline PriceBaseline) error {
	s := r.sources

	identityConfirmed, err := s.Identity.IdentityConfirmed(ctx, variantID)
	if err != nil {
		return fmt.Errorf("execution: resolve identity signal: %w", err)
	}
	in.IdentityConfirmed = identityConfirmed

	priceMatches, err := s.Price.CurrentPriceMatches(ctx, variantID, baseline)
	if err != nil {
		return fmt.Errorf("execution: resolve current-price signal: %w", err)
	}
	in.CurrentPriceMatches = priceMatches

	unambiguous, err := s.MoneyUnit.MoneyUnitUnambiguous(ctx, variantID)
	if err != nil {
		return fmt.Errorf("execution: resolve money-unit signal: %w", err)
	}
	in.MoneyUnitAmbiguous = !unambiguous

	boundaryKnown, err := s.Boundary.BoundaryKnown(ctx, variantID)
	if err != nil {
		return fmt.Errorf("execution: resolve boundary signal: %w", err)
	}
	in.BoundaryKnown = boundaryKnown

	permitted, err := s.Permission.PermissionGranted(ctx, accountID)
	if err != nil {
		return fmt.Errorf("execution: resolve permission signal: %w", err)
	}
	in.PermissionGranted = permitted

	currentEvidence, err := s.Evidence.CurrentEvidenceVersions(ctx, variantID)
	if err != nil {
		return fmt.Errorf("execution: resolve current-evidence signal: %w", err)
	}
	in.Current.EvidenceVersions = currentEvidence

	jitFresh, err := s.Evidence.JITFresh(ctx, variantID)
	if err != nil {
		return fmt.Errorf("execution: resolve jit-freshness signal: %w", err)
	}
	in.JITFresh = jitFresh
	return nil
}

// parseEvidenceVersions decodes the card's bound evidence-version map (jsonb
// {observation_uuid: version}). An empty payload is an empty binding (no cited
// evidence), which is distinct from an unavailable live re-resolution.
func parseEvidenceVersions(raw []byte) (map[uuid.UUID]int64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[uuid.UUID]int64
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("execution: parse bound evidence versions: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}
	return m, nil
}

// DBIdentitySignal resolves the identity gate LIVE from the authoritative
// market_product_identities store: a variant counts as Confirmed only while its
// mapping is state='confirmed' AND active (GetActiveConfirmedIdentityForVariant).
// A reopened / needs-review / rejected / obsolete mapping yields no row → false →
// the identity gate blocks (§16 reopen). This is a real authoritative producer,
// not a stub.
type DBIdentitySignal struct{ pool *pgxpool.Pool }

// NewDBIdentitySignal wires the live identity signal over the given pool.
func NewDBIdentitySignal(pool *pgxpool.Pool) *DBIdentitySignal { return &DBIdentitySignal{pool: pool} }

// IdentityConfirmed reports whether the variant has an active Confirmed mapping.
// pgx.ErrNoRows (no active Confirmed mapping) is the fail-closed FALSE, not an
// error; any other error is propagated.
func (d *DBIdentitySignal) IdentityConfirmed(ctx context.Context, variantID uuid.UUID) (bool, error) {
	_, err := db.New(d.pool).GetActiveConfirmedIdentityForVariant(ctx, variantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
