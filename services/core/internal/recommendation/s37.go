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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/keyset"
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

	// Never-cut policy-order re-check (§4.6, CHAT-044, issue #134): the operator-
	// edited price MUST re-pass the FULL boundary → floor → movement-cap → cooldown
	// → strategy → objective chain against the EDITED value before a new control-
	// bearing version can exist. Fail CLOSED when no rechecker is wired (dark P0) or
	// the chain does not admit exactly the edited price — an invalid edit mints no
	// new card version and no new parameter version, so it can never reach
	// AwaitingConfirmation. This runs on the SAME versioned recommendation the card
	// was minted from (CST-002 reproducibility), never the current one.
	if s.editPriceRecheck == nil {
		return db.ApprovalCard{}, ErrEditedPriceRejected
	}
	rec, err := db.New(s.pool).GetRecommendation(ctx, current.RecommendationID)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	pc, err := s.editPriceRecheck.PolicyContextFor(ctx, rec)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if _, admitted, err := AdmitEditedPrice(pc, newPrice, now); err != nil {
		return db.ApprovalCard{}, err
	} else if !admitted {
		return db.ApprovalCard{}, ErrEditedPriceRejected
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

// Default and hard-maximum page sizes for the actions queue read (PD-3 item 5,
// §17 bounded reads). The default applies when the caller omits a limit; the
// maximum is a hard cap, so a large or absent client limit can never unbound the
// scan.
//
// The cap FAILS CLOSED (issue #90 blocker 3): a caller asking for MORE than
// MaxActionsLimit gets ErrLimitAboveMax, never a silently clamped page. A silent
// clamp is what made the old 500-row truncation invisible — the caller believed it
// held the whole queue while the server had quietly dropped the tail. (The
// notification feed deliberately clamps instead: its response is a scrollable feed
// whose completeness the caller reads from hasMore/nextCursor. The actions queue
// now carries the same completeness signal, and additionally refuses an
// out-of-contract limit rather than answering a question it was not asked.)
const (
	defaultActionsLimit int32 = 200
	// MaxActionsLimit is the hard, contract-declared maximum page size for
	// GET /actions (mirrored as `maximum: 500` on the wire).
	MaxActionsLimit int32 = 500
)

// ErrLimitAboveMax is returned when a caller requests an actions page larger than
// MaxActionsLimit. It fails closed with NO rows: the transport maps it to a 400, so
// an over-large request is a visible validation error, never a truncated success.
var ErrLimitAboveMax = errors.New("recommendation: requested page limit exceeds the maximum")

// ErrInvalidCursor is returned for a malformed, tampered, unknown-version, or
// FOREIGN-account continuation cursor. It IS keyset.ErrInvalidCursor, the shared
// sentinel every paginated read in this repo fails safe with; the transport maps it
// to a 400. A bad cursor is never silently reinterpreted as a first page (which
// would quietly re-serve rows the caller already had) and never reads another
// tenant's queue — the account predicate remains the authorization.
var ErrInvalidCursor = keyset.ErrInvalidCursor

// ActionsPageRequest is a caller-facing, UNRESOLVED bounded-read request: the
// optional raw limit and the optional opaque cursor exactly as they arrived on the
// wire. Resolution (bound check + decode + account-binding validation) happens in
// the service, so the fail-closed rules live in ONE place.
type ActionsPageRequest struct {
	Limit  *int32
	Cursor *string
}

// ActionsPage is ONE bounded page of the actions queue plus its truthful
// completeness signal. HasMore reports whether older matching actions exist beyond
// this page; NextCursor is the opaque continuation token for them (nil when
// HasMore is false). A caller can therefore always distinguish "this is the whole
// queue" from "there is more" — the distinction the silent 500-row clamp destroyed.
type ActionsPage struct {
	Items      []db.ListApprovalCardsPageRow
	NextCursor *string
	HasMore    bool
}

// resolveActionsLimit applies the §17 bound: an omitted or non-positive limit gets
// the conservative default; a limit ABOVE the maximum fails closed. The result is
// always in [1, MaxActionsLimit].
func resolveActionsLimit(requested *int32) (int32, error) {
	if requested == nil || *requested <= 0 {
		return defaultActionsLimit, nil
	}
	if *requested > MaxActionsLimit {
		return 0, ErrLimitAboveMax
	}
	return *requested, nil
}

// ListActionsPage returns ONE bounded, keyset-paginated page of the account's
// actions queue, newest first, optionally narrowed to a single §8.4 state (the
// predicate is applied in SQL, BEFORE the page bound — issue #142).
//
// The projection is PD-4 rule (1) for issue #106: current lineage heads UNION card
// versions that carry an execution. A page therefore may hold SEVERAL versions of
// one action lineage (an executed version and the newer Draft that superseded it),
// which is why the execution overlay above this read is keyed by CARD id, never by
// action id.
//
// Completeness is EXPLICIT: it fetches limit+1 rows, uses the extra row as the
// hasMore signal, trims it, and mints the continuation cursor from the last
// RETURNED row. Nothing is ever silently dropped.
//
// Fail-closed inputs (§4.6): a limit above MaxActionsLimit is ErrLimitAboveMax; a
// malformed, tampered, or foreign-account cursor is ErrInvalidCursor. The account
// predicate — resolved upstream from the authenticated org — is the authorization;
// the cursor only names a position within it.
func (s *Service) ListActionsPage(ctx context.Context, account uuid.UUID, stateFilter string, req ActionsPageRequest) (ActionsPage, error) {
	limit, err := resolveActionsLimit(req.Limit)
	if err != nil {
		// The fail-closed page cap is a never-cut boundary: its refusals are COUNTED,
		// never inferred from an absence of rows (CLAUDE.md §SRE).
		s.tel().pageLimitRejected(ctx, seamListActionsPage)
		return ActionsPage{}, err
	}
	params := db.ListApprovalCardsPageParams{
		MarketplaceAccountID: account,
		PageLimit:            limit + 1, // the +1 probe row is the hasMore signal.
	}
	if stateFilter != "" {
		params.State = pgtype.Text{String: stateFilter, Valid: true}
	}
	if req.Cursor != nil {
		cur, err := keyset.Decode(*req.Cursor)
		if err != nil {
			return ActionsPage{}, err
		}
		// A cursor minted for ANOTHER account is rejected outright rather than
		// reinterpreted: defense in depth beside the account-scoped predicate.
		if cur.Account != account {
			return ActionsPage{}, ErrInvalidCursor
		}
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cur.CreatedAt, Valid: true}
		params.CursorID = pgtype.UUID{Bytes: cur.ID, Valid: true}
	}

	rows, err := db.New(s.pool).ListApprovalCardsPage(ctx, params)
	if err != nil {
		return ActionsPage{}, err
	}
	page := ActionsPage{HasMore: int32(len(rows)) > limit}
	if page.HasMore {
		rows = rows[:limit]
	}
	page.Items = rows
	if page.HasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		tok := keyset.Encode(account, last.CreatedAt, last.ID)
		page.NextCursor = &tok
	}
	return page, nil
}

// seamListActionsPage / seamListActions name the bounded-read seams in telemetry.
// They are stable operator-facing identifiers, never localized copy (LOC-001).
const (
	seamListActionsPage = "list_actions_page"
	seamListActions     = "list_actions"
	seamBulkConfirm     = "confirm_bulk_selection"
)

// ListActions returns the account's actions queue, newest first, bounded by
// limit (PD-3 item 5). A non-empty stateFilter narrows to that exact §8.4 state;
// empty returns every state.
//
// The projection is PD-4 rule (1) for issue #106: current lineage heads UNION
// card versions that carry an execution (write action_executions OR EXE-005
// recommend_only_actions), deduplicated by card id. A terminal executed card
// version therefore stays visible through the common action API even after the
// domain mints a newer Draft on the same action lineage — preserving EXE-005 /
// OUT-001 / AUD-001 visibility for the DEFAULT (recommend-only) execution mode.
//
// It FAILS CLOSED on an over-maximum limit exactly as ListActionsPage does (issue
// #90 fix cycle 1, F9): leaving a silently-clamping read beside a fail-closed one
// invites the clamp defect straight back. A non-positive limit still means "apply
// the conservative default" — that is an absent request, not an out-of-contract one.
//
// The state predicate is AUTHORITATIVE and applied in SQL, over the UNIONED set,
// BEFORE LIMIT (issue #142) — a page bounds MATCHING rows, never an unfiltered
// newest-N prefix, so an older matching row is never hidden behind newer
// non-matching ones. Tenant scoping stays account-scoped on BOTH branches; the
// account arg is resolved upstream.
//
// It is NOT the request path (issue #90 blocker 3): GET /actions reads
// ListActionsPage, which projects the SAME PD-4 set with an explicit completeness
// signal. This bounded read is retained for internal fixed-bound callers.
//
// It is a pure read over append-only history: no card version is rewritten,
// collapsed, or re-stamped (approval versioning is never-cut, §4.6).
func (s *Service) ListActions(ctx context.Context, account uuid.UUID, stateFilter string, limit int32) ([]db.ApprovalCard, error) {
	resolved, err := resolveActionsLimit(&limit)
	if err != nil {
		s.tel().pageLimitRejected(ctx, seamListActions)
		return nil, err
	}
	limit = resolved
	q := db.New(s.pool)
	if stateFilter == "" {
		return q.ListApprovalCardsByAccount(ctx, db.ListApprovalCardsByAccountParams{
			MarketplaceAccountID: account,
			Limit:                limit,
		})
	}
	return q.ListApprovalCardsByAccountAndState(ctx, db.ListApprovalCardsByAccountAndStateParams{
		MarketplaceAccountID: account,
		State:                stateFilter,
		Limit:                limit,
	})
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
		disp := dispositionOf(row)
		views = append(views, PreviewMemberView{VariantID: m.VariantID, RecommendationID: m.RecommendationID, Disposition: disp})
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
