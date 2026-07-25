// Server-minted bulk selection-set preview (PD-3 item 4): the SERVER, never the
// client, is the selection-set version authority. Pairs with bulk_confirm.go,
// which authoritatively confirms a preview bound to one exact version.
package recommendation

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// ErrUnknownMember is returned when a bulk-preview member names a
// recommendation that does not exist, or that belongs to a different
// account/variant than named — fails closed, never a fabricated member.
var ErrUnknownMember = errors.New("recommendation: unknown or mismatched selection-set member")

// PreviewMemberInput is one candidate member of a bulk selection-set preview.
type PreviewMemberInput struct {
	VariantID        uuid.UUID
	RecommendationID uuid.UUID
	// OfferIdentity is an OPTIONAL client SELECTOR (issue #87, BULK-PROTOCOL DESIGN
	// RECORD (e)) — never an assertion the server acts on. It is ADDITIVE: the empty
	// string means "not asserted", so the pre-#87 {VariantID, RecommendationID} shape
	// keeps working unchanged and this field is NEVER behaviorally required.
	//
	// When non-empty it is compared to the identity the SERVER seals from the named
	// recommendation's own evidence. A mismatch fails closed as ErrUnknownMember —
	// the SAME uniform not-found an unknown member produces, so it is no existence
	// oracle for the real identity. It can therefore NARROW (reject) but never WIDEN
	// or REDIRECT what gets authorized.
	OfferIdentity string
}

// PreviewMemberView is one resolved member of a selection-set preview, with its
// SERVER-derived disposition and SERVER-SEALED offer identity.
type PreviewMemberView struct {
	VariantID        uuid.UUID
	RecommendationID uuid.UUID
	Disposition      Disposition
	// OfferIdentity is the observed-offer identity SEALED by the server from this
	// member's own recommendation evidence (issue #87 criterion D, OBS-004). It is
	// resolved from the recommendation — which names exactly ONE evidence observation
	// — and NEVER by a lookup-by-target: a target may carry many offer identities, and
	// picking one by target is the #87 defect itself.
	//
	// "" is EXPLICIT ABSENCE (a recommendation that is not observation-driven), never
	// a stand-in for some other offer (quarantine over inference, §4.6).
	OfferIdentity string
}

// PreviewResult is the server-minted bulk selection-set preview (PD-3 item 4).
type PreviewResult struct {
	Set             db.SelectionSet
	Members         []PreviewMemberView
	AggregateImpact *money.Money // nil ⇒ unknown (never a fabricated zero, EVT-005 posture).
}

// PreviewBulkSelection is the screens-native bulk preview: it mints a
// SELECTION-SET VERSION ENTIRELY SERVER-SIDE (recommendation.CreateSelectionSet's
// append-only "next version per lineage" numbering — the hard safety precondition
// that the server, never the client, is the version authority). It resolves each
// member's disposition from the NAMED recommendation's own persisted, current
// state — never from a client assertion — and fails closed (ErrUnknownMember) on
// a recommendation that does not exist or does not belong to account/variant.
// Omitting lineage starts a NEW lineage; supplying an existing one mints the NEXT
// version within it (a refreshed preview).
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

	// Tenant isolation (issue #90, §4.6): claim-or-verify the lineage→account
	// ownership WHILE HOLDING the lineage lock and BEFORE any version is minted. A
	// refresh of a lineage owned by another account fails closed here
	// (ErrLineageNotOwned) — it can neither append a version into the victim's
	// lineage (which would invalidate the victim's live bound confirmation) nor plant
	// a row that a later confirmation could resolve. The database enforces the same
	// rule one layer down (migration 0045's composite FK), so this guard is the typed,
	// observable, non-oracle surface of an invariant that holds regardless.
	if err := s.claimLineageOwnership(ctx, q, "preview_bulk_selection", lineage, account); err != nil {
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
// account/variant, and computes the aggregate impact via aggregateContribution.
// The aggregate is known ONLY when EVERY member has available, compatible,
// non-overflowing contribution evidence; a missing contribution or a
// cross-currency/exponent mismatch or overflow flips the WHOLE aggregate to
// unknown (quarantine-over-inference, issue #141) rather than presenting an
// understated partial total. It reads on the caller's q so the resolution and the
// subsequent seal share one transaction — the returned aggregate is bound
// identically into the response and the sealed version.
func (s *Service) resolveBulkMembers(ctx context.Context, q *db.Queries, account uuid.UUID, members []PreviewMemberInput) ([]PreviewMemberView, *money.Money, error) {
	views := make([]PreviewMemberView, 0, len(members))
	contribs := make([]memberContribution, 0, len(members))
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
		// BULK-PROTOCOL DESIGN RECORD (e) — the offer identity is SEALED HERE, from
		// the server's OWN persisted state, on the SAME transaction that seals the
		// version. It is read from the recommendation's evidence observation, so it is
		// a pure function of the recommendation id and reproduces identically on a
		// historical replay (CST-002).
		sealedOffer, err := q.GetRecommendationSealedOfferIdentity(ctx, m.RecommendationID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, ErrUnknownMember
			}
			return nil, nil, err
		}
		// The client's value is a SELECTOR, validated against the sealed value — never
		// an input. A mismatch fails closed as the SAME uniform not-found an unknown
		// member produces (no existence oracle), and the whole preview rolls back, so
		// no partially-sealed version can be observed or bound.
		if m.OfferIdentity != "" && m.OfferIdentity != sealedOffer {
			return nil, nil, ErrUnknownMember
		}
		disp := dispositionOf(row)
		views = append(views, PreviewMemberView{
			VariantID:        m.VariantID,
			RecommendationID: m.RecommendationID,
			Disposition:      disp,
			OfferIdentity:    sealedOffer,
		})
		contribs = append(contribs, memberContribution{
			Available: row.ProposedContributionAvailable,
			Mantissa:  row.ProposedContributionMantissa.Int64,
			Currency:  row.ProposedContributionCurrency,
			Exponent:  int8(row.ProposedContributionExponent),
		})
	}
	impactPtr, err := aggregateContribution(contribs)
	if err != nil {
		return nil, nil, err
	}
	return views, impactPtr, nil
}

// memberContribution is one selection member's contribution evidence, decoupled
// from persistence so the aggregate-completeness rule is a pure, DB-free unit
// (issue #141). When Available is false the money triple is meaningless and is
// never read.
type memberContribution struct {
	Available bool
	Mantissa  int64
	Currency  string
	Exponent  int8
}

// aggregateContribution folds member contributions into ONE selection aggregate
// (issue #141). The aggregate is KNOWN (non-nil) only when EVERY member has
// available, mutually compatible, non-overflowing contribution evidence — a
// selection aggregate is a complete sum or it is nothing.
//
//   - Any member whose contribution evidence is UNAVAILABLE flips the WHOLE
//     aggregate to unknown (nil). A missing contribution is UNKNOWN, never zero;
//     presenting a partial sum as a complete known total is the #141 defect.
//   - A currency/exponent mismatch or int64 overflow while summing likewise flips
//     the aggregate to unknown — quarantine-over-inference, never an understated
//     partial (§9.1).
//   - A malformed currency code is a data fault that fails closed as a hard error
//     (never a silent unknown that could be mistaken for a routine unavailable
//     aggregate).
//
// The returned pointer is bound identically into both the operator-facing preview
// response and the sealed selection-set version (membership_fingerprint), so the
// two can never disagree.
func aggregateContribution(contribs []memberContribution) (*money.Money, error) {
	var impact money.Money
	haveImpact := false
	for _, c := range contribs {
		if !c.Available {
			return nil, nil
		}
		contrib, err := money.New(c.Mantissa, c.Currency, c.Exponent)
		if err != nil {
			return nil, err
		}
		if !haveImpact {
			impact = contrib
			haveImpact = true
			continue
		}
		summed, err := impact.Add(contrib)
		if err != nil {
			// Cross-currency/exponent mismatch or int64 overflow: the aggregate
			// can no longer be a trusted complete sum. Fail closed to unknown
			// rather than silently dropping a contribution from the running total.
			return nil, nil
		}
		impact = summed
	}
	if !haveImpact {
		return nil, nil
	}
	return &impact, nil
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
