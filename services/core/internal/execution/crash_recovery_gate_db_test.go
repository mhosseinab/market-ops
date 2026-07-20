package execution

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// TestExecute_ResumeFromExecuting_GateFails_NoWrite is the issue #105 never-cut
// regression: a card a crash left in Executing WITH NO execution record must
// RE-VALIDATE the EXE-001 gate before the fresh external write. When the gate now
// fails (here: permission revoked), the resume performs ZERO external writes,
// creates NO execution record, and fails closed to the legal §8.4
// Executing→PendingReconciliation edge — never a live marketplace write on a
// control whose versioning changed across the retry (§4.6).
func TestExecute_ResumeFromExecuting_GateFails_NoWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	rec := recommendation.NewService(pool)
	advanceCardTo(t, rec, ctx, card.ID, approval.StateExecuting)

	// Write ENABLED but the EXE-001 gate FAILS: permission was revoked after approval.
	rc := enabledContext(card, native)
	rc.Inputs.PermissionGranted = false

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, rec, writer, fakeResolver{ctx: rc})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("resume execute (gate fail): %v", err)
	}
	if res.DidWrite {
		t.Fatalf("resume with a failing gate performed an external write (never-cut violation)")
	}
	if !res.Blocked || res.FailedGate != GatePermission {
		t.Fatalf("resume: blocked=%v failedGate=%q; want blocked on permission", res.Blocked, res.FailedGate)
	}
	if res.ExternalState != StatePendingReconciliation {
		t.Fatalf("resume: externalState=%q; want pending_reconciliation (fail closed)", res.ExternalState)
	}
	if got := atomic.LoadInt32(writes); got != 0 {
		t.Fatalf("external writes = %d; want 0 (a blocked resume must never write)", got)
	}
	// A gate-blocked, NO-WRITE execution marker MUST exist so the parked card is
	// DRAINABLE and VISIBLE (issue #105 fix cycle 1). Its absence was the safety hole:
	// a PendingReconciliation card with no action_executions row is an
	// operations-invisible zombie no reconciliation drain path can reach.
	exec, err := q.GetActionExecutionByAction(ctx, card.ActionID)
	if err != nil {
		t.Fatalf("GetActionExecutionByAction err = %v; want a gate-blocked marker row", err)
	}
	if !exec.GateBlocked {
		t.Fatalf("execution marker gate_blocked = false; want true (no-write recovery marker)")
	}
	if exec.ExternalState != string(StatePendingReconciliation) {
		t.Fatalf("execution marker external_state = %q; want pending_reconciliation", exec.ExternalState)
	}
	// The marker surfaces in the OPS-002 operations queue / backlog gauge enumeration.
	pending, err := svc.ListPendingReconciliation(ctx, card.MarketplaceAccountID, 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	var found bool
	for _, p := range pending {
		if p.ActionID == card.ActionID {
			found = true
		}
	}
	if !found {
		t.Fatalf("gate-blocked park not visible in ListPendingReconciliation (operations-invisible zombie)")
	}
	after, _ := q.GetApprovalCard(ctx, card.ID)
	if after.State != string(approval.StatePendingReconciliation) {
		t.Fatalf("card state = %q; want pending_reconciliation after a blocked resume", after.State)
	}
}

// TestExecute_ResumeFromRevalidating_GateFails_NoWrite is the resume-from-Revalidating
// negative: a card a crash left in Revalidating whose gate now fails invalidates
// (Revalidating→Invalidated) and writes nothing — the pre-existing behaviour, kept
// under test so the resume paths are proven symmetric (both fail closed, zero writes).
func TestExecute_ResumeFromRevalidating_GateFails_NoWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	rec := recommendation.NewService(pool)
	advanceCardTo(t, rec, ctx, card.ID, approval.StateRevalidating)

	rc := enabledContext(card, native)
	rc.Inputs.PermissionGranted = false

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, rec, writer, fakeResolver{ctx: rc})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("resume execute (gate fail): %v", err)
	}
	if res.DidWrite || !res.Blocked || res.FailedGate != GatePermission {
		t.Fatalf("resume: didWrite=%v blocked=%v gate=%q; want blocked-on-permission, no write", res.DidWrite, res.Blocked, res.FailedGate)
	}
	if got := atomic.LoadInt32(writes); got != 0 {
		t.Fatalf("external writes = %d; want 0", got)
	}
	after, _ := q.GetApprovalCard(ctx, card.ID)
	if after.State != string(approval.StateInvalidated) {
		t.Fatalf("card state = %q; want invalidated after a blocked resume", after.State)
	}
}
