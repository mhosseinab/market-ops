package execution

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// advanceCardTo drives the card through the legal §8.4 write-path prelude up to
// `to` (one of Revalidating, Executing) to SIMULATE a process crash that left the
// card mid-flight — the exact strandings issue #105 must recover from.
func advanceCardTo(t *testing.T, rec *recommendation.Service, ctx context.Context, cardID uuid.UUID, to approval.State) {
	t.Helper()
	steps := []struct{ from, to approval.State }{
		{approval.StateApproved, approval.StateRevalidating},
		{approval.StateRevalidating, approval.StateExecuting},
	}
	for _, s := range steps {
		if _, err := rec.Advance(ctx, cardID, s.from, s.to, "simulate crash mid-write"); err != nil {
			t.Fatalf("advance %s→%s: %v", s.from, s.to, err)
		}
		if s.to == to {
			return
		}
	}
}

// TestExecute_ResumeFromExecuting_NoDuplicateWrite proves EXE-002 restart-safety:
// a card a crash left in Executing WITH NO execution record yet (crash between the
// Revalidating→Executing commit and the idempotency claim) resumes on the next
// Execute — it claims, writes EXACTLY ONCE, and converges to a terminal state.
// Before the fix, Execute rejected the non-Approved card (ErrNotApproved) and the
// card was stranded forever.
func TestExecute_ResumeFromExecuting_NoDuplicateWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	rec := recommendation.NewService(pool)
	advanceCardTo(t, rec, ctx, card.ID, approval.StateExecuting)

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, rec, writer, fakeResolver{ctx: enabledContext(card, native)})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("resume execute: %v", err)
	}
	if !res.DidWrite || res.ExternalState != StateAccepted {
		t.Fatalf("resume: didWrite=%v state=%q; want write+accepted", res.DidWrite, res.ExternalState)
	}
	if got := atomic.LoadInt32(writes); got != 1 {
		t.Fatalf("external writes = %d; want exactly 1 (resume must not duplicate)", got)
	}
	after, _ := q.GetApprovalCard(ctx, card.ID)
	if after.State != string(approval.StateAccepted) {
		t.Fatalf("card state = %q; want accepted after resume", after.State)
	}
}

// TestExecute_ResumeFromRevalidating re-validates and completes a card a crash left
// in Revalidating (crash after Approved→Revalidating, before the gate/Executing
// step). The resume re-runs the EXE-001 gate, advances to Executing, and writes
// exactly once.
func TestExecute_ResumeFromRevalidating(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	rec := recommendation.NewService(pool)
	advanceCardTo(t, rec, ctx, card.ID, approval.StateRevalidating)

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, rec, writer, fakeResolver{ctx: enabledContext(card, native)})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("resume execute: %v", err)
	}
	if !res.DidWrite || res.ExternalState != StateAccepted {
		t.Fatalf("resume: didWrite=%v state=%q; want write+accepted", res.DidWrite, res.ExternalState)
	}
	if got := atomic.LoadInt32(writes); got != 1 {
		t.Fatalf("external writes = %d; want exactly 1", got)
	}
}

// TestExecute_SelfHealStrandedExecuting proves the "card remains Executing while
// external state is ambiguous" bug is closed: a claim committed (execution record =
// pending_reconciliation) but the result-commit transaction never ran, leaving the
// card in Executing. The next Execute self-heals the card to PendingReconciliation
// (visible to reconciliation) WITHOUT any new external write — the true result
// stays unknown and is NEVER inferred (EXE-003).
func TestExecute_SelfHealStrandedExecuting(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	rec := recommendation.NewService(pool)
	advanceCardTo(t, rec, ctx, card.ID, approval.StateExecuting)

	// Simulate a durable claim that committed before the result-commit crash.
	if _, err := q.ClaimActionExecution(ctx, db.ClaimActionExecutionParams{
		CardID:         card.ID,
		ActionID:       card.ActionID,
		IdempotencyKey: card.IdempotencyKey,
		Mode:           "write",
		ExternalState:  string(StatePendingReconciliation),
		RequestPayload: []byte("{}"),
	}); err != nil {
		t.Fatalf("seed pending claim: %v", err)
	}

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, rec, writer, fakeResolver{ctx: enabledContext(card, native)})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("self-heal execute: %v", err)
	}
	if res.DidWrite {
		t.Fatalf("self-heal performed a duplicate external write")
	}
	if res.ExternalState != StatePendingReconciliation {
		t.Fatalf("self-heal: state=%q; want pending_reconciliation (never inferred)", res.ExternalState)
	}
	if got := atomic.LoadInt32(writes); got != 0 {
		t.Fatalf("external writes = %d; want 0 (replay must not write)", got)
	}
	after, _ := q.GetApprovalCard(ctx, card.ID)
	if after.State != string(approval.StatePendingReconciliation) {
		t.Fatalf("card state = %q; want pending_reconciliation after self-heal", after.State)
	}
}
