package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// ErrRejectedTransition is returned when a §8.4 advance is refused: either the
// move is undefined (approval.Advance rejects it) or the card already left the
// expected from-state (the FROM-guarded UPDATE matched no row). Either way, no
// state is mutated — the machine fails closed.
var ErrRejectedTransition = errors.New("recommendation: approval state transition rejected")

// ExecutionDispatcher durably enqueues the server-side execution intent for a card
// that has just committed Approved (issue #92, S18 EXE-* / AUD-001). Its enqueue
// runs INSIDE the confirm transaction (tx), so the intent commits ATOMICALLY with
// the Approved state: a rollback discards BOTH, and a committed Approved always
// carries its durable intent. An acknowledged confirmation therefore never depends
// on a second client request to reach revalidation / recommend-only processing.
type ExecutionDispatcher interface {
	DispatchApprovedTx(ctx context.Context, tx pgx.Tx, card db.ApprovalCard) error
}

// Service persists recommendations and approval cards and drives the §8.4 state
// machine over the store. It keeps the pure domain (internal/approval,
// internal/recommendation domain types) free of DB concerns; all persistence and
// append-only discipline live here.
type Service struct {
	pool       *pgxpool.Pool
	dispatcher ExecutionDispatcher
	// auditAppend is the AUD-001 audit-append seam. It defaults to audit.Append and
	// is only swapped by white-box tests (export_test.go) to force an append failure
	// and prove a confirmation rolls back atomically when its audit cannot commit
	// (issue #103). Production always uses audit.Append.
	auditAppend func(ctx context.Context, q *db.Queries, ev audit.Event) (db.AuditRecord, error)
}

// NewService builds a recommendation/approval Service bound to the pool. The
// execution dispatcher is optional (nil until wired via SetExecutionDispatcher):
// without it, a confirmation still commits Approved but enqueues no durable intent
// — the pre-#92 behaviour, kept for the Draft-only chat plane and tests.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, auditAppend: audit.Append}
}

// SetExecutionDispatcher wires the durable execution-intent dispatcher (issue #92).
// It is called once during startup, AFTER the River client exists and BEFORE the
// HTTP server serves, so there is no concurrent access to the field. It returns the
// Service for chaining.
func (s *Service) SetExecutionDispatcher(d ExecutionDispatcher) *Service {
	s.dispatcher = d
	return s
}

// CreateCard persists an approvable recommendation's initial Draft card and
// appends its first §8.4 history row ([*] → Draft). The recommendation must be
// Approvable (a blocked or simulated one has no control-bearing card). lineage is
// the card lineage (a price edit later mints a new version in the same lineage).
func (s *Service) CreateCard(ctx context.Context, recID, lineage uuid.UUID, account uuid.UUID, rec Recommendation) (db.ApprovalCard, error) {
	binding, ok := rec.BuildBinding()
	if !ok {
		return db.ApprovalCard{}, ErrRejectedTransition
	}
	price, ok := rec.ProposedPrice.Get()
	if !ok {
		return db.ApprovalCard{}, ErrRejectedTransition
	}
	return s.mintDraftCard(ctx, recID, lineage, account, binding, price)
}

// mintDraftCard is the SINGLE Draft-minting path (§8.4 [*] → Draft): it inserts
// the initial approval card in state Draft and appends its first append-only
// history row, in ONE transaction. It is TERMINAL AT DRAFT — it never advances
// the state machine and never mints an approval control. Both CreateCard (from a
// live domain recommendation) and the chat Draft-only handlers (from a persisted
// recommendation, chat_drafts.go) go through here, so the machine plane cannot
// reach a different, weaker Draft-creation path.
func (s *Service) mintDraftCard(ctx context.Context, recID, lineage, account uuid.UUID, binding approval.Binding, price money.Money) (db.ApprovalCard, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	card, err := s.mintDraftCardTx(ctx, db.New(tx), recID, lineage, account, binding, price)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.ApprovalCard{}, err
	}
	return card, nil
}

// mintDraftCardTx performs the §8.4 [*] → Draft mint on the caller's transaction q:
// it locks the lineage, inserts the Draft card and appends its first append-only
// history row, WITHOUT begin/commit. It is terminal at Draft (never advances the
// machine, never mints a control). Sharing q lets the runtime producer commit the
// recommendation version and its card ATOMICALLY (ProduceVersion), while the pool-
// bound mintDraftCard wraps this in its own transaction for the standalone callers.
func (s *Service) mintDraftCardTx(ctx context.Context, q *db.Queries, recID, lineage, account uuid.UUID, binding approval.Binding, price money.Money) (db.ApprovalCard, error) {
	evJSON, err := marshalEvidenceVersions(binding.EvidenceVersions)
	if err != nil {
		return db.ApprovalCard{}, err
	}

	// Take the lineage lock BEFORE minting the next version, so a concurrent
	// individual confirm (which locks the same lineage) serializes against this
	// mint: it either sees the pre-mint current version or waits for this new
	// version to commit — a stale control can never approve across the race
	// (APR-001 authoritative-current resolution).
	if err := q.LockApprovalLineage(ctx, lineage); err != nil {
		return db.ApprovalCard{}, err
	}

	card, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID:     recID,
		MarketplaceAccountID: account,
		LineageID:            lineage,
		ActionID:             binding.ActionID,
		ParameterVersion:     binding.ParameterVersion,
		ContextVersion:       binding.ContextVersion,
		PolicyVersion:        binding.PolicyVersion,
		CostProfileVersion:   binding.CostProfileVersion,
		EvidenceVersions:     evJSON,
		IdempotencyKey:       binding.IdempotencyKey(),
		State:                string(approval.StateDraft),
		PriceMantissa:        price.Mantissa(),
		PriceCurrency:        price.Currency(),
		PriceExponent:        int16(price.Exponent()),
		ExpiresAt:            binding.Expiry,
	})
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if _, err := q.AppendApprovalCardState(ctx, db.AppendApprovalCardStateParams{
		CardID:      card.ID,
		CardVersion: card.Version,
		FromState:   pgtype.Text{}, // NULL: the [*] entry.
		ToState:     string(approval.StateDraft),
		Reason:      "card created",
	}); err != nil {
		return db.ApprovalCard{}, err
	}
	return card, nil
}

// Advance moves a card from → to under the §8.4 machine. It validates the move
// with approval.Advance (undefined transitions fail closed), then applies the
// FROM-guarded UPDATE and the append-only history row in ONE transaction. A card
// that already left `from` matches no row and the whole advance is rejected —
// no blind overwrite, and the history stays a faithful, append-only lifecycle.
func (s *Service) Advance(ctx context.Context, cardID uuid.UUID, from, to approval.State, reason string) (db.ApprovalCard, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	card, err := s.AdvanceTx(ctx, db.New(tx), cardID, from, to, reason)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.ApprovalCard{}, err
	}
	return card, nil
}

// AdvanceTx performs the §8.4 move on the caller's transaction q. It applies the
// FROM-guarded UPDATE + the append-only history row on q, so the state change can
// commit ATOMICALLY with whatever else the caller writes in the same transaction
// (e.g. the AUD-001 audit record — a state transition never commits without its
// audit row). It does NOT begin or commit a transaction. An undefined move or a
// stale from-state both fail closed with ErrRejectedTransition and leave q for the
// caller to roll back.
func (s *Service) AdvanceTx(ctx context.Context, q *db.Queries, cardID uuid.UUID, from, to approval.State, reason string) (db.ApprovalCard, error) {
	if err := approval.Advance(from, to); err != nil {
		return db.ApprovalCard{}, ErrRejectedTransition
	}
	card, err := q.AdvanceApprovalCardState(ctx, db.AdvanceApprovalCardStateParams{
		ID:      cardID,
		State:   string(from),
		State_2: string(to),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ApprovalCard{}, ErrRejectedTransition
	}
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if _, err := q.AppendApprovalCardState(ctx, db.AppendApprovalCardStateParams{
		CardID:      card.ID,
		CardVersion: card.Version,
		FromState:   pgtype.Text{String: string(from), Valid: true},
		ToState:     string(to),
		Reason:      reason,
	}); err != nil {
		return db.ApprovalCard{}, err
	}
	return card, nil
}

// History returns the append-only §8.4 lifecycle for a card (AUD-001).
func (s *Service) History(ctx context.Context, cardID uuid.UUID) ([]db.ApprovalCardState, error) {
	return db.New(s.pool).ListApprovalCardStates(ctx, cardID)
}

// GetCard returns a single card by id.
func (s *Service) GetCard(ctx context.Context, id uuid.UUID) (db.ApprovalCard, error) {
	return db.New(s.pool).GetApprovalCard(ctx, id)
}

// GetRecommendation returns a single persisted recommendation row by id
// (PD-3 items 1/3, S37 recommendation-detail read). pgx.ErrNoRows means unknown.
func (s *Service) GetRecommendation(ctx context.Context, id uuid.UUID) (db.Recommendation, error) {
	return db.New(s.pool).GetRecommendation(ctx, id)
}

// ExpireDependentForVariant invalidates every LIVE control-bearing card for a
// variant (§16 "Reopen mapping; expire dependent recommendation"). It is the S11
// identity-reopen consumer: a reopened mapping means the recommendation's identity
// is no longer confirmed, so any card whose control could still authorize a write
// must be driven to Invalidated so a stale approval can never execute against a
// changed identity (§4.6 identity-quarantine + approval-versioning; fail closed).
//
// The §8.4 table (internal/approval) gives most live states a direct → Invalidated
// edge (AwaitingConfirmation, Revalidating). Approved has NO direct edge; per the
// verbatim diagram it reaches Invalidated only via Revalidating, so an Approved
// dependent is invalidated by composing Approved → Revalidating → Invalidated in
// ONE transaction (both FROM-guarded hops + both append-only history rows commit
// atomically — issue #86). Draft/Ready carry no → Invalidated edge and are left as
// is: they bear no activatable control, so they cannot authorize a write.
//
// It returns the number of cards invalidated. Re-delivery is idempotent: an
// already-Invalidated card is terminal and no longer returned by the live query,
// and any card that raced to another state is skipped cleanly (ErrRejectedTransition).
func (s *Service) ExpireDependentForVariant(ctx context.Context, variant uuid.UUID, reason string) (int, error) {
	cards, err := db.New(s.pool).ListLiveCardsForVariant(ctx, variant)
	if err != nil {
		return 0, err
	}
	invalidated := 0
	for _, c := range cards {
		from := approval.State(c.State)
		if err := s.invalidateDependent(ctx, c.ID, from, reason); err != nil {
			if errors.Is(err, ErrRejectedTransition) {
				continue // no defined path from this state, or raced away; skip.
			}
			return invalidated, err
		}
		invalidated++
	}
	return invalidated, nil
}

// invalidateDependent drives a single dependent card from `from` to Invalidated
// along a §8.4-defined path, failing closed if no such path exists. An Approved
// card has no direct → Invalidated edge, so it is advanced through Revalidating in
// one transaction (both hops atomic). States with a direct edge take the single
// hop. States with neither (Draft/Ready) return ErrRejectedTransition and are
// skipped by the caller.
func (s *Service) invalidateDependent(ctx context.Context, cardID uuid.UUID, from approval.State, reason string) error {
	if from == approval.StateApproved {
		return s.invalidateApprovedCard(ctx, cardID, reason)
	}
	if !approval.CanTransition(from, approval.StateInvalidated) {
		return ErrRejectedTransition // draft/ready carry no direct → Invalidated edge (§8.4).
	}
	_, err := s.Advance(ctx, cardID, from, approval.StateInvalidated, reason)
	return err
}

// invalidateApprovedCard invalidates an Approved dependent by composing the two
// §8.4 edges Approved → Revalidating → Invalidated in a SINGLE transaction: both
// FROM-guarded UPDATEs and both append-only history rows commit atomically, so the
// card can never be observed stuck in Revalidating and a partial failure rolls the
// whole invalidation back (issue #86). The diagram is unchanged — no new
// Approved → Invalidated edge is introduced.
func (s *Service) invalidateApprovedCard(ctx context.Context, cardID uuid.UUID, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	if _, err := s.AdvanceTx(ctx, q, cardID, approval.StateApproved, approval.StateRevalidating, reason); err != nil {
		return err
	}
	if _, err := s.AdvanceTx(ctx, q, cardID, approval.StateRevalidating, approval.StateInvalidated, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ConfirmOutcome is the result of activating an individual structured control.
type ConfirmOutcome struct {
	Card             db.ApprovalCard
	State            approval.State
	Reason           approval.InvalidationReason
	ExecutionPending bool
}

// ConfirmIndividual activates the structured control on a card (§8.4 /
// APR-001). In ONE transaction it LOCKS the card's recommendation/card lineage,
// resolves the AUTHORITATIVE CURRENT card for that lineage, and only then
// advances the requested card — so a control can never approve a version that a
// later card (e.g. a price edit, CHAT-044) has already superseded.
//
//   - If the requested card is NOT the current lineage version, it is superseded:
//     it is driven to Invalidated (ReasonSuperseded) with NO execution intent. An
//     exactly-replayed stale binding therefore fails closed instead of approving.
//   - If it IS current, the PRESENTED binding is re-verified against the card's
//     authoritative binding at instant now and persisted as Approved (match),
//     Invalidated (a bound version changed), or Expired (lapsed).
//
// A card that is not control-bearing (not AwaitingConfirmation, or a simulation)
// is refused with approval.ErrNoControl — free text can never reach Approved.
// Execution stays in S18: an Approved card reports ExecutionPending true and
// performs no write. The lineage lock is shared with the Draft-minting path
// (mintDraftCard), so a concurrent version mint and a confirm serialize and a
// stale approval can never win the race.
//
// AUD-001 (issue #103): a genuine Approved outcome appends EXACTLY ONE immutable
// confirmation audit record — the authenticated actor, surface, APR-001 bindings,
// and a card snapshot — on THIS transaction, so the audit commits ATOMICALLY with
// the Approved state. If the audit append fails the whole confirmation rolls back
// (fail closed): a state-changing approval can never exist without its immutable
// evidence. The actor is supplied by the caller from authenticated context ONLY
// (never a request body); it is identity, never free-text authority. A non-approval
// outcome (Superseded→Invalidated, a changed binding→Invalidated, a lapse→Expired)
// writes NO confirmation event — its required non-approval evidence is the
// append-only approval_card_states transition AdvanceTx already records; it must not
// masquerade as an activation.
func (s *Service) ConfirmIndividual(ctx context.Context, cardID uuid.UUID, presented approval.Binding, now time.Time, actor audit.Actor) (ConfirmOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConfirmOutcome{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	requested, err := q.GetApprovalCard(ctx, cardID)
	if err != nil {
		return ConfirmOutcome{}, err // pgx.ErrNoRows for an unknown card.
	}

	// Serialize every writer on this lineage (mint vs confirm) before reading the
	// authoritative current version, so the decision below cannot race a mint.
	if err := q.LockApprovalLineage(ctx, requested.LineageID); err != nil {
		return ConfirmOutcome{}, err
	}

	card, err := cardFromDB(requested)
	if err != nil {
		return ConfirmOutcome{}, err
	}
	// Free-text / no-control containment: only a live AwaitingConfirmation card
	// bears a structured control. Any other state (incl. an already-approved card
	// on idempotent replay) fails closed here.
	if _, err := card.Control(); err != nil {
		return ConfirmOutcome{}, err // approval.ErrNoControl.
	}

	current, err := q.GetCurrentApprovalCard(ctx, requested.LineageID)
	if err != nil {
		return ConfirmOutcome{}, err
	}

	// Authoritative-lineage gate (APR-001): the requested card must BE the current
	// version. A superseded card is invalidated with no execution intent, whatever
	// its echoed binding says. The reason is the exact dimension the newer version
	// changed relative to this stale control — resolved against the authoritative
	// current binding, never a client echo. In P0 the only in-lineage mint
	// (EditPrice, CHAT-044) always bumps the parameter version, so this reports
	// parameter_version_changed; the ReasonNone fallback keeps the path fail-closed
	// even if a future mint left every bound dimension unchanged.
	if requested.ID != current.ID || requested.Version != current.Version {
		currentCard, err := cardFromDB(current)
		if err != nil {
			return ConfirmOutcome{}, err
		}
		reason := card.Binding.ValidateAgainst(currentCard.Binding, now)
		if reason == approval.ReasonNone {
			reason = approval.ReasonParameterChanged
		}
		advanced, err := s.AdvanceTx(ctx, q, requested.ID, approval.StateAwaitingConfirmation, approval.StateInvalidated, confirmReason(reason))
		if err != nil {
			return ConfirmOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ConfirmOutcome{}, err
		}
		return ConfirmOutcome{
			Card:             advanced,
			State:            approval.StateInvalidated,
			Reason:           reason,
			ExecutionPending: false,
		}, nil
	}

	// The requested card is the authoritative current version: re-verify the
	// presented binding against it and persist the §8.4 outcome.
	res, err := card.Confirm(presented, now)
	if err != nil {
		return ConfirmOutcome{}, err // approval.ErrNoControl for a non-control-bearing card.
	}
	advanced, err := s.AdvanceTx(ctx, q, requested.ID, approval.StateAwaitingConfirmation, res.Card.State, confirmReason(res.Reason))
	if err != nil {
		return ConfirmOutcome{}, err
	}
	// AUD-001 confirmation audit (issue #103): ONLY a genuinely Approved card writes
	// a confirmation event, and it writes on THIS transaction (q over tx) so the
	// immutable audit commits ATOMICALLY with the Approved state. An append failure
	// rolls the whole confirmation back (fail closed): the card stays
	// AwaitingConfirmation and can be re-confirmed rather than being left Approved
	// with no evidence of who activated the control. The append precedes the durable
	// dispatch below, so a rollback from either seam discards BOTH the audit and the
	// state. A non-approval outcome never reaches here (it returned above), so it
	// never fabricates a confirmation event — its append-only state-history row is
	// its required non-approval evidence.
	if res.Card.State == approval.StateApproved {
		if _, err := s.auditAppend(ctx, q, audit.Event{
			ActionID:     advanced.ActionID,
			CardID:       advanced.ID,
			AccountID:    advanced.MarketplaceAccountID,
			Type:         audit.EventConfirmation,
			Actor:        actor,
			Binding:      card.Binding,
			CardSnapshot: confirmationSnapshot(advanced),
			Detail:       confirmationDetail(presented),
		}); err != nil {
			return ConfirmOutcome{}, err
		}
		if s.dispatcher != nil {
			if err := s.dispatcher.DispatchApprovedTx(ctx, tx, advanced); err != nil {
				return ConfirmOutcome{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ConfirmOutcome{}, err
	}
	return ConfirmOutcome{
		Card:             advanced,
		State:            res.Card.State,
		Reason:           res.Reason,
		ExecutionPending: res.Card.State == approval.StateApproved,
	}, nil
}

// confirmationSnapshot is the AUD-001 card snapshot recorded with a confirmation
// event: the exact card that was Approved (id/lineage version/state/action) and its
// money-typed price rendered as raw components (never a float). It is JSON-safe so
// the trail reproduces the approved card without the conversation.
func confirmationSnapshot(card db.ApprovalCard) map[string]any {
	return map[string]any{
		"card_id":           card.ID.String(),
		"recommendation_id": card.RecommendationID.String(),
		"version":           card.Version,
		"state":             card.State,
		"action_id":         card.ActionID.String(),
		"price": map[string]any{
			"mantissa": card.PriceMantissa,
			"currency": card.PriceCurrency,
			"exponent": card.PriceExponent,
		},
		"expires_at": card.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
}

// confirmationDetail is the AUD-001 confirmation-event detail: it records that a
// structured control was activated and the action the presented control was bound
// to. It carries identifiers only — never a chat message body or free text.
func confirmationDetail(presented approval.Binding) map[string]any {
	return map[string]any{
		"event":               "structured_control_activated",
		"presented_action_id": presented.ActionID.String(),
	}
}

// confirmReason renders the persisted history reason for a confirmation outcome.
func confirmReason(r approval.InvalidationReason) string {
	if r == approval.ReasonNone {
		return "structured control activated"
	}
	return string(r)
}

// cardFromDB reconstructs the pure domain card from a persisted row (state,
// binding, price). It is the read-side inverse of CreateCard.
func cardFromDB(row db.ApprovalCard) (approval.Card, error) {
	price, err := money.New(row.PriceMantissa, row.PriceCurrency, int8(row.PriceExponent))
	if err != nil {
		return approval.Card{}, err
	}
	ev, err := unmarshalEvidenceVersions(row.EvidenceVersions)
	if err != nil {
		return approval.Card{}, err
	}
	binding := approval.Binding{
		ActionID:           row.ActionID,
		ParameterVersion:   row.ParameterVersion,
		ContextVersion:     row.ContextVersion,
		PolicyVersion:      row.PolicyVersion,
		CostProfileVersion: row.CostProfileVersion,
		EvidenceVersions:   ev,
		Expiry:             row.ExpiresAt,
	}
	return approval.Card{
		ID:               row.ID,
		RecommendationID: row.RecommendationID,
		Version:          int64(row.Version),
		State:            approval.State(row.State),
		Binding:          binding,
		Price:            price,
	}, nil
}

// DecodeEvidenceVersions decodes a stored evidence-version JSON map (the
// transport uses it to render the bound versions of a card, APR-001).
func DecodeEvidenceVersions(b []byte) (map[uuid.UUID]int64, error) {
	return unmarshalEvidenceVersions(b)
}

// unmarshalEvidenceVersions decodes the bound evidence-version map from JSON.
func unmarshalEvidenceVersions(b []byte) (map[uuid.UUID]int64, error) {
	if len(b) == 0 {
		return nil, nil
	}
	raw := map[string]int64{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[uuid.UUID]int64, len(raw))
	for k, v := range raw {
		id, err := uuid.Parse(k)
		if err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, nil
}

// ReopenExpirer adapts the Service to the identity.EventSink seam so an S11
// mapping-reopen event expires the dependent recommendations here (plan §4.8: the
// event carries JSON-safe business data only).
type ReopenExpirer struct{ svc *Service }

// NewReopenExpirer wires the Service as an identity reopen consumer.
func NewReopenExpirer(svc *Service) *ReopenExpirer { return &ReopenExpirer{svc: svc} }

var _ identity.EventSink = (*ReopenExpirer)(nil)

// MappingReopened expires the dependent recommendations for the reopened
// mapping's variant. It fails closed on error (the durable invalidation event row
// remains the system of record, so nothing is lost).
func (r *ReopenExpirer) MappingReopened(ctx context.Context, ev identity.MappingReopenedEvent) error {
	_, err := r.svc.ExpireDependentForVariant(ctx, ev.VariantID, "identity_reopen:"+string(ev.Reason))
	return err
}

// marshalEvidenceVersions encodes the evidence-version map as JSON for the bound
// card column. A nil/empty map encodes as an empty object.
func marshalEvidenceVersions(m map[uuid.UUID]int64) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	out := make(map[string]int64, len(m))
	for id, v := range m {
		out[id.String()] = v
	}
	return json.Marshal(out)
}
