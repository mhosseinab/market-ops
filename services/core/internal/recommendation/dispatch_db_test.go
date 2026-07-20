package recommendation_test

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
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// countIntents returns the number of execute_approved River jobs whose durable args
// name this card (the intent record for issue #92). river_job.args is the outbox
// payload; card_id is its uniqueness key.
func countIntents(t *testing.T, pool *pgxpool.Pool, cardID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM river_job WHERE kind = 'execute_approved' AND args->>'card_id' = $1`,
		cardID.String()).Scan(&n); err != nil {
		t.Fatalf("count execute_approved intents: %v", err)
	}
	return n
}

// failingDispatcher forces the confirm transaction to fail at the dispatch seam, to
// prove atomicity: neither Approved nor an intent may commit if enqueue fails.
type failingDispatcher struct{}

var errDispatchBoom = errors.New("dispatch boom (test)")

func (failingDispatcher) DispatchApprovedTx(_ context.Context, _ pgx.Tx, _ db.ApprovalCard) error {
	return errDispatchBoom
}

// realDispatcherFor builds the production JobDispatcher over a real (River-migrated)
// insert-only client, so the durable intent row is exercised end-to-end at the
// producer without a worker consuming it.
func realDispatcherFor(t *testing.T, pool *pgxpool.Pool) *recommendation.JobDispatcher {
	t.Helper()
	if err := jobs.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	client, err := jobs.NewClient(pool, nil, nil) // insert-only; no worker runs
	if err != nil {
		t.Fatalf("river client: %v", err)
	}
	return recommendation.NewJobDispatcher(client)
}

// TestConfirmDispatch_ApprovedEnqueuesOneIntentAtomically is the happy producer
// path (issue #92): a genuine confirmation commits Approved AND, in the SAME
// transaction, enqueues exactly ONE durable execution intent. The acknowledged
// confirmation no longer depends on a second /actions/execute request.
func TestConfirmDispatch_ApprovedEnqueuesOneIntentAtomically(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	card := awaitingCard(t, svc, account, variant)
	presented := bindingOf(t, card)

	outcome, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if outcome.State != approval.StateApproved || !outcome.ExecutionPending {
		t.Fatalf("outcome = %s pending=%v; want approved+pending", outcome.State, outcome.ExecutionPending)
	}
	if got := countIntents(t, pool, card.ID); got != 1 {
		t.Fatalf("durable intents = %d; want exactly 1 (atomic with Approved)", got)
	}
}

// TestConfirmDispatch_DuplicateConfirmOneIntent proves idempotency (§4.6): a
// duplicate/retried confirmation of the same card produces AT MOST ONE durable
// intent. The second confirm finds no live control (card already Approved) and
// never re-enqueues.
func TestConfirmDispatch_DuplicateConfirmOneIntent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	card := awaitingCard(t, svc, account, variant)
	presented := bindingOf(t, card)

	if _, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor()); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	// Second confirm of the same control must fail closed (no live control).
	if _, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor()); err != approval.ErrNoControl {
		t.Fatalf("duplicate confirm err = %v; want ErrNoControl", err)
	}
	if got := countIntents(t, pool, card.ID); got != 1 {
		t.Fatalf("durable intents after duplicate confirm = %d; want exactly 1", got)
	}
}

// TestConfirmDispatch_TxFailureNoApprovedNoIntent proves atomicity: if the dispatch
// enqueue fails inside the confirm transaction, the whole thing rolls back — the
// card is NOT left Approved and NO intent exists. The confirmation can be retried;
// nothing is half-committed.
func TestConfirmDispatch_TxFailureNoApprovedNoIntent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	// River migrated so the intent table exists to be asserted empty.
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	svc := recommendation.NewService(pool).SetExecutionDispatcher(failingDispatcher{})

	card := awaitingCard(t, svc, account, variant)
	presented := bindingOf(t, card)

	_, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor())
	if !errors.Is(err, errDispatchBoom) {
		t.Fatalf("confirm err = %v; want dispatch failure to propagate (rollback)", err)
	}
	reloaded, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.State != string(approval.StateAwaitingConfirmation) {
		t.Fatalf("card state after failed dispatch = %s; want still awaiting_confirmation (Approved rolled back)", reloaded.State)
	}
	if got := countIntents(t, pool, card.ID); got != 0 {
		t.Fatalf("durable intents after failed tx = %d; want 0 (nothing half-committed)", got)
	}
}

// TestConfirmDispatch_SupersededNoIntent proves APR-001 at the dispatch seam: a
// superseded control is invalidated with NO execution intent — a stale approval can
// never enqueue durable processing (mirrors the ExecutionPending=false contract).
func TestConfirmDispatch_SupersededNoIntent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool)).SetEditPriceRechecker(authoritativeRechecker{})

	v1 := awaitingCard(t, svc, account, variant)
	presentedV1 := bindingOf(t, v1)

	// Edit to the account's authoritative proposal (feasHigh 1050) so the #134
	// policy re-check admits it and V2 is minted.
	newPrice, err := money.New(1050, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	if _, err := svc.EditPrice(ctx, v1.ID, newPrice, time.Now().UTC()); err != nil {
		t.Fatalf("EditPrice (mint V2): %v", err)
	}

	outcome, err := svc.ConfirmIndividual(ctx, v1.ID, presentedV1, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm superseded: %v", err)
	}
	if outcome.State != approval.StateInvalidated || outcome.ExecutionPending {
		t.Fatalf("superseded outcome = %s pending=%v; want invalidated + no pending", outcome.State, outcome.ExecutionPending)
	}
	if got := countIntents(t, pool, v1.ID); got != 0 {
		t.Fatalf("superseded control enqueued %d intents; want 0 (APR-001)", got)
	}
}
