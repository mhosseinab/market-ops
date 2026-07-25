// The APPEND-ONLY bulk provenance ledger (issue #87, prior findings 1 and 3).
//
// PRD refs: §4.6 (idempotency, approval versioning, audit), AUD-001 (a state-changing
// operation is reproducible from its own durable evidence, without the transcript),
// CHAT-052 (a bulk confirmation binds ONE exact selection-set version).
//
// WHY THIS EXISTS. Before it, `already_authorized` meant only "this card's structured
// control was activated by SOMEONE". A card approved through the INDIVIDUAL §8.4
// confirm — or through a DIFFERENT selection set — was therefore reported
// `already_authorized` for a selection that had authorized nothing (prior finding 1),
// which told the operator that this selection had done something it never did, and
// left the authorization unattributable in the audit trail.
//
// WHAT IT IS. One immutable row per (selection-set version, member) that this plane
// actually authorized, carrying the full provenance: the set, its (lineage, version)
// PAIR, the account, the variant, the recommendation, the SEALED offer identity, and
// the §8.4 card + APR-001 action it activated. `already_authorized` now requires a
// row that matches EXACTLY; anything else fails closed.
//
// WHERE IT IS ENFORCED. In the DATABASE (migration 0049): composite foreign keys bind
// the (member, set) pair and the (set, lineage, version, account) tuple, and a trigger
// verifies the variant / recommendation / offer-identity columns against the member
// row — because selection_set_members.recommendation_id is NULLABLE and a MATCH SIMPLE
// composite FK over it is silently satisfied by any NULL component, which is the exact
// hole prior finding 3 exploited with raw SQL. The Go check below is defence in depth,
// never the primary guard.
package recommendation

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// bulkProvenance is the exact provenance a bulk authorization records, assembled from
// the SEALED member row and its SEALED selection-set version — never from request
// input. It is passed down into the individual confirm so the ledger row commits
// ATOMICALLY with the Approved state: an authorization can never exist without its
// provenance, and provenance can never exist without its authorization.
type bulkProvenance struct {
	MemberID         uuid.UUID
	SetID            uuid.UUID
	LineageID        uuid.UUID
	Version          int32
	Account          uuid.UUID
	VariantID        uuid.UUID
	RecommendationID uuid.UUID
	OfferIdentity    string
}

// provenanceOf builds the provenance for one member of a bound selection-set version.
// Both inputs are server-sealed rows, so every field is authoritative by construction.
func provenanceOf(set db.SelectionSet, m db.SelectionSetMember) bulkProvenance {
	return bulkProvenance{
		MemberID:         m.ID,
		SetID:            m.SelectionSetID,
		LineageID:        set.LineageID,
		Version:          set.Version,
		Account:          m.MarketplaceAccountID,
		VariantID:        m.VariantID,
		RecommendationID: uuidFromPg(m.RecommendationID),
		OfferIdentity:    m.OfferIdentity,
	}
}

// recordBulkBinding appends the provenance row for a member this call just authorized,
// on the CALLER'S transaction (the same one that advanced the card to Approved and
// appended the AUD-001 confirmation event). A failure rolls the whole confirmation
// back — fail closed: an authorization with no durable provenance is exactly the state
// prior finding 1 describes, so it must never be reachable.
//
// The insert is ON CONFLICT DO NOTHING on (set, member): a replayed confirmation
// collapses to exactly ONE binding per member (design record (c)). It is NEVER
// DO UPDATE — the ledger is append-only (§4.6) and re-pointing an existing binding is
// the forgery the ledger exists to prevent.
func recordBulkBinding(ctx context.Context, q *db.Queries, p bulkProvenance, card db.ApprovalCard) error {
	_, err := q.InsertBulkActionBinding(ctx, db.InsertBulkActionBindingParams{
		SelectionSetMemberID:  p.MemberID,
		SelectionSetID:        p.SetID,
		SelectionSetLineageID: p.LineageID,
		SelectionSetVersion:   p.Version,
		MarketplaceAccountID:  p.Account,
		VariantID:             p.VariantID,
		RecommendationID:      p.RecommendationID,
		OfferIdentity:         p.OfferIdentity,
		CardID:                card.ID,
		ActionID:              card.ActionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING returned no row: the binding already exists for this
		// (set, member). That is the idempotent replay, not a failure — the durable
		// fact is already recorded and must not be rewritten.
		return nil
	}
	return err
}

// bulkBindingMatch is the outcome of checking a member's durable provenance before
// reporting a sealed authorization.
type bulkBindingMatch int

const (
	// bindingMatches — a durable binding exists for THIS (set, member) and every
	// provenance field agrees. `already_authorized` is truthful.
	bindingMatches bulkBindingMatch = iota
	// bindingAbsent — the card's control was activated, but NOT by this selection
	// (an individual confirm, or a different selection set). Fails closed.
	bindingAbsent
	// bindingUndetermined — the provenance read itself failed. The outcome is not
	// known, so it is never guessed; the caller reports an undetermined result and a
	// resume re-derives it.
	bindingUndetermined
)

// matchBulkBinding reports whether THIS selection-set version durably authorized this
// member (prior finding 1). It is the gate in front of `already_authorized`.
//
// The lookup is by the (set, member) PAIR — BULK-PROTOCOL DESIGN RECORD (d): it never
// orders, ranges, or diffs version counters, and never accepts a bare version, because
// versions are monotonic only WITHIN one lineage and are NOT comparable across
// lineages.
//
// The field-by-field comparison that follows is DEFENCE IN DEPTH, not the primary
// guard: migration 0049's composite FKs and provenance trigger already make a
// divergent row unconstructable at the database. It is kept because a guard that only
// holds "because another layer holds" is a guard that silently disappears the day that
// layer is relaxed.
func matchBulkBinding(ctx context.Context, q *db.Queries, p bulkProvenance) bulkBindingMatch {
	row, err := q.GetBulkActionBindingForMember(ctx, db.GetBulkActionBindingForMemberParams{
		SelectionSetID:       p.SetID,
		SelectionSetMemberID: p.MemberID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return bindingAbsent
		}
		return bindingUndetermined
	}
	if row.SelectionSetLineageID != p.LineageID ||
		row.SelectionSetVersion != p.Version ||
		row.MarketplaceAccountID != p.Account ||
		row.VariantID != p.VariantID ||
		row.RecommendationID != p.RecommendationID ||
		row.OfferIdentity != p.OfferIdentity {
		// Unreachable while the DB constraints stand. Treated as ABSENT rather than as
		// a match: a provenance row that does not describe this member is not evidence
		// that this selection authorized it.
		return bindingAbsent
	}
	return bindingMatches
}
