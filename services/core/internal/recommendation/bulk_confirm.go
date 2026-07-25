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
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/reservation"
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
	// BulkItemAlreadyAuthorized — an idempotent replay: the member's structured
	// control was already ACTIVATED by another confirmation — a PRIOR one (resume) or
	// a CONCURRENT one that committed first (a double-clicked confirm / a retried
	// request, issue #90 fix cycle 2) — so no second authorization/intent is created.
	// The outcome is SEALED — it is reported for a
	// card that is Approved AND for one that has since advanced downstream
	// (Revalidating, Executing, or a terminal external result: Accepted, Rejected,
	// PendingReconciliation, Failed). This is the resume-safe terminal for an
	// already-processed member; re-attempting a member whose EXECUTION failed is the
	// reconciliation-gated /actions retry path's decision, never a second bulk
	// authorization.
	BulkItemAlreadyAuthorized BulkItemState = "already_authorized"
	// BulkItemExcluded — a blocked or warning member: never approvable in bulk, so it
	// is reported and skipped, never executed.
	BulkItemExcluded BulkItemState = "excluded"
	// BulkItemInvalidated — an executable member whose live card is no longer a
	// bindable control (superseded, expired, cross-account, or absent): fails closed,
	// never executes, and is not retriable into execution.
	BulkItemInvalidated BulkItemState = "invalidated"
	// BulkItemFailed — this call did not authorize an otherwise-eligible member and
	// nothing is half-committed: either a TRANSIENT failure (a dispatch/store error
	// rolled the individual confirm back, leaving the card a live control) or the
	// member's outcome could not be DETERMINED (its state re-read failed). Both are
	// resume-safe: a re-confirm retries a still-live control and re-derives an
	// undetermined outcome. `failed` never means the member's authorization was
	// voided — that is `invalidated` — and it is never reported for a member a
	// concurrent confirmation durably approved (that is `already_authorized`).
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
	// OfferIdentity is the SERVER-SEALED observed-offer identity of the member,
	// read from the sealed member row of the BOUND version (issue #87 criterion D,
	// BULK-PROTOCOL DESIGN RECORD (e)). Preview and execution therefore report the
	// SAME explicit identity: it is never re-derived by a lookup-by-target at confirm
	// time, which could resolve a different sibling offer if the target's observations
	// changed after the operator reviewed the preview.
	//
	// "" is EXPLICIT ABSENCE (a member sealed from a recommendation with no evidence
	// observation, or a version sealed before #87), never a stand-in for another offer.
	OfferIdentity string
}

// BulkConfirmOutcome is the authoritative result of a bulk confirmation. Valid is
// false when the bound version is no longer current (any set/evidence change minted
// a new version); in that case NOTHING is authorized and Items is empty. When Valid,
// Items carries one durable per-item result for every member of the bound version.
//
// ExecutionPending is true only while at least one member carries a LIVE,
// still-unresolved execution authorization (Approved / Revalidating / Executing). It
// is NOT implied by an authorized item: a resume whose members have all reached an
// external result reports already_authorized per item with ExecutionPending false.
type BulkConfirmOutcome struct {
	Lineage          uuid.UUID
	BoundVersion     int32
	CurrentVersion   int32
	Valid            bool
	ExecutionPending bool
	Items            []BulkItemResult
}

// ConfirmBulkSelection confirms a bulk approval bound to ONE exact selection-set
// version and durably authorizes each executable member (issue #90, CHAT-052). It is
// the AUTHORITATIVE, account-scoped confirmation entry point: `account` is the
// caller's own resolved marketplace account (threaded down from
// ConfirmBulkSelectionForOrg, never taken from request input), and there is NO
// unscoped variant — a bulk confirmation cannot be reached without naming the tenant
// it is predicated on.
//
// Version binding is decided INSIDE one transaction that holds the per-lineage lock
// (issue #90 blocker 1): the lock is taken first, then the ACCOUNT-SCOPED current
// version is read, then the bound version's sealed membership is snapshotted — so a
// concurrent refresh can neither interleave between the read and the membership
// snapshot nor let a foreign lineage resolve. A lineage that is not the caller's
// matches no row and yields pgx.ErrNoRows: indistinguishable from a missing lineage
// (no existence oracle), with no read of foreign members and no side effect.
//
// The confirmation is valid ONLY when boundVersion is the current (greatest) version
// of the lineage. Because membership is immutable per version (#91), binding the
// version transitively binds the EXACT membership, dispositions, and aggregate the
// operator reviewed — a stale bound version authorizes NOTHING (fail closed). When
// valid, it walks the bound version's sealed members and, for each EXECUTABLE member,
// authorizes its live card through the SAME individual §8.4 confirm path (so every
// control-bearing / authoritative-current / expiry / tenant gate applies and bulk can
// bypass none of them). Blocked and warning members are reported excluded and never
// execute.
//
// The binding transaction COMMITS (releasing the lineage lock) before the member loop
// runs: each executable member is then authorized in its OWN transaction (inside
// ConfirmIndividual), so one member's failure never rolls back another's authorization
// — partial failure is durable and a resume retries only the still-eligible members.
// Holding the selection-lineage lock across the loop is deliberately avoided: the loop
// acquires a second pooled connection per member, and nesting that under a held lock
// would couple a tenant-isolation lock to pool availability. Nothing is weakened by
// releasing it — each member's own confirm re-verifies its own APR-001 binding under
// its own card-lineage lock, so a member superseded after the snapshot still fails
// closed.
//
// BULK-PROTOCOL DESIGN RECORD (f) — BINDING IS DECIDED AT BIND TIME (issue #90 fix
// cycle 1, M3). Currency of the bound version is evaluated ONCE, inside the binding
// transaction, under the lineage lock. A refresh that commits a NEWER version AFTER
// that point does NOT retract the in-flight confirmation: the loop continues over the
// version's SEALED membership, so a member dropped by that later version can still be
// authorized. This is deliberate — the operator approved exactly the membership,
// dispositions, and aggregate v_bound sealed, and a client-driven narrowing is not
// retroactive.
//
// It is NOT an evidence/policy escape. Every server-side evidence, price, cost,
// policy, or boundary change mints a NEW CARD version and is caught PER MEMBER by
// ConfirmIndividual's authoritative-lineage/binding gate, which fails that member
// closed as invalidated. The only thing this window admits is a client-driven
// membership NARROWING racing an already-authorized confirmation — asserted, not
// assumed, by
// TestConfirmBulkSelection_RefreshDuringMemberLoopDoesNotRetractTheBinding. A stale
// bound version presented on a LATER call still authorizes nothing.
func (s *Service) ConfirmBulkSelection(ctx context.Context, account, lineage uuid.UUID, boundVersion int32, now time.Time, actor audit.Actor) (BulkConfirmOutcome, error) {
	current, members, err := s.bindSelectionVersion(ctx, account, lineage, boundVersion)
	if err != nil {
		return BulkConfirmOutcome{}, err
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
	out.Items = make([]BulkItemResult, 0, len(members))
	pendingAny := false
	for _, m := range members {
		item := BulkItemResult{
			VariantID:        m.VariantID,
			RecommendationID: uuidFromPg(m.RecommendationID),
			Disposition:      Disposition(m.Disposition),
			// From the SEALED member row of the bound version — not a fresh
			// lookup-by-target (issue #87 criterion D).
			OfferIdentity: m.OfferIdentity,
		}
		if item.Disposition != DispositionExecutable {
			// Blocked / warning members are never approvable in bulk — reported and
			// skipped, never executed.
			item.State = BulkItemExcluded
			item.Reason = m.Disposition
			out.Items = append(out.Items, item)
			continue
		}
		// The tenant is the CALLER's own resolved account (threaded down from
		// ConfirmBulkSelectionForOrg), never a field of a row this loop just read.
		// bindSelectionVersion already matched the set on that same account, so the
		// two are provably equal — sourcing it from the caller keeps the tenant
		// predicate anchored to the authorization rather than to persisted data.
		if s.authorizeBulkMember(ctx, account, provenanceOf(current, m), &item, now, actor) {
			pendingAny = true
		}
		out.Items = append(out.Items, item)
	}
	// ExecutionPending reports that at least one member now carries a LIVE,
	// still-unresolved execution authorization (approval.StateHasPendingExecution:
	// Approved / Revalidating / Executing) — never a bare "the version was valid"
	// signal, and never a member whose write has already produced an external result.
	// A resume over members whose executions have all terminated (accepted, rejected,
	// failed, or awaiting reconciliation) reports each item already_authorized — the
	// authorization IS sealed — while ExecutionPending is false, because nothing is in
	// flight (issue #90 fix cycle 1, M1).
	out.ExecutionPending = pendingAny
	return out, nil
}

// bindSelectionVersion resolves, in ONE transaction under the per-lineage lock, the
// caller's ACCOUNT-SCOPED current selection-set version and (when the bound version
// is that current version) its sealed membership. Taking the lock BEFORE the read is
// what makes the binding decision atomic against a concurrent refresh: a refresh that
// already holds the lock is waited on, so the confirmation can never bind a version
// that a committed refresh has already superseded. The transaction is read-only — it
// writes nothing — and commits before any member is authorized.
//
// A lineage owned by another account matches no row (GetCurrentSelectionSetForAccount)
// and returns pgx.ErrNoRows, the same as a missing lineage: no disclosure, no foreign
// member read, no side effect.
func (s *Service) bindSelectionVersion(ctx context.Context, account, lineage uuid.UUID, boundVersion int32) (db.SelectionSet, []db.SelectionSetMember, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.SelectionSet{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	if err := q.LockApprovalLineage(ctx, lineage); err != nil {
		return db.SelectionSet{}, nil, err
	}
	current, err := q.GetCurrentSelectionSetForAccount(ctx, db.GetCurrentSelectionSetForAccountParams{
		LineageID:            lineage,
		MarketplaceAccountID: account,
	})
	if err != nil {
		return db.SelectionSet{}, nil, err // pgx.ErrNoRows ⇒ unknown OR foreign lineage (404 at transport).
	}
	if current.Version != boundVersion {
		// Stale: do not read the members of a version the caller is not bound to.
		if err := tx.Commit(ctx); err != nil {
			return db.SelectionSet{}, nil, err
		}
		return current, nil, nil
	}
	members, err := q.ListSelectionSetMembers(ctx, current.ID)
	if err != nil {
		return db.SelectionSet{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.SelectionSet{}, nil, err
	}
	return current, members, nil
}

// authorizeBulkMember resolves an executable member's live approval card and
// authorizes it through the individual §8.4 confirm path, mutating item in place. It
// NEVER approves directly: it re-resolves the recommendation's current card, enforces
// tenant integrity, and delegates to ConfirmIndividual, whose gates fail closed for a
// superseded, expired, non-control-bearing, or already-decided card.
//
// It returns whether this member now carries a LIVE, still-unresolved execution
// authorization (approval.StateHasPendingExecution) — the ONLY input to the
// outcome's ExecutionPending. A sealed-but-terminated member (already_authorized on
// a card that has reached an external result) returns false: its authorization
// stands, but nothing is in flight.
// It carries the member's sealed `prov` (issue #87): on the fresh-authorization arm it
// is appended as the durable bulk provenance ledger row, and on the sealed-authorization
// arm it is the exact provenance a replay must MATCH before `already_authorized` may
// be reported (prior finding 1).
func (s *Service) authorizeBulkMember(ctx context.Context, account uuid.UUID, prov bulkProvenance, item *BulkItemResult, now time.Time, actor audit.Actor) bool {
	if item.RecommendationID == uuid.Nil {
		item.State = BulkItemInvalidated
		item.Reason = "no_recommendation"
		return false
	}
	card, err := db.New(s.pool).GetCurrentApprovalCardByRecommendation(ctx, item.RecommendationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No card ⇒ nothing to authorize; fail closed rather than fabricate one.
			item.State = BulkItemInvalidated
			item.Reason = "no_live_card"
			return false
		}
		item.State = BulkItemFailed
		item.Reason = "card_lookup_failed"
		return false
	}
	// Tenant integrity (never-cut): only authorize a card that belongs to the SAME
	// account as the selection set. A cross-account card is rejected, never approved.
	if card.MarketplaceAccountID != account {
		item.State = BulkItemInvalidated
		item.Reason = "account_mismatch"
		return false
	}

	domainCard, err := cardFromDB(card)
	if err != nil {
		item.State = BulkItemFailed
		item.Reason = "card_decode_failed"
		return false
	}
	// Present the card's OWN authoritative binding: the operator authorized the
	// reviewed selection version, and each member rides its pre-existing structured
	// control. ConfirmIndividual re-verifies control-bearing, authoritative-current,
	// and expiry against the live card, so a changed/superseded/expired member fails
	// closed here — bulk cannot approve what an individual confirm could not.
	outcome, err := s.confirmIndividual(ctx, card.ID, domainCard.Binding, now, actor, &prov)
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrNoControl), errors.Is(err, ErrRejectedTransition):
			// This call activated NOTHING, for one of the two ways an already-decided
			// member refuses a (re-)confirmation:
			//   - ErrNoControl: the card was no longer AwaitingConfirmation when the
			//     individual confirm read it (a completed prior confirmation / resume);
			//   - ErrRejectedTransition: the card WAS read as AwaitingConfirmation, but a
			//     CONCURRENT confirmation of the same member committed first, so the
			//     FROM-guarded advance matched no row (issue #90 fix cycle 2, C1). A
			//     double-clicked bulk confirm, or a client retry of a confirmation whose
			//     response was lost, produces exactly this. It is NOT a transient
			//     authorize failure: the member is durably approved and its write is in
			//     flight, so reporting it `failed` with nothing pending told the operator
			//     the opposite of the truth.
			//
			// The authorization outcome is SEALED: any card whose structured control was
			// already ACTIVATED is a prior authorization — report already_authorized and
			// NEVER re-authorize or re-dispatch. The predicate is the §8.4 machine's own
			// domain knowledge (approval.StateHasAuthorized), so a member that
			// legitimately ADVANCED past Approved (Revalidating, Executing, or a terminal
			// external result) is no longer mislabelled invalidated / not_control_bearing
			// — the issue #90 blocker-2 defect, which also blocked a resume from retrying
			// the members that were still eligible.
			//
			// Retrying a member whose EXECUTION failed is deliberately NOT this seam's
			// job: it stays already_authorized here, and re-attempting it goes through
			// the reconciliation-gated retry path (execution.Retry — an unknown result
			// must reconcile first, only a definitively Failed action is eligible,
			// EXE-003 / §16). A bulk resume is never a back door around that gate.
			// Only states that were NEVER authorized, or whose authorization was voided
			// (Invalidated, Expired, Blocked, or a pre-activation state), fail closed
			// as invalidated below.
			//
			// The state is re-read FRESH here rather than taken from the pre-confirm
			// snapshot above: that snapshot is a pool read taken BEFORE the individual
			// confirm ran, so a concurrent winner committing in between would have both
			// mislabelled a sealed member (invalidated / not_control_bearing) and
			// reported a stale ExecutionPending. After ErrNoControl / ErrRejectedTransition
			// the card's state is monotonically at-or-after the confirm attempt, so a
			// fresh read cannot regress — it is the only state this arm may reason about.
			state, ok := s.currentCardState(ctx, item.RecommendationID)
			if !ok {
				// The outcome could not be DETERMINED (the state read failed). Fail
				// closed on the report, never on a guess: claim no authorization and
				// nothing pending, and let a resume re-derive the real outcome.
				item.State = BulkItemFailed
				item.Reason = "state_read_failed"
				return false
			}
			if approval.StateHasAuthorized(state) {
				// PRIOR FINDING 1 (issue #87): "the control was activated" is NOT
				// evidence that THIS selection activated it. `already_authorized` is a
				// claim about this selection, so it requires this selection's own
				// durable provenance to match EXACTLY — set, member, lineage, version,
				// variant, recommendation, and sealed offer identity.
				switch matchBulkBinding(ctx, db.New(s.pool), prov, card) {
				case bindingAbsent:
					// The card WAS authorized, but not by this selection: an individual
					// §8.4 confirmation, or a different selection set. Reporting
					// already_authorized here told the operator this selection had done
					// something it never did. Fail closed instead — the card is not a
					// bindable control for THIS selection, and (like every other
					// invalidated member) it is not retriable into execution. It is NOT
					// `failed`: `failed` promises a resume can still authorize it, and
					// no resume of this selection ever will.
					s.tel().bulkProvenanceMismatch(ctx, seamBulkConfirm, prov.SetID, prov.MemberID)
					item.State = BulkItemInvalidated
					item.Reason = "authorized_outside_selection"
					return false
				case bindingUndetermined:
					// The provenance read failed: the outcome is UNKNOWN. Never guessed
					// in either direction — report an undetermined result and let a
					// resume re-derive it (quarantine over inference, §4.6).
					item.State = BulkItemFailed
					item.Reason = "binding_read_failed"
					return false
				}
				item.State = BulkItemAlreadyAuthorized
				item.Reason = "already_authorized"
				// The idempotency boundary (§4.6): "this replay authorized nothing a
				// second time" is COUNTED, not merely implied by the absence of a
				// duplicate intent.
				s.tel().sealedAuthorizationOnResume(ctx, seamBulkConfirm)
				// Sealed ≠ in flight: only a card still upstream of an external result
				// (Approved / Revalidating / Executing) contributes to ExecutionPending.
				// A member whose write already produced a result — accepted, rejected,
				// failed, or awaiting reconciliation — stays already_authorized while
				// reporting nothing pending (issue #90 fix cycle 1, M1).
				return approval.StateHasPendingExecution(state)
			}
			item.State = BulkItemInvalidated
			item.Reason = "not_control_bearing"
			return false
		case errors.Is(err, reservation.ErrVariantReserved):
			// BULK-PROTOCOL DESIGN RECORD (a) / prior finding 2: another card already
			// holds an in-flight write on this owned VARIANT — the sibling-offer /
			// two-selection-lineages case. The confirmation rolled back entirely, so
			// the member's card is still a LIVE control and NOTHING was dispatched:
			// this is `failed`'s exact documented meaning (transient, resume-safe,
			// nothing half-committed), not `invalidated`. A resume authorizes it once
			// the holder reaches a DEFINITE external result.
			//
			// The reason is a stable, non-localized diagnostic key so an operator can
			// tell this apart from a store/dispatch failure (§4.6: errors are
			// actionable and name the failing seam).
			item.State = BulkItemFailed
			item.Reason = "variant_reservation_held"
			return false
		case errors.Is(err, pgx.ErrNoRows):
			item.State = BulkItemInvalidated
			item.Reason = "no_live_card"
			return false
		default:
			// A transient error (e.g. a dispatch/store failure rolled the individual
			// confirm back): the card stays a live control, so a resume retries it.
			item.State = BulkItemFailed
			item.Reason = "authorize_failed"
			return false
		}
	}
	if outcome.State == approval.StateApproved {
		item.State = BulkItemAuthorized
		item.Reason = "authorized"
		// Freshly activated: the intent enqueued by this call is live by construction.
		return approval.StateHasPendingExecution(approval.StateApproved)
	}
	// Invalidated / Expired: the member's binding changed or lapsed — fail closed,
	// no execution.
	item.State = BulkItemInvalidated
	if outcome.Reason != approval.ReasonNone {
		item.Reason = string(outcome.Reason)
	} else {
		item.Reason = string(outcome.State)
	}
	return false
}

// currentCardState re-reads the CURRENT approval card of a member's recommendation
// and returns its §8.4 state. It is the fresh-read seam the sealed-authorization arm
// reasons about: a state decided from a snapshot taken before the confirm attempt can
// be stale by exactly the race that produced the error. A read failure returns
// ok=false — the caller reports an undetermined outcome rather than inferring one.
func (s *Service) currentCardState(ctx context.Context, recommendationID uuid.UUID) (approval.State, bool) {
	card, err := db.New(s.pool).GetCurrentApprovalCardByRecommendation(ctx, recommendationID)
	if err != nil {
		return "", false
	}
	return approval.State(card.State), true
}

// uuidFromPg converts a nullable pgtype.UUID member column to a plain uuid.UUID; an
// invalid (NULL) value becomes uuid.Nil, which authorizeBulkMember fails closed on.
func uuidFromPg(v pgtype.UUID) uuid.UUID {
	if !v.Valid {
		return uuid.Nil
	}
	return v.Bytes
}
