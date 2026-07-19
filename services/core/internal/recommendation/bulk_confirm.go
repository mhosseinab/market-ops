// Authoritative bulk approval (issue #90). A bulk confirmation is an OPERATIONAL
// seam, not a version-validity assertion: it binds ONE exact, immutable
// selection-set version (#91), then durably AUTHORIZES each executable member
// through the SAME §8.4 individual-confirm path (ConfirmIndividual) — never a
// bulk-only shortcut — and returns explicit per-item results with safe
// partial-failure / resume semantics.
//
// Never-cut invariants this seam carries (PRD §4.6):
//   - Approval versioning: a stale bound version authorizes NOTHING (fail closed);
//     each member is authorized only through the version-bound structured control
//     its live card already holds.
//   - Idempotency: re-confirming produces AT MOST ONE authorization/action per
//     eligible member (the individual path is FROM-guarded and the execution intent
//     is unique by card id), so a replay/resume reports already_authorized and never
//     re-dispatches.
//   - Tenant integrity: a member card is authorized only when it belongs to the
//     SAME account as the selection set — a cross-account card is rejected, never
//     approved.
//   - Free text never approves: bulk carries no free text; only the pre-existing
//     structured control on each member's AwaitingConfirmation card can reach
//     Approved, and blocked/warning members carry no control and never execute.
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
)

// BulkItemState is a per-member bulk-confirmation outcome. Only Authorized and
// AlreadyAuthorized mean the member carries (or already carried) a durable
// authorization + execution intent; every other state means the member did NOT
// execute this call.
type BulkItemState string

const (
	// BulkItemAuthorized — the member's live control was activated THIS call:
	// Approved + exactly one execution intent enqueued.
	BulkItemAuthorized BulkItemState = "authorized"
	// BulkItemAlreadyAuthorized — an idempotent replay: the member's card was
	// already Approved by a prior confirmation, so no second authorization/intent is
	// created. This is the resume-safe terminal for an already-processed member.
	BulkItemAlreadyAuthorized BulkItemState = "already_authorized"
	// BulkItemExcluded — a blocked or warning member: never approvable in bulk, so it
	// is reported and skipped, never executed.
	BulkItemExcluded BulkItemState = "excluded"
	// BulkItemInvalidated — an executable member whose live card is no longer a
	// bindable control (superseded, expired, cross-account, or absent): fails closed,
	// never executes, and is not retriable into execution.
	BulkItemInvalidated BulkItemState = "invalidated"
	// BulkItemFailed — a TRANSIENT failure authorizing an otherwise-eligible member
	// (e.g. a dispatch/store error). The member's card stays a live control, so a
	// resume (re-confirm) retries exactly this member; nothing is half-committed.
	BulkItemFailed BulkItemState = "failed"
)

// BulkItemResult is one selection-set member's authoritative bulk outcome. The
// disposition is the SERVER-sealed disposition of the bound version (immutable per
// version, #91) — never a client assertion.
type BulkItemResult struct {
	VariantID        uuid.UUID
	RecommendationID uuid.UUID
	Disposition      Disposition
	State            BulkItemState
	Reason           string
}

// BulkConfirmOutcome is the authoritative result of a bulk confirmation. Valid is
// false when the bound version is no longer current (any set/evidence change minted
// a new version); in that case NOTHING is authorized and Items is empty. When Valid,
// Items carries one durable per-item result for every member of the bound version.
type BulkConfirmOutcome struct {
	Lineage          uuid.UUID
	BoundVersion     int32
	CurrentVersion   int32
	Valid            bool
	ExecutionPending bool
	Items            []BulkItemResult
}

// ConfirmBulkSelection confirms a bulk approval bound to ONE exact selection-set
// version and durably authorizes each executable member (issue #90, CHAT-052).
//
// It first binds the version: the confirmation is valid ONLY when boundVersion is
// the current (greatest) version of the lineage. Because membership is immutable per
// version (#91), binding the version transitively binds the EXACT membership,
// dispositions, and aggregate the operator reviewed — a stale bound version
// authorizes NOTHING (fail closed). When valid, it walks the bound version's sealed
// members and, for each EXECUTABLE member, authorizes its live card through the SAME
// individual §8.4 confirm path (so every control-bearing / authoritative-current /
// expiry / tenant gate applies and bulk can bypass none of them). Blocked and
// warning members are reported excluded and never execute. Each executable member is
// authorized in its OWN transaction (inside ConfirmIndividual), so one member's
// failure never rolls back another's authorization — partial failure is durable and
// a resume retries only the still-eligible members.
func (s *Service) ConfirmBulkSelection(ctx context.Context, lineage uuid.UUID, boundVersion int32, now time.Time) (BulkConfirmOutcome, error) {
	q := db.New(s.pool)
	current, err := q.GetCurrentSelectionSet(ctx, lineage)
	if err != nil {
		return BulkConfirmOutcome{}, err // pgx.ErrNoRows ⇒ unknown lineage (404 at transport).
	}
	out := BulkConfirmOutcome{
		Lineage:        lineage,
		BoundVersion:   boundVersion,
		CurrentVersion: current.Version,
	}
	if current.Version != boundVersion {
		// Stale binding: the set/evidence changed and minted a new version. Fail
		// closed — authorize NOTHING, execute NOTHING (APR-001 / CHAT-052).
		return out, nil
	}
	out.Valid = true

	members, err := q.ListSelectionSetMembers(ctx, current.ID)
	if err != nil {
		return BulkConfirmOutcome{}, err
	}
	out.Items = make([]BulkItemResult, 0, len(members))
	authorizedAny := false
	for _, m := range members {
		item := BulkItemResult{
			VariantID:        m.VariantID,
			RecommendationID: uuidFromPg(m.RecommendationID),
			Disposition:      Disposition(m.Disposition),
		}
		if item.Disposition != DispositionExecutable {
			// Blocked / warning members are never approvable in bulk — reported and
			// skipped, never executed.
			item.State = BulkItemExcluded
			item.Reason = m.Disposition
			out.Items = append(out.Items, item)
			continue
		}
		s.authorizeBulkMember(ctx, current.MarketplaceAccountID, &item, now)
		if item.State == BulkItemAuthorized || item.State == BulkItemAlreadyAuthorized {
			authorizedAny = true
		}
		out.Items = append(out.Items, item)
	}
	// ExecutionPending reports that at least one member now carries a durable,
	// pending execution authorization — never a bare "the version was valid" signal.
	out.ExecutionPending = authorizedAny
	return out, nil
}

// authorizeBulkMember resolves an executable member's live approval card and
// authorizes it through the individual §8.4 confirm path, mutating item in place. It
// NEVER approves directly: it re-resolves the recommendation's current card, enforces
// tenant integrity, and delegates to ConfirmIndividual, whose gates fail closed for a
// superseded, expired, non-control-bearing, or already-decided card.
func (s *Service) authorizeBulkMember(ctx context.Context, account uuid.UUID, item *BulkItemResult, now time.Time) {
	if item.RecommendationID == uuid.Nil {
		item.State = BulkItemInvalidated
		item.Reason = "no_recommendation"
		return
	}
	card, err := db.New(s.pool).GetCurrentApprovalCardByRecommendation(ctx, item.RecommendationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No card ⇒ nothing to authorize; fail closed rather than fabricate one.
			item.State = BulkItemInvalidated
			item.Reason = "no_live_card"
			return
		}
		item.State = BulkItemFailed
		item.Reason = "card_lookup_failed"
		return
	}
	// Tenant integrity (never-cut): only authorize a card that belongs to the SAME
	// account as the selection set. A cross-account card is rejected, never approved.
	if card.MarketplaceAccountID != account {
		item.State = BulkItemInvalidated
		item.Reason = "account_mismatch"
		return
	}

	domainCard, err := cardFromDB(card)
	if err != nil {
		item.State = BulkItemFailed
		item.Reason = "card_decode_failed"
		return
	}
	// Present the card's OWN authoritative binding: the operator authorized the
	// reviewed selection version, and each member rides its pre-existing structured
	// control. ConfirmIndividual re-verifies control-bearing, authoritative-current,
	// and expiry against the live card, so a changed/superseded/expired member fails
	// closed here — bulk cannot approve what an individual confirm could not.
	outcome, err := s.ConfirmIndividual(ctx, card.ID, domainCard.Binding, now)
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrNoControl):
			// Not control-bearing. An already-Approved card is a prior authorization
			// (idempotent replay / resume) — report already_authorized and NEVER
			// re-dispatch. Any other non-control state fails closed as invalidated.
			if card.State == string(approval.StateApproved) {
				item.State = BulkItemAlreadyAuthorized
				item.Reason = "already_authorized"
				return
			}
			item.State = BulkItemInvalidated
			item.Reason = "not_control_bearing"
			return
		case errors.Is(err, pgx.ErrNoRows):
			item.State = BulkItemInvalidated
			item.Reason = "no_live_card"
			return
		default:
			// A transient error (e.g. a dispatch/store failure rolled the individual
			// confirm back): the card stays a live control, so a resume retries it.
			item.State = BulkItemFailed
			item.Reason = "authorize_failed"
			return
		}
	}
	if outcome.State == approval.StateApproved {
		item.State = BulkItemAuthorized
		item.Reason = "authorized"
		return
	}
	// Invalidated / Expired: the member's binding changed or lapsed — fail closed,
	// no execution.
	item.State = BulkItemInvalidated
	if outcome.Reason != approval.ReasonNone {
		item.Reason = string(outcome.Reason)
	} else {
		item.Reason = string(outcome.State)
	}
}

// uuidFromPg converts a nullable pgtype.UUID member column to a plain uuid.UUID; an
// invalid (NULL) value becomes uuid.Nil, which authorizeBulkMember fails closed on.
func uuidFromPg(v pgtype.UUID) uuid.UUID {
	if !v.Valid {
		return uuid.Nil
	}
	return v.Bytes
}
