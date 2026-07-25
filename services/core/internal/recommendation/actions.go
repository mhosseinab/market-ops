// Actions queue reads (PD-3 item 5): the bounded, keyset-paginated request-path
// read and the fixed-bound read retained for internal callers. Both fail closed
// on an out-of-contract page limit.
package recommendation

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/keyset"
)

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
