package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// uniqueViolation is the Postgres SQLSTATE for a unique_violation. On the
// append-only event table it means the event was already emitted (dedup), which
// the producer treats as success — never a double-expire.
const uniqueViolation = "23505"

// GenerateCandidates creates rule-based EXACT-native-id candidates for every
// variant of the account that has no pending or Confirmed mapping. Each new
// candidate is NeedsReview (never executable until a human confirms) and is
// recorded with a 'candidate_created' audit row. It is idempotent: a re-run over
// already-candidated variants creates nothing. Returns the created candidates.
//
// The rule is exact identity: the candidate maps the variant to the SAME native
// product id the catalog observed for it. Fuzzy/automated suggestion is P0.5 and
// intentionally absent.
func (s *Service) GenerateCandidates(ctx context.Context, account uuid.UUID) ([]db.MarketProductIdentity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: begin candidate tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	targets, err := q.ListVariantsWithoutActiveIdentity(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("identity: list candidate variants: %w", err)
	}

	created := make([]db.MarketProductIdentity, 0, len(targets))
	for _, v := range targets {
		mapping, err := q.CreateIdentityCandidate(ctx, db.CreateIdentityCandidateParams{
			MarketplaceAccountID: account,
			VariantID:            v.ID,
			NativeVariantID:      v.NativeVariantID,
			NativeProductID:      v.NativeProductID,
		})
		if err != nil {
			return nil, fmt.Errorf("identity: create candidate for variant %s: %w", v.ID, err)
		}
		evidence := candidateEvidence(v)
		if _, err := q.InsertIdentityDecision(ctx, db.InsertIdentityDecisionParams{
			IdentityID:           mapping.ID,
			MarketplaceAccountID: account,
			VariantID:            v.ID,
			Decision:             "candidate_created",
			FromState:            "",
			ToState:              string(StateNeedsReview),
			Reason:               "exact_native_id",
			Evidence:             evidence,
			DecidedBy:            decidedBy(systemActor()),
		}); err != nil {
			return nil, fmt.Errorf("identity: audit candidate for variant %s: %w", v.ID, err)
		}
		created = append(created, mapping)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("identity: commit candidate tx: %w", err)
	}
	return created, nil
}

// Confirm transitions a NeedsReview candidate to Confirmed (journey 4 step 2).
// The partial unique index enforces at most one active Confirmed per variant, so
// a second confirm for the same variant fails at commit — surfaced as a unique
// violation. Only after Confirm does the variant become an observation target
// (OBS-001, wired downstream).
func (s *Service) Confirm(ctx context.Context, identityID uuid.UUID, actor Actor) (db.MarketProductIdentity, error) {
	return s.decide(ctx, identityID, actor, "confirmed", func(q *db.Queries) (db.MarketProductIdentity, error) {
		return q.ConfirmIdentity(ctx, identityID)
	})
}

// Reject transitions a NeedsReview candidate to Rejected (journey 4 step 2). A
// rejected mapping never feeds an executable path.
func (s *Service) Reject(ctx context.Context, identityID uuid.UUID, actor Actor, note string) (db.MarketProductIdentity, error) {
	return s.decideWithReason(ctx, identityID, actor, "rejected", note, func(q *db.Queries) (db.MarketProductIdentity, error) {
		return q.RejectIdentity(ctx, identityID)
	})
}

// Defer leaves a candidate in NeedsReview (journey 4 step 2) and records the
// deferral in the append-only audit. It never promotes the mapping.
func (s *Service) Defer(ctx context.Context, identityID uuid.UUID, actor Actor, note string) (db.MarketProductIdentity, error) {
	return s.decideWithReason(ctx, identityID, actor, "deferred", note, func(q *db.Queries) (db.MarketProductIdentity, error) {
		return q.DeferIdentity(ctx, identityID)
	})
}

// decide runs a guarded state transition plus its append-only audit row in one
// transaction. transition returns pgx.ErrNoRows when the mapping was not in
// NeedsReview, which decide maps to ErrNotPending.
func (s *Service) decide(
	ctx context.Context, identityID uuid.UUID, actor Actor, decision string,
	transition func(*db.Queries) (db.MarketProductIdentity, error),
) (db.MarketProductIdentity, error) {
	return s.decideWithReason(ctx, identityID, actor, decision, "", transition)
}

func (s *Service) decideWithReason(
	ctx context.Context, identityID uuid.UUID, actor Actor, decision, reason string,
	transition func(*db.Queries) (db.MarketProductIdentity, error),
) (db.MarketProductIdentity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.MarketProductIdentity{}, fmt.Errorf("identity: begin %s tx: %w", decision, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	before, err := q.GetIdentity(ctx, identityID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.MarketProductIdentity{}, ErrNotFound
		}
		return db.MarketProductIdentity{}, fmt.Errorf("identity: load mapping: %w", err)
	}

	after, err := transition(q)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.MarketProductIdentity{}, ErrNotPending
		}
		return db.MarketProductIdentity{}, fmt.Errorf("identity: %s mapping: %w", decision, err)
	}

	if _, err := q.InsertIdentityDecision(ctx, db.InsertIdentityDecisionParams{
		IdentityID:           after.ID,
		MarketplaceAccountID: after.MarketplaceAccountID,
		VariantID:            after.VariantID,
		Decision:             decision,
		FromState:            before.State,
		ToState:              after.State,
		Reason:               reason,
		Evidence:             transitionEvidence(after, reason),
		DecidedBy:            decidedBy(actor),
	}); err != nil {
		return db.MarketProductIdentity{}, fmt.Errorf("identity: audit %s: %w", decision, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return db.MarketProductIdentity{}, fmt.Errorf("identity: commit %s tx: %w", decision, err)
	}
	return after, nil
}

// Reopen reopens an active Confirmed mapping on a merge/split/redirect/
// variant-conflict signal (§16). It moves the mapping out of the executable set
// (Obsolete for a redirect, NeedsReview otherwise), records the append-only
// audit, and emits the append-only recommendation-invalidation event that
// downstream packages subscribe to (consumed in S17 to expire dependent
// recommendations). The durable delivery intent is enqueued INSIDE this same
// transaction (issue #49) so it commits atomically with the state change and the
// event row; a committed reopen therefore always carries its durable delivery and can
// never be permanently lost. Returns the emitted event.
func (s *Service) Reopen(ctx context.Context, identityID uuid.UUID, reason ReopenReason, actor Actor) (MappingReopenedEvent, error) {
	if !reason.Valid() {
		return MappingReopenedEvent{}, ErrInvalidReason
	}
	target := StateNeedsReview
	if reason == ReasonRedirect {
		// A redirect means the mapped product record is gone; the mapping cannot
		// be re-confirmed as-is, so it becomes Obsolete rather than re-queued.
		target = StateObsolete
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MappingReopenedEvent{}, fmt.Errorf("identity: begin reopen tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	before, err := q.GetIdentity(ctx, identityID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MappingReopenedEvent{}, ErrNotFound
		}
		return MappingReopenedEvent{}, fmt.Errorf("identity: load mapping: %w", err)
	}

	after, err := q.ReopenConfirmedIdentity(ctx, db.ReopenConfirmedIdentityParams{
		ID:    identityID,
		State: string(target),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MappingReopenedEvent{}, ErrNotReopenable
		}
		return MappingReopenedEvent{}, fmt.Errorf("identity: reopen mapping: %w", err)
	}

	if _, err := q.InsertIdentityDecision(ctx, db.InsertIdentityDecisionParams{
		IdentityID:           after.ID,
		MarketplaceAccountID: after.MarketplaceAccountID,
		VariantID:            after.VariantID,
		Decision:             "reopened",
		FromState:            before.State,
		ToState:              after.State,
		Reason:               string(reason),
		Evidence:             transitionEvidence(after, string(reason)),
		DecidedBy:            decidedBy(actor),
	}); err != nil {
		return MappingReopenedEvent{}, fmt.Errorf("identity: audit reopen: %w", err)
	}

	dedupKey := fmt.Sprintf("%s:%s:%d", after.ID, reason, after.Version)
	event, err := q.InsertRecommendationInvalidation(ctx, db.InsertRecommendationInvalidationParams{
		MarketplaceAccountID: after.MarketplaceAccountID,
		VariantID:            after.VariantID,
		IdentityID:           after.ID,
		Reason:               string(reason),
		DedupKey:             dedupKey,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			// Already emitted for this (identity, reason, version): dedup, no
			// double-expire. Fetch the existing event so the caller still gets it.
			existing, ferr := fetchEventByDedup(ctx, q, after.MarketplaceAccountID, dedupKey)
			if ferr != nil {
				return MappingReopenedEvent{}, fmt.Errorf("identity: fetch deduped event: %w", ferr)
			}
			event = existing
		} else {
			return MappingReopenedEvent{}, fmt.Errorf("identity: emit invalidation: %w", err)
		}
	}

	domainEvent := MappingReopenedEvent{
		EventID:    event.ID,
		AccountID:  event.MarketplaceAccountID,
		VariantID:  event.VariantID,
		IdentityID: event.IdentityID,
		Reason:     ReopenReason(event.Reason),
		DedupKey:   event.DedupKey,
		EmittedAt:  event.EmittedAt,
		NewState:   State(after.State),
	}

	// Durable reopen dispatch (issue #49): enqueue the delivery intent on THIS
	// transaction, so it commits ATOMICALLY with the state change, the append-only
	// audit row, and the append-only event row. A dispatch-enqueue failure rolls the
	// WHOLE reopen back (fail closed): the mapping stays Confirmed, no orphan event or
	// audit row is left, and the reopen can be retried. Delivery is durable in the
	// River job store — it no longer depends on an in-process post-commit callback
	// that could be lost to a sink error or a crash after commit. The intent is unique
	// by dedup key, so a deduped re-emit collapses to one durable record.
	if s.dispatcher != nil {
		if err := s.dispatcher.DispatchReopenTx(ctx, tx, domainEvent); err != nil {
			return MappingReopenedEvent{}, fmt.Errorf("identity: dispatch reopen intent: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return MappingReopenedEvent{}, fmt.Errorf("identity: commit reopen tx: %w", err)
	}

	// When a durable dispatcher is wired (production), the committed River intent IS
	// the delivery guarantee and the worker drives the idempotent consumer exactly-
	// once-effectively; the in-process sink is intentionally NOT used (no double
	// processing, and success never depends on it). Without a dispatcher (legacy/
	// tests), the in-process sink is the delivery path and its error is surfaced —
	// but it is called AFTER commit, so a subscriber never sees a rolled-back reopen.
	if s.dispatcher == nil {
		if err := s.sink.MappingReopened(ctx, domainEvent); err != nil {
			return domainEvent, fmt.Errorf("identity: notify reopen subscriber: %w", err)
		}
	}
	return domainEvent, nil
}

// fetchEventByDedup returns an already-emitted event by its dedup key.
func fetchEventByDedup(ctx context.Context, q *db.Queries, account uuid.UUID, dedupKey string) (db.RecommendationInvalidationEvent, error) {
	events, err := q.ListRecommendationInvalidations(ctx, account)
	if err != nil {
		return db.RecommendationInvalidationEvent{}, err
	}
	for _, e := range events {
		if e.DedupKey == dedupKey {
			return e, nil
		}
	}
	return db.RecommendationInvalidationEvent{}, pgx.ErrNoRows
}

func candidateEvidence(v db.ListVariantsWithoutActiveIdentityRow) []byte {
	b, _ := json.Marshal(map[string]any{
		"candidate_source":  "exact_native_id",
		"native_variant_id": v.NativeVariantID,
		"native_product_id": v.NativeProductID,
		"supplier_code":     v.SupplierCode,
		"variant_title":     v.Title,
	})
	return b
}

func transitionEvidence(m db.MarketProductIdentity, note string) []byte {
	b, _ := json.Marshal(map[string]any{
		"native_variant_id": m.NativeVariantID,
		"native_product_id": m.NativeProductID,
		"version":           m.Version,
		"note":              note,
	})
	return b
}

func decidedBy(a Actor) pgtype.UUID {
	id := uuid.UUID(a)
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
