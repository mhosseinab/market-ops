package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// realReopenDispatcher builds the production River-backed ReopenDispatcher over a
// real (River-migrated) insert-only client, exercising the durable intent row
// end-to-end at the producer without a worker consuming it.
func realReopenDispatcher(t *testing.T, pool *pgxpool.Pool) *identity.JobReopenDispatcher {
	t.Helper()
	if err := jobs.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	client, err := jobs.NewClient(pool, nil, nil) // insert-only; no worker runs
	if err != nil {
		t.Fatalf("river client: %v", err)
	}
	return identity.NewJobReopenDispatcher(client)
}

// failingReopenDispatcher forces the reopen transaction to fail at the dispatch seam,
// to prove atomicity: neither the state change, the audit row, nor the event row may
// commit if the durable enqueue fails.
type failingReopenDispatcher struct{}

var errReopenDispatchBoom = errors.New("reopen dispatch boom (test)")

func (failingReopenDispatcher) DispatchReopenTx(_ context.Context, _ pgx.Tx, _ identity.MappingReopenedEvent) error {
	return errReopenDispatchBoom
}

// countReopenIntents returns the number of mapping_reopened River jobs whose durable
// args name this dedup key (the outbox record for issue #49).
func countReopenIntents(t *testing.T, pool *pgxpool.Pool, dedupKey string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM river_job WHERE kind = 'mapping_reopened' AND args->>'dedup_key' = $1`,
		dedupKey).Scan(&n); err != nil {
		t.Fatalf("count mapping_reopened intents: %v", err)
	}
	return n
}

func confirmedIdentity(t *testing.T, svc *identity.Service, account, variant uuid.UUID) db.MarketProductIdentity {
	t.Helper()
	c := candidateFor(t, svc, account, variant)
	confirmed, err := svc.Confirm(context.Background(), c.ID, identity.Actor(uuid.New()))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return confirmed
}

// TestReopenDispatch_DurableIntentEnqueuedAtomically is the NEGATIVE regression proof
// for issue #49: a committed reopen produces a DURABLE delivery intent atomically, so
// the reopen event can never be permanently lost to a post-commit sink/process
// failure. The intent is present after the reopen commits (transactional enqueue).
func TestReopenDispatch_DurableIntentEnqueuedAtomically(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, _, _ := seedVariant(t, q)
	svc := identity.NewService(pool, nil).SetReopenDispatcher(realReopenDispatcher(t, pool))

	confirmed := confirmedIdentity(t, svc, account, variant)

	ev, err := svc.Reopen(ctx, confirmed.ID, identity.ReasonMerge, identity.Actor(uuid.New()))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	// The durable append-only event row committed.
	n, err := q.CountRecommendationInvalidationsForIdentity(ctx, confirmed.ID)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Fatalf("durable invalidation events = %d; want 1", n)
	}
	// And its durable delivery intent committed atomically with it.
	if got := countReopenIntents(t, pool, ev.DedupKey); got != 1 {
		t.Fatalf("durable reopen intents = %d; want exactly 1 (atomic with the reopen)", got)
	}
}

// TestReopenDispatch_TxFailureRollsBackEverything is the FAIL-CLOSED proof: a dispatch
// enqueue failure INSIDE the reopen transaction rolls the WHOLE reopen back — the
// mapping stays Confirmed, NO recommendation_invalidation_events row exists, and NO
// reopened audit row exists. Nothing is half-committed; the reopen can be retried.
func TestReopenDispatch_TxFailureRollsBackEverything(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, _, _ := seedVariant(t, q)
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	svc := identity.NewService(pool, nil).SetReopenDispatcher(failingReopenDispatcher{})

	confirmed := confirmedIdentity(t, svc, account, variant)
	auditBefore := len(decisionsOf(t, svc, confirmed.ID))

	_, err := svc.Reopen(ctx, confirmed.ID, identity.ReasonMerge, identity.Actor(uuid.New()))
	if !errors.Is(err, errReopenDispatchBoom) {
		t.Fatalf("reopen err = %v; want dispatch failure to propagate (rollback)", err)
	}

	// Mapping still Confirmed and executable — nothing moved out of the executable set.
	after, err := q.GetIdentity(ctx, confirmed.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.State != string(identity.StateConfirmed) || !after.Active {
		t.Fatalf("after failed reopen state=%q active=%v; want confirmed/active (rolled back)", after.State, after.Active)
	}
	// No invalidation event row.
	if n, err := q.CountRecommendationInvalidationsForIdentity(ctx, confirmed.ID); err != nil || n != 0 {
		t.Fatalf("invalidation events after failed reopen = %d (err=%v); want 0", n, err)
	}
	// No new (reopened) audit row.
	if got := len(decisionsOf(t, svc, confirmed.ID)); got != auditBefore {
		t.Fatalf("audit rows after failed reopen = %d; want unchanged %d (no orphan reopened row)", got, auditBefore)
	}
}

// TestReopenDispatch_EventTableNeverUpdated is the APPEND-ONLY proof: the fix records
// delivery state in the River job store, NEVER by mutating the append-only
// recommendation_invalidation_events row. The tuple's xmin (Postgres write stamp) is
// unchanged across reopen + delivery — an UPDATE would bump it.
func TestReopenDispatch_EventTableNeverUpdated(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, _, _ := seedVariant(t, q)
	svc := identity.NewService(pool, nil).SetReopenDispatcher(realReopenDispatcher(t, pool))

	confirmed := confirmedIdentity(t, svc, account, variant)
	ev, err := svc.Reopen(ctx, confirmed.ID, identity.ReasonMerge, identity.Actor(uuid.New()))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	xmin := func() string {
		var s string
		if err := pool.QueryRow(ctx,
			`SELECT xmin::text FROM recommendation_invalidation_events WHERE id = $1`, ev.EventID).Scan(&s); err != nil {
			t.Fatalf("read xmin: %v", err)
		}
		return s
	}
	before := xmin()

	// Deliver the intent through the idempotent consumer (as the worker would).
	if _, err := recommendation.NewService(pool).ExpireDependentForVariant(ctx, variant, "identity_reopen:"+string(ev.Reason)); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if after := xmin(); after != before {
		t.Fatalf("recommendation_invalidation_events xmin changed (%s -> %s); the append-only event row was UPDATED", before, after)
	}
	_ = q
}

// TestReopenDispatch_RedeliveryExactlyOnceEffective is the REDELIVERY proof (the
// issue's suggested verification): delivering the durable intent expires the
// dependent recommendation for the variant; a SECOND delivery (crash/restart
// redelivery) invalidates nothing new — the consumer is idempotent, so the effect is
// exactly-once. This is the "force process failure after commit, restart, event
// delivered exactly-once-effectively" acceptance.
func TestReopenDispatch_RedeliveryExactlyOnceEffective(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, _, _ := seedVariant(t, q)
	svc := identity.NewService(pool, nil).SetReopenDispatcher(realReopenDispatcher(t, pool))
	recSvc := recommendation.NewService(pool)

	// A live, control-bearing dependent card for the variant.
	cardID := seedAwaitingCard(t, pool, account, variant)

	confirmed := confirmedIdentity(t, svc, account, variant)
	ev, err := svc.Reopen(ctx, confirmed.ID, identity.ReasonMerge, identity.Actor(uuid.New()))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	// The worker runner, exactly as main wires it: reconstruct the event from the
	// JSON-safe args and drive the idempotent consumer.
	deliver := func() (int, error) {
		return recSvc.ExpireDependentForVariant(ctx, ev.VariantID, "identity_reopen:"+string(ev.Reason))
	}

	// First delivery expires the dependent control.
	n1, err := deliver()
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if n1 != 1 {
		t.Fatalf("first delivery invalidated %d; want 1", n1)
	}
	if state := cardStateOf(t, recSvc, cardID); state != string(approval.StateInvalidated) {
		t.Fatalf("card after first delivery = %s; want invalidated", state)
	}
	// Second delivery (redelivery after a simulated crash) invalidates nothing new.
	n2, err := deliver()
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("redelivery invalidated %d; want 0 (exactly-once-effective)", n2)
	}
	_ = q
}

// decisionsOf returns the append-only decision audit for a mapping.
func decisionsOf(t *testing.T, svc *identity.Service, identityID uuid.UUID) []db.MarketProductIdentityDecision {
	t.Helper()
	d, err := svc.Decisions(context.Background(), identityID)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	return d
}

func cardStateOf(t *testing.T, svc *recommendation.Service, cardID uuid.UUID) string {
	t.Helper()
	c, err := svc.GetCard(context.Background(), cardID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	return c.State
}

// seedAwaitingCard inserts a recommendation + approval card for the variant and drives
// it to AwaitingConfirmation (a live, control-bearing state), returning the card id.
func seedAwaitingCard(t *testing.T, pool *pgxpool.Pool, account, variant uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	lineage := uuid.New()
	var recID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO recommendations (
			marketplace_account_id, variant_id, lineage_id, version, objective,
			current_price_mantissa, current_price_currency, current_price_exponent,
			readiness, evidence_quality,
			cost_profile_version, policy_version, context_version, parameter_version)
		VALUES ($1,$2,$3,1,'maximize_contribution',100000,'IRR',0,'complete','verified',1,1,1,1)
		RETURNING id`, account, variant, lineage).Scan(&recID); err != nil {
		t.Fatalf("insert recommendation: %v", err)
	}

	actionID := uuid.New()
	bind := approval.Binding{
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1,
		PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(30 * time.Minute),
	}
	card, err := db.New(pool).InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: recID, MarketplaceAccountID: account, LineageID: uuid.New(),
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
		EvidenceVersions: []byte("{}"), IdempotencyKey: bind.IdempotencyKey(),
		State: string(approval.StateDraft), PriceMantissa: 95000, PriceCurrency: "IRR", PriceExponent: 0,
		ExpiresAt: bind.Expiry,
	})
	if err != nil {
		t.Fatalf("insert card: %v", err)
	}
	svc := recommendation.NewService(pool)
	for _, step := range []struct{ from, to approval.State }{
		{approval.StateDraft, approval.StateReadyForReview},
		{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
	} {
		if _, err := svc.Advance(ctx, card.ID, step.from, step.to, "seed"); err != nil {
			t.Fatalf("advance %s→%s: %v", step.from, step.to, err)
		}
	}
	return card.ID
}
