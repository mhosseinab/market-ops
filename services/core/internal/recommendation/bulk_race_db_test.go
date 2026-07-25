package recommendation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"

	"github.com/jackc/pgx/v5/pgxpool"
)

// waitForLineageLockContention blocks until SOME session is waiting on the
// transaction-scoped advisory lock keyed on this lineage id (the exact key
// LockApprovalLineage takes: hashtextextended(lineage::text, 0), split across
// pg_locks.classid/objid). It is what makes the concurrency choreography below
// deterministic instead of sleep-timed: the test only proceeds once the loser is
// provably parked on the lock the winner holds.
func waitForLineageLockContention(t *testing.T, pool *pgxpool.Pool, lineage uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted
			  AND ((classid::bigint::bit(64) << 32) | objid::bigint::bit(64))
			      = (hashtextextended($1::uuid::text, 0))::bit(64)`,
			lineage.String()).Scan(&n); err != nil {
			t.Fatalf("poll advisory-lock contention: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no session ever blocked on the lineage lock for %s", lineage)
}

// TestConfirmBulkSelection_LoserOfAConcurrentConfirmReportsSealedAuthorization is the
// issue #90 fix-cycle-2 C1 regression (§4.6 idempotency + honest reporting). Two
// confirmations of the SAME member race: the winner advances the card
// AwaitingConfirmation → Approved and its write goes in flight; the loser read the
// card as AwaitingConfirmation BEFORE that commit, so its FROM-guarded advance
// matches no row and the individual confirm returns ErrRejectedTransition.
//
// The loser must report the SEALED authorization (already_authorized) with
// ExecutionPending TRUE — the card IS approved and a price write IS in flight.
// Reporting `failed` + ExecutionPending false (the pre-fix behavior) told the
// operator the member had failed and that nothing was pending while a write was live,
// which is the most dangerous shape of a false negative on an approval surface.
//
// The choreography is deterministic, not timing-based: the winner holds the card's
// lineage lock with its advance UNCOMMITTED, the loser is proven to be parked on that
// lock, and only then does the winner commit.
func TestConfirmBulkSelection_LoserOfAConcurrentConfirmReportsSealedAuthorization(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	_, account, variant := seedTenant(t, q)
	card := awaitingCard(t, svc, account, variant)
	lineage, version := previewExecutableSet(t, svc, account, variant, card)

	// The WINNER: holds the card-lineage lock and has already advanced the member to
	// Approved, uncommitted — exactly the state a concurrent confirmation is in
	// between its FROM-guarded advance and its commit.
	winner, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin winner tx: %v", err)
	}
	defer func() { _ = winner.Rollback(ctx) }()
	wq := db.New(winner)
	if err := wq.LockApprovalLineage(ctx, card.LineageID); err != nil {
		t.Fatalf("winner lock lineage: %v", err)
	}
	if _, err := svc.AdvanceTx(ctx, wq, card.ID,
		approval.StateAwaitingConfirmation, approval.StateApproved, "concurrent winner"); err != nil {
		t.Fatalf("winner advance: %v", err)
	}
	// The winner is a concurrent BULK confirmation of the SAME selection, so it also
	// appends that selection's provenance ledger row on its OWN transaction — exactly
	// what confirmIndividual does (issue #87, prior finding 1). Without this the
	// fixture models a winner no production path produces: a card driven to Approved
	// with NO bulk provenance at all, which is an INDIVIDUAL approval, and which must
	// (and now does) fail closed rather than report already_authorized. Modelling the
	// winner faithfully keeps this test's assertion exactly as strong as it was; see
	// TestConfirmBulkSelection_IndividuallyApprovedCardIsNotAlreadyAuthorizedForASelection
	// for the case where provenance is genuinely absent.
	if _, err := winner.Exec(ctx, `
		INSERT INTO bulk_action_bindings (
			selection_set_member_id, selection_set_id, selection_set_lineage_id,
			selection_set_version, marketplace_account_id, variant_id,
			recommendation_id, offer_identity, card_id, action_id)
		SELECT m.id, m.selection_set_id, s.lineage_id, s.version, m.marketplace_account_id,
		       m.variant_id, m.recommendation_id, m.offer_identity, $3, $4
		  FROM selection_set_members m
		  JOIN selection_sets s ON s.id = m.selection_set_id
		 WHERE s.lineage_id = $1 AND s.version = $2 AND m.recommendation_id = $5`,
		lineage, version, card.ID, card.ActionID, card.RecommendationID,
	); err != nil {
		t.Fatalf("winner append bulk provenance: %v", err)
	}

	type outcome struct {
		out recommendation.BulkConfirmOutcome
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
		done <- outcome{out, err}
	}()

	waitForLineageLockContention(t, pool, card.LineageID)
	if err := winner.Commit(ctx); err != nil {
		t.Fatalf("commit winner: %v", err)
	}

	got := <-done
	if got.err != nil {
		t.Fatalf("loser confirm: %v", got.err)
	}
	if !got.out.Valid {
		t.Fatalf("loser reported the bound version invalid; the version never changed")
	}
	item := itemFor(t, got.out.Items, card.RecommendationID)
	if item.State != recommendation.BulkItemAlreadyAuthorized {
		t.Fatalf("loser reported %s (reason %q); want already_authorized — the member IS durably approved",
			item.State, item.Reason)
	}
	if !got.out.ExecutionPending {
		t.Fatalf("loser reported executionPending=false while the member's card is %s and its write is in flight",
			reloadState(t, svc, card.ID))
	}
	if st := reloadState(t, svc, card.ID); st != approval.StateApproved {
		t.Fatalf("member card = %s; want approved (the loser must not have re-driven it)", st)
	}
}

// TestConfirmBulkSelection_ConcurrentReplayNeverMislabelsADurablyApprovedMember is the
// operator-visible shape of the same defect (issue #90 fix cycle 2, C1): a
// double-clicked bulk confirm — or a client retry of a confirmation whose response was
// lost — runs two ConfirmBulkSelection calls concurrently on the same
// (lineage, version). Whichever call loses ANY of the internal races (the FROM-guarded
// advance, or the pre-confirm card read going stale) must still report the member's
// authorization as SEALED and its execution as PENDING; neither call may report
// `failed` or `invalidated`, and the member must carry EXACTLY ONE durable execution
// intent across both calls (§4.6 idempotency).
func TestConfirmBulkSelection_ConcurrentReplayNeverMislabelsADurablyApprovedMember(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	const iterations = 12
	for i := 0; i < iterations; i++ {
		_, account, variant := seedTenant(t, q)
		card := awaitingCard(t, svc, account, variant)
		lineage, version := previewExecutableSet(t, svc, account, variant, card)

		outs := make([]recommendation.BulkConfirmOutcome, 2)
		errs := make([]error, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for c := 0; c < 2; c++ {
			wg.Add(1)
			go func(c int) {
				defer wg.Done()
				<-start
				outs[c], errs[c] = svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor())
			}(c)
		}
		close(start)
		wg.Wait()

		for c := range outs {
			if errs[c] != nil {
				t.Fatalf("iteration %d call %d: %v", i, c, errs[c])
			}
			item := itemFor(t, outs[c].Items, card.RecommendationID)
			switch item.State {
			case recommendation.BulkItemAuthorized, recommendation.BulkItemAlreadyAuthorized:
			default:
				t.Fatalf("iteration %d call %d reported %s (reason %q) for a member whose card is %s; a concurrent replay never invalidates or fails a durably approved member",
					i, c, item.State, item.Reason, reloadState(t, svc, card.ID))
			}
			if !outs[c].ExecutionPending {
				t.Fatalf("iteration %d call %d reported executionPending=false while the member's card is %s (item %s)",
					i, c, reloadState(t, svc, card.ID), item.State)
			}
		}
		if got := countIntents(t, pool, card.ID); got != 1 {
			t.Fatalf("iteration %d: intents = %d; want exactly 1 across the concurrent replay", i, got)
		}
	}
}
