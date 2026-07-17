package routec

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
)

// TargetRetirer subscribes to identity-reopen events and RETIRES (deactivates)
// the observation targets of a reopened identity. This closes the S13
// carry-forward: migration 0007 created targets only for active Confirmed
// identities and guarded creation with a trigger, but left target DEACTIVATION on
// reopen unwired. An identity that leaves the Confirmed set (NeedsReview/Rejected/
// Obsolete) must stop producing executable observations — its target is
// deactivated here, so the tier scheduler (which lists only active targets) never
// fetches it again and no new executable observation is created for it.
//
// It implements identity.EventSink. The identity service calls it AFTER the
// reopen transaction commits, so a target is only retired for a reopen that
// actually happened; the durable recommendation_invalidation_events row remains
// the system of record. OBS-007: deactivation disables only the dependent
// capability — it never relabels the target's already-stored observations as
// current; those age out through the store's normal expiry.
type TargetRetirer struct {
	pool *pgxpool.Pool
}

// NewTargetRetirer builds the retirer bound to the pool.
func NewTargetRetirer(pool *pgxpool.Pool) *TargetRetirer {
	return &TargetRetirer{pool: pool}
}

// Ensure TargetRetirer satisfies the identity subscription seam.
var _ identity.EventSink = (*TargetRetirer)(nil)

// MappingReopened deactivates every active target owned by the reopened
// identity. Idempotent: a re-delivered event finds the target already inactive
// and changes nothing (the query filters active=true). Returns an error only on
// a real DB failure, so a transient failure can be retried by the caller.
func (r *TargetRetirer) MappingReopened(ctx context.Context, ev identity.MappingReopenedEvent) error {
	retired, err := db.New(r.pool).DeactivateObservationTargetsForIdentity(ctx, ev.IdentityID)
	if err != nil {
		return fmt.Errorf("routec: retire targets for reopened identity %s: %w", ev.IdentityID, err)
	}
	_ = retired // count available for observability; the deactivation is the effect
	return nil
}
