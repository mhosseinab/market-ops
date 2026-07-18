package execution

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// CapabilityCheck reports whether the account's price_write capability is
// Supported (§15.2). It is injected so the executor never hardcodes a capability;
// the default wiring returns false (Unknown never enables), keeping writes OFF.
type CapabilityCheck func(ctx context.Context, accountID uuid.UUID) (bool, error)

// DefaultResolver is the production RevalidationContext resolver. It re-resolves
// the CURRENT binding SERVER-SIDE from the authoritative store — NEVER from a
// client-echoed request body (carry-forward from S17):
//
//   - It re-reads the card row (the authoritative BOUND binding).
//   - It re-resolves the CURRENT cost/policy/context/parameter versions from the
//     greatest-version recommendation in the card's lineage, so an out-of-band
//     version change that never passed through a state-machine invalidation is
//     caught at the gate (EXE-001).
//   - Write enablement reads the S35 region write-verification flag and the
//     injected capability check; both default OFF, so a real write is impossible
//     until S35 records verified parameters.
//
// The remaining external gate signals (identity, current price, money unit,
// boundary, permission, JIT freshness) are, in this resolver, the state the
// approval control was already minted against at Confirm time — they are NOT yet
// re-resolved live. The step that must replace them with LIVE per-signal
// resolution is S35 (region money-verification + write-verification), which turns
// write enablement on; the exact signals it must make live are:
//
//	IdentityConfirmed    ← identity mapping is Confirmed (§7.1)
//	CurrentPriceMatches  ← live owned-offer price equals the card baseline
//	MoneyUnitAmbiguous   ← source money unit is verified/unambiguous (§9.1)
//	BoundaryKnown        ← current marketplace price boundary is known
//	PermissionGranted    ← actor still holds L4 execute permission
//	JITFresh             ← a JIT refresh satisfies OBS-009 (≤10min, in budget)
//
// Coupling guard (latent EXE-001 bypass): this resolver REFUSES to hand back a
// write-ENABLED context while these signals are still static placeholders (see
// ErrSignalsStatic). So if S35 flips enablement on WITHOUT calling WithLiveSignals,
// the resolver fails closed rather than passing gates on hardcoded truth.
type DefaultResolver struct {
	pool        *pgxpool.Pool
	capability  CapabilityCheck
	now         func() time.Time
	signalsLive bool
}

// ErrSignalsStatic is returned when write enablement is on but the external gate
// signals are still static placeholders — a fail-closed guard against an EXE-001
// bypass (the write path must not run on hardcoded gate truth).
var ErrSignalsStatic = errors.New("execution: write enabled but external gate signals are not live-resolved; refusing to authorize (call WithLiveSignals — S35)")

// NewDefaultResolver wires the resolver. A nil capability check defaults to
// "never Supported" (fail closed).
func NewDefaultResolver(pool *pgxpool.Pool, capability CapabilityCheck) *DefaultResolver {
	if capability == nil {
		capability = func(context.Context, uuid.UUID) (bool, error) { return false, nil }
	}
	return &DefaultResolver{pool: pool, capability: capability, now: func() time.Time { return time.Now().UTC() }}
}

// WithLiveSignals asserts that the external gate signals produced by this resolver
// are live-resolved from their owning services (S35). Only then may it return a
// write-enabled context. Until it is called, a write-enabled context fails closed.
func (r *DefaultResolver) WithLiveSignals() *DefaultResolver { r.signalsLive = true; return r }

// Resolve implements Resolver.
func (r *DefaultResolver) Resolve(ctx context.Context, card db.ApprovalCard) (RevalidationContext, error) {
	q := db.New(r.pool)

	// Re-read the card server-side: this row is the authoritative BOUND binding.
	fresh, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		return RevalidationContext{}, err
	}
	bound := bindingOf(fresh)

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

	verified, err := q.IsWriteVerified(ctx, fresh.MarketplaceAccountID)
	if err != nil {
		return RevalidationContext{}, err
	}
	supported, err := r.capability(ctx, fresh.MarketplaceAccountID)
	if err != nil {
		return RevalidationContext{}, err
	}

	enablement := WriteEnablement{CapabilitySupported: supported, RegionWriteVerified: verified}
	// Fail closed: never authorize a write while the external gate signals are
	// static placeholders (R5 — latent EXE-001 bypass guard).
	if enablement.CanWrite() && !r.signalsLive {
		return RevalidationContext{}, ErrSignalsStatic
	}

	return RevalidationContext{
		Inputs: RevalidationInputs{
			Bound:   bound,
			Current: current,
			Now:     r.now(),
			// Signals validated at Confirm time; live per-signal re-resolution is a
			// named follow-up. Enablement stays OFF below, so no write is reachable.
			IdentityConfirmed:   true,
			CurrentPriceMatches: true,
			MoneyUnitAmbiguous:  false,
			BoundaryKnown:       true,
			PermissionGranted:   true,
			JITFresh:            true,
		},
		Enablement:      enablement,
		Actor:           audit.Actor{Surface: "system"},
		AccountID:       rc.AccountID,
		VariantID:       rc.VariantID,
		VariantNativeID: rc.NativeVariantID,
	}, nil
}
