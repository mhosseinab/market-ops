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
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// ErrRejectedTransition is returned when a §8.4 advance is refused: either the
// move is undefined (approval.Advance rejects it) or the card already left the
// expected from-state (the FROM-guarded UPDATE matched no row). Either way, no
// state is mutated — the machine fails closed.
var ErrRejectedTransition = errors.New("recommendation: approval state transition rejected")

// Service persists recommendations and approval cards and drives the §8.4 state
// machine over the store. It keeps the pure domain (internal/approval,
// internal/recommendation domain types) free of DB concerns; all persistence and
// append-only discipline live here.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds a recommendation/approval Service bound to the pool.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

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
	evJSON, err := marshalEvidenceVersions(binding.EvidenceVersions)
	if err != nil {
		return db.ApprovalCard{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

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
	if err := tx.Commit(ctx); err != nil {
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

// ExpireDependentForVariant invalidates every LIVE control-bearing card for a
// variant (§16 "Reopen mapping; expire dependent recommendation"). It is the S11
// identity-reopen consumer: a reopened mapping means the recommendation's identity
// is no longer confirmed, so any card whose control could still authorize a write
// (AwaitingConfirmation, Revalidating — the states with a defined → Invalidated
// edge) is moved to Invalidated. It returns the number of cards invalidated.
func (s *Service) ExpireDependentForVariant(ctx context.Context, variant uuid.UUID, reason string) (int, error) {
	cards, err := db.New(s.pool).ListLiveCardsForVariant(ctx, variant)
	if err != nil {
		return 0, err
	}
	invalidated := 0
	for _, c := range cards {
		from := approval.State(c.State)
		if !approval.CanTransition(from, approval.StateInvalidated) {
			continue // draft/ready/approved carry no direct → Invalidated edge (§8.4).
		}
		if _, err := s.Advance(ctx, c.ID, from, approval.StateInvalidated, reason); err != nil {
			if errors.Is(err, ErrRejectedTransition) {
				continue // raced to another state; skip.
			}
			return invalidated, err
		}
		invalidated++
	}
	return invalidated, nil
}

// ConfirmOutcome is the result of activating an individual structured control.
type ConfirmOutcome struct {
	Card             db.ApprovalCard
	State            approval.State
	Reason           approval.InvalidationReason
	ExecutionPending bool
}

// ConfirmIndividual activates the structured control on a card (§8.4 /
// APR-001). It reconstructs the card's authoritative binding from the store,
// re-verifies the PRESENTED control's binding against it at instant now, and
// persists the resulting §8.4 state: Approved (match), Invalidated (a bound
// version changed), or Expired (lapsed). A card that is not control-bearing (not
// AwaitingConfirmation, or a simulation) is refused with approval.ErrNoControl —
// free text can never reach Approved. Execution stays in S18: an Approved card
// reports ExecutionPending true and performs no write.
func (s *Service) ConfirmIndividual(ctx context.Context, cardID uuid.UUID, presented approval.Binding, now time.Time) (ConfirmOutcome, error) {
	row, err := db.New(s.pool).GetApprovalCard(ctx, cardID)
	if err != nil {
		return ConfirmOutcome{}, err
	}
	card, err := cardFromDB(row)
	if err != nil {
		return ConfirmOutcome{}, err
	}
	res, err := card.Confirm(presented, now)
	if err != nil {
		return ConfirmOutcome{}, err // approval.ErrNoControl for a non-control-bearing card.
	}
	advanced, err := s.Advance(ctx, cardID, approval.StateAwaitingConfirmation, res.Card.State, confirmReason(res.Reason))
	if err != nil {
		return ConfirmOutcome{}, err
	}
	return ConfirmOutcome{
		Card:             advanced,
		State:            res.Card.State,
		Reason:           res.Reason,
		ExecutionPending: res.Card.State == approval.StateApproved,
	}, nil
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
