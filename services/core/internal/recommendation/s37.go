// S37 consolidated PD-3 gateway endpoints (dk-p0-product-decisions.md):
// edit-price (CHAT-044, item 2), the actions queue read (item 5), and the
// server-minted bulk selection-set preview (item 4, the hard safety
// precondition — the server, never the client, mints the selection-set
// version).
package recommendation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// editPriceControlTTL is the fresh control-eligible window a price edit's new
// Draft carries. It follows the same explicit, locale-neutral (LOC-001) posture
// as draftControlTTL — this plane never derives a duration from a locale/region
// source.
const editPriceControlTTL = time.Hour

// EditPrice implements CHAT-044 / PD-3 item 2: mints a NEW card version, in the
// SAME lineage, with a NEW parameter version and the edited price, reset to
// Draft (approval.Card.EditPrice's domain intent, realized through the SAME
// mintDraftCard path every other Draft goes through — so no weaker Draft-
// creation path exists for a price edit). The prior control (if any) is thereby
// invalidated: its parameter version no longer matches the new binding.
func (s *Service) EditPrice(ctx context.Context, cardID uuid.UUID, newPrice money.Money, now time.Time) (db.ApprovalCard, error) {
	current, err := db.New(s.pool).GetApprovalCard(ctx, cardID)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	ev, err := DecodeEvidenceVersions(current.EvidenceVersions)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	binding := approval.Binding{
		ActionID:           current.ActionID,
		ParameterVersion:   current.ParameterVersion + 1, // a price edit always mints a NEW parameter version.
		ContextVersion:     current.ContextVersion,
		PolicyVersion:      current.PolicyVersion,
		CostProfileVersion: current.CostProfileVersion,
		EvidenceVersions:   ev,
		Expiry:             now.Add(editPriceControlTTL),
	}
	return s.mintDraftCard(ctx, current.RecommendationID, current.LineageID, current.MarketplaceAccountID, binding, newPrice)
}

// ListActions returns the account's actions queue: the current (greatest)
// version per lineage, newest first, bounded by limit (PD-3 item 5). A
// non-empty stateFilter narrows to that exact §8.4 state; empty returns every
// state. Filtering happens here (Go), not in SQL, to keep the query simple and
// always-safe.
func (s *Service) ListActions(ctx context.Context, account uuid.UUID, stateFilter string, limit int32) ([]db.ApprovalCard, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.New(s.pool).ListApprovalCardsByAccount(ctx, db.ListApprovalCardsByAccountParams{
		MarketplaceAccountID: account,
		Limit:                limit,
	})
	if err != nil {
		return nil, err
	}
	if stateFilter == "" {
		return rows, nil
	}
	out := make([]db.ApprovalCard, 0, len(rows))
	for _, r := range rows {
		if r.State == stateFilter {
			out = append(out, r)
		}
	}
	return out, nil
}

// ErrUnknownMember is returned when a bulk-preview member names a
// recommendation that does not exist, or that belongs to a different
// account/variant than named — fails closed, never a fabricated member.
var ErrUnknownMember = errors.New("recommendation: unknown or mismatched selection-set member")

// PreviewMemberInput is one candidate member of a bulk selection-set preview.
type PreviewMemberInput struct {
	VariantID        uuid.UUID
	RecommendationID uuid.UUID
}

// PreviewMemberView is one resolved member of a selection-set preview, with its
// SERVER-derived disposition.
type PreviewMemberView struct {
	VariantID        uuid.UUID
	RecommendationID uuid.UUID
	Disposition      Disposition
}

// PreviewResult is the server-minted bulk selection-set preview (PD-3 item 4).
type PreviewResult struct {
	Set             db.SelectionSet
	Members         []PreviewMemberView
	AggregateImpact *money.Money // nil ⇒ unknown (never a fabricated zero, EVT-005 posture).
}

// PreviewBulkSelection is the S37 screens-native bulk preview: it mints a
// SELECTION-SET VERSION ENTIRELY SERVER-SIDE (recommendation.CreateSelectionSet's
// append-only "next version per lineage" numbering — the hard S35/S37 safety
// precondition that the server, never the client, is the version authority). It
// resolves each member's disposition from the NAMED recommendation's own
// persisted, current state — never from a client assertion — and fails closed
// (ErrUnknownMember) on a recommendation that does not exist or does not belong
// to account/variant. Omitting lineage starts a NEW lineage; supplying an
// existing one mints the NEXT version within it (a refreshed preview).
func (s *Service) PreviewBulkSelection(ctx context.Context, account, lineage uuid.UUID, name string, criteria map[string]string, members []PreviewMemberInput) (PreviewResult, error) {
	if lineage == uuid.Nil {
		lineage = uuid.New()
	}

	// The WHOLE preview — member resolution, fingerprint, version mint, and the
	// exactly-member_count member inserts — happens in ONE transaction and fails
	// closed (rollback) on any error. No half-populated version can ever be observed
	// or bound (#91).
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PreviewResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	views, impactPtr, err := s.resolveBulkMembers(ctx, q, account, members)
	if err != nil {
		return PreviewResult{}, err
	}

	// Serialize per-lineage version minting BEFORE computing MAX(version)+1 and
	// writing members, so two concurrent creations on one lineage produce ORDERED,
	// distinct versions with no lost members (the lock is held to commit).
	if err := q.LockApprovalLineage(ctx, lineage); err != nil {
		return PreviewResult{}, err
	}

	// The membership_fingerprint is computed inside sealSelectionVersion from the
	// resolved views + aggregate BEFORE any member write, then the version and its
	// exact membership are inserted and sealed.
	set, err := sealSelectionVersion(ctx, q, account, lineage, name, criteria, views, impactPtr)
	if err != nil {
		return PreviewResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return PreviewResult{}, err
	}

	return PreviewResult{Set: set, Members: views, AggregateImpact: impactPtr}, nil
}

// resolveBulkMembers resolves each requested member's SERVER-side disposition from
// its own persisted recommendation (never a client assertion), failing closed
// (ErrUnknownMember) on a recommendation that does not exist or does not belong to
// account/variant, and computes the aggregate impact. A cross-currency/exponent
// mismatch flips the WHOLE aggregate to unknown (quarantine-over-inference) rather
// than presenting an understated partial total. It reads on the caller's q so the
// resolution and the subsequent seal share one transaction.
func (s *Service) resolveBulkMembers(ctx context.Context, q *db.Queries, account uuid.UUID, members []PreviewMemberInput) ([]PreviewMemberView, *money.Money, error) {
	views := make([]PreviewMemberView, 0, len(members))
	var impact money.Money
	haveImpact := false
	impactUnavailable := false
	for _, m := range members {
		row, err := q.GetRecommendation(ctx, m.RecommendationID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, ErrUnknownMember
			}
			return nil, nil, err
		}
		if row.MarketplaceAccountID != account || row.VariantID != m.VariantID {
			return nil, nil, ErrUnknownMember
		}
		disp := dispositionOf(row)
		views = append(views, PreviewMemberView{VariantID: m.VariantID, RecommendationID: m.RecommendationID, Disposition: disp})

		if row.ProposedContributionAvailable && !impactUnavailable {
			contrib, err := money.New(row.ProposedContributionMantissa.Int64, row.ProposedContributionCurrency, int8(row.ProposedContributionExponent))
			if err != nil {
				return nil, nil, err
			}
			if !haveImpact {
				impact = contrib
				haveImpact = true
				continue
			}
			summed, err := impact.Add(contrib)
			if err != nil {
				// A cross-currency/exponent mismatch means the aggregate can no
				// longer be trusted as a complete sum. Quarantine-over-inference:
				// NEVER present a partial/understated total as if it were the
				// whole picture — flip the WHOLE aggregate to unknown rather than
				// silently dropping this member's contribution from the running
				// total (§9.1, "no swallowed error returning a default downstream
				// treats as success").
				haveImpact = false
				impactUnavailable = true
				continue
			}
			impact = summed
		}
	}
	if haveImpact {
		return views, &impact, nil
	}
	return views, nil, nil
}

// dispositionOf derives a member's SERVER-side bulk disposition from its
// persisted recommendation: approvable ⇒ executable; a non-approvable
// recommendation with recorded blockers ⇒ blocked; otherwise (e.g. still
// analysis-only, no hard blocker recorded) ⇒ warning. Never taken from the
// client.
func dispositionOf(row db.Recommendation) Disposition {
	if row.Approvable {
		return DispositionExecutable
	}
	if len(row.Blockers) > 2 { // "[]" (empty JSON array) has length 2.
		return DispositionBlocked
	}
	return DispositionWarning
}
