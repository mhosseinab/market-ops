package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// advanceCard drives a card along a §8.4 path, hop by hop, so a test can place a
// member's card in a state DOWNSTREAM of Approved exactly the way the machine does
// (no direct state writes — every hop is FROM-guarded and appends its history row).
func advanceCard(t *testing.T, svc *recommendation.Service, cardID uuid.UUID, path ...approval.State) {
	t.Helper()
	ctx := context.Background()
	from := reloadState(t, svc, cardID)
	for _, to := range path {
		if _, err := svc.Advance(ctx, cardID, from, to, "test advance"); err != nil {
			t.Fatalf("advance %s → %s: %v", from, to, err)
		}
		from = to
	}
}

// TestConfirmBulkSelection_ResumeAfterExecutionAdvancedSealsAuthorization is the
// issue #90 blocker-2 regression: a bulk resume must report already_authorized for a
// member whose card has ADVANCED past Approved (Revalidating / Executing / a terminal
// external result), not mislabel it invalidated / not_control_bearing — while the
// still-eligible sibling that failed transiently IS retried by the same resume, and
// the advanced member is NEVER re-authorized or re-dispatched
// (exactly-one-action-per-eligible-member, §4.6 idempotency).
func TestConfirmBulkSelection_ResumeAfterExecutionAdvancedSealsAuthorization(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	_, account, variantA := seedTenant(t, q)
	variantB := seedSecondVariant(t, q, account)
	cardA := awaitingCard(t, svc, account, variantA) // will advance into execution
	cardB := awaitingCard(t, svc, account, variantB) // stays a live control (the retryable one)

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "resume", nil,
		[]recommendation.PreviewMemberInput{
			{VariantID: variantA, RecommendationID: cardA.RecommendationID},
			{VariantID: variantB, RecommendationID: cardB.RecommendationID},
		})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	lineage, version := res.Set.LineageID, res.Set.Version

	// Member A is authorized by a first confirmation, then advances downstream — the
	// exact partial-failure shape the resume has to survive.
	first, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	if st := itemFor(t, first.Items, cardA.RecommendationID).State; st != recommendation.BulkItemAuthorized {
		t.Fatalf("member A first confirm = %s; want authorized", st)
	}
	intentsAfterFirst := countIntents(t, pool, cardA.ID)
	if intentsAfterFirst != 1 {
		t.Fatalf("member A intents after first confirm = %d; want 1", intentsAfterFirst)
	}

	// Every state DOWNSTREAM of Approved must seal the authorization on resume.
	for _, tc := range []struct {
		name string
		path []approval.State
	}{
		{"revalidating", []approval.State{approval.StateRevalidating}},
		{"executing", []approval.State{approval.StateExecuting}},
		{"pending_reconciliation", []approval.State{approval.StatePendingReconciliation}},
		{"accepted", []approval.State{approval.StateAccepted}},
	} {
		advanceCard(t, svc, cardA.ID, tc.path...)
		resume, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
		if err != nil {
			t.Fatalf("resume with member A in %s: %v", tc.name, err)
		}
		itemA := itemFor(t, resume.Items, cardA.RecommendationID)
		if itemA.State != recommendation.BulkItemAlreadyAuthorized {
			t.Fatalf("member A in %s reported %s (reason %q); want already_authorized (sealed)",
				tc.name, itemA.State, itemA.Reason)
		}
		// NEVER re-executed: no second authorization, no second durable intent.
		if got := countIntents(t, pool, cardA.ID); got != 1 {
			t.Fatalf("member A in %s: intents = %d; want still exactly 1 (never re-dispatched)", tc.name, got)
		}
		if got := reloadState(t, svc, cardA.ID); got == approval.StateApproved {
			t.Fatalf("member A in %s was driven BACK to approved by a resume", tc.name)
		}
		// The still-eligible sibling IS retried by the same resume.
		if st := itemFor(t, resume.Items, cardB.RecommendationID).State; st != recommendation.BulkItemAuthorized && st != recommendation.BulkItemAlreadyAuthorized {
			t.Fatalf("member B on resume (A in %s) = %s; want the still-eligible member retried", tc.name, st)
		}
	}

	// Member B was authorized exactly once across all of those resumes.
	if got := countIntents(t, pool, cardB.ID); got != 1 {
		t.Fatalf("member B intents = %d; want exactly 1 across every resume", got)
	}
}

// TestConfirmBulkSelection_ResumeAfterFailedExecutionIsNotReAuthorized pins the
// §16 retry rule at the bulk seam: a member whose EXECUTION failed carries a sealed
// authorization, so a bulk re-confirm reports already_authorized and never mints a
// second authorization/intent. Retrying a failed action is the reconciliation-gated
// /actions retry path's decision (execution.Retry: PendingReconciliation must
// reconcile first, only a definitively Failed action is retry-eligible) — a bulk
// resume is never a back door around that gate.
func TestConfirmBulkSelection_ResumeAfterFailedExecutionIsNotReAuthorized(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	_, account, variant := seedTenant(t, q)
	card := awaitingCard(t, svc, account, variant)
	lineage, version := previewExecutableSet(t, svc, account, variant, card)

	if _, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor()); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	advanceCard(t, svc, card.ID, approval.StateRevalidating, approval.StateExecuting, approval.StateFailed)

	resume, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("resume after failed execution: %v", err)
	}
	item := itemFor(t, resume.Items, card.RecommendationID)
	if item.State != recommendation.BulkItemAlreadyAuthorized {
		t.Fatalf("failed-execution member reported %s (reason %q); want already_authorized", item.State, item.Reason)
	}
	if got := countIntents(t, pool, card.ID); got != 1 {
		t.Fatalf("failed-execution member intents = %d; want still exactly 1", got)
	}
	if got := reloadState(t, svc, card.ID); got != approval.StateFailed {
		t.Fatalf("failed-execution member state = %s; want failed (a bulk resume never re-drives it)", got)
	}
}

// TestConfirmBulkSelection_ResumeLeavesNeverAuthorizedMembersInvalidated is the
// negative half: a member that was NEVER authorized and no longer bears a control
// (expired, blocked, invalidated) must stay invalidated — the sealed-authorization
// fix must not turn a never-authorized member into a spurious already_authorized.
func TestConfirmBulkSelection_ResumeLeavesNeverAuthorizedMembersInvalidated(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	_, account, variant := seedTenant(t, q)
	card := awaitingCard(t, svc, account, variant)
	lineage, version := previewExecutableSet(t, svc, account, variant, card)

	// The member's control lapses before any confirmation: never authorized.
	advanceCard(t, svc, card.ID, approval.StateExpired)

	out, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	item := itemFor(t, out.Items, card.RecommendationID)
	if item.State != recommendation.BulkItemInvalidated {
		t.Fatalf("expired (never authorized) member reported %s; want invalidated", item.State)
	}
	if got := countIntents(t, pool, card.ID); got != 0 {
		t.Fatalf("expired member enqueued %d intents; want 0", got)
	}
}

// TestConfirmBulkSelection_ResumeAfterAllExecutionsFailedReportsNoExecutionPending is
// the issue #90 fix-cycle-1 M1 regression: `executionPending` must report a LIVE
// pending execution authorization, not merely "some member was authorized at some
// point". A resume over a set whose ONLY member's execution has definitively FAILED
// still (correctly) reports already_authorized per item — but nothing is pending, so
// the outcome-level flag must be false. Reporting true there told the operator an
// execution was in flight when the write had already terminated.
func TestConfirmBulkSelection_ResumeAfterAllExecutionsFailedReportsNoExecutionPending(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	_, account, variant := seedTenant(t, q)
	card := awaitingCard(t, svc, account, variant)
	lineage, version := previewExecutableSet(t, svc, account, variant, card)

	first, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	if !first.ExecutionPending {
		t.Fatalf("first confirm: executionPending=false; want true (the member IS newly approved)")
	}

	// The single member's execution runs to a DEFINITIVE failure.
	advanceCard(t, svc, card.ID, approval.StateRevalidating, approval.StateExecuting, approval.StateFailed)

	resume, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	item := itemFor(t, resume.Items, card.RecommendationID)
	if item.State != recommendation.BulkItemAlreadyAuthorized {
		t.Fatalf("failed-execution member = %s; want already_authorized (the authorization is still sealed)", item.State)
	}
	if resume.ExecutionPending {
		t.Fatalf("resume over an all-failed set reported executionPending=true; nothing is pending (card state=%s)",
			reloadState(t, svc, card.ID))
	}
}

// TestConfirmBulkSelection_ResumeWhileExecutionInFlightStillReportsPending is the
// positive half of M1: a member that is sealed-authorized AND still carries a live
// intent (Approved / Revalidating / Executing) keeps `executionPending` true, so the
// narrowed predicate did not silently turn the flag off for a genuinely in-flight
// bulk.
func TestConfirmBulkSelection_ResumeWhileExecutionInFlightStillReportsPending(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	_, account, variant := seedTenant(t, q)
	card := awaitingCard(t, svc, account, variant)
	lineage, version := previewExecutableSet(t, svc, account, variant, card)

	if _, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor()); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	for _, live := range []approval.State{approval.StateRevalidating, approval.StateExecuting} {
		advanceCard(t, svc, card.ID, live)
		resume, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
		if err != nil {
			t.Fatalf("resume with member in %s: %v", live, err)
		}
		if !resume.ExecutionPending {
			t.Fatalf("resume with member in %s reported executionPending=false; the intent is still live", live)
		}
	}
}
