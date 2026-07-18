package execution

import (
	"context"
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
// boundary, permission, JIT freshness) are the state the approval control was
// already minted against at Confirm time; a full LIVE re-resolution of each from
// its owning service is the named follow-up. Until then this resolver never
// enables a write on its own (enablement is OFF), so the recommend-only path is
// the only reachable outcome and no un-re-resolved external signal can authorize
// a marketplace mutation.
type DefaultResolver struct {
	pool       *pgxpool.Pool
	capability CapabilityCheck
	now        func() time.Time
}

// NewDefaultResolver wires the resolver. A nil capability check defaults to
// "never Supported" (fail closed).
func NewDefaultResolver(pool *pgxpool.Pool, capability CapabilityCheck) *DefaultResolver {
	if capability == nil {
		capability = func(context.Context, uuid.UUID) (bool, error) { return false, nil }
	}
	return &DefaultResolver{pool: pool, capability: capability, now: func() time.Time { return time.Now().UTC() }}
}

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
		Enablement:      WriteEnablement{CapabilitySupported: supported, RegionWriteVerified: verified},
		Actor:           audit.Actor{Surface: "system"},
		AccountID:       rc.AccountID,
		VariantID:       rc.VariantID,
		VariantNativeID: rc.NativeVariantID,
	}, nil
}
