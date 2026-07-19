package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// seedBlockedRecommendation persists a non-approvable recommendation (identity
// unconfirmed ⇒ a PRC-002 blocker is recorded), so PreviewBulkSelection resolves it
// as a blocked member server-side. It has NO approval card.
func seedBlockedRecommendation(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID) uuid.UUID {
	t.Helper()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	in.IdentityConfirmed = false // PRC-002 blocker ⇒ not approvable, blockers recorded.
	rec := recommendation.Assemble(in)
	if rec.Approvable() {
		t.Fatalf("seedBlockedRecommendation built an approvable recommendation")
	}
	persisted, err := svc.Persist(context.Background(), uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist blocked recommendation: %v", err)
	}
	return persisted.ID
}

// previewExecutableSet mints a server-side selection-set version whose single member
// is the given AwaitingConfirmation card's recommendation (disposition executable,
// resolved server-side). It returns the lineage and version the confirmation binds.
func previewExecutableSet(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID, card db.ApprovalCard) (uuid.UUID, int32) {
	t.Helper()
	res, err := svc.PreviewBulkSelection(context.Background(), account, uuid.Nil, "bulk-90",
		nil, []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}})
	if err != nil {
		t.Fatalf("preview bulk selection: %v", err)
	}
	if len(res.Members) != 1 || res.Members[0].Disposition != recommendation.DispositionExecutable {
		t.Fatalf("seeded member not executable: %+v", res.Members)
	}
	return res.Set.LineageID, res.Set.Version
}

// itemFor returns the per-item result for a recommendation id.
func itemFor(t *testing.T, items []recommendation.BulkItemResult, rec uuid.UUID) recommendation.BulkItemResult {
	t.Helper()
	for _, it := range items {
		if it.RecommendationID == rec {
			return it
		}
	}
	t.Fatalf("no bulk item for recommendation %s", rec)
	return recommendation.BulkItemResult{}
}

// TestConfirmBulkSelection_StaleVersionAuthorizesNothing is the negative test first
// (§4.6 approval versioning): a confirmation bound to a superseded selection-set
// version authorizes NOTHING — Valid is false, no items, and the member's live card
// stays AwaitingConfirmation with no execution intent.
func TestConfirmBulkSelection_StaleVersionAuthorizesNothing(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	card := awaitingCard(t, svc, account, variant)
	lineage, v1 := previewExecutableSet(t, svc, account, variant, card)

	// Mint a NEW version on the SAME lineage (a refreshed preview) — v1 is now stale.
	if _, err := svc.PreviewBulkSelection(ctx, account, lineage, "bulk-90",
		nil, []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}}); err != nil {
		t.Fatalf("mint v2: %v", err)
	}

	out, err := svc.ConfirmBulkSelection(ctx, lineage, v1, time.Now().UTC())
	if err != nil {
		t.Fatalf("confirm bulk: %v", err)
	}
	if out.Valid {
		t.Fatalf("stale bound version %d reported valid (current=%d)", v1, out.CurrentVersion)
	}
	if len(out.Items) != 0 {
		t.Fatalf("stale confirm authorized %d items; want 0", len(out.Items))
	}
	if got := reloadState(t, svc, card.ID); got != approval.StateAwaitingConfirmation {
		t.Fatalf("member card advanced to %s on stale confirm; want awaiting_confirmation", got)
	}
	if got := countIntents(t, pool, card.ID); got != 0 {
		t.Fatalf("stale confirm enqueued %d execution intents; want 0", got)
	}
}

// TestConfirmBulkSelection_ValidAuthorizesExactlyOnce proves a valid confirmation
// creates EXACTLY ONE authorization + execution intent per executable member, and a
// replay is idempotent (already_authorized, no second intent) — §4.6 idempotency.
func TestConfirmBulkSelection_ValidAuthorizesExactlyOnce(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	card := awaitingCard(t, svc, account, variant)
	lineage, v1 := previewExecutableSet(t, svc, account, variant, card)

	out, err := svc.ConfirmBulkSelection(ctx, lineage, v1, time.Now().UTC())
	if err != nil {
		t.Fatalf("confirm bulk: %v", err)
	}
	if !out.Valid || !out.ExecutionPending {
		t.Fatalf("valid confirm: valid=%v pending=%v; want both true", out.Valid, out.ExecutionPending)
	}
	item := itemFor(t, out.Items, card.RecommendationID)
	if item.State != recommendation.BulkItemAuthorized {
		t.Fatalf("member state = %s; want authorized", item.State)
	}
	if got := reloadState(t, svc, card.ID); got != approval.StateApproved {
		t.Fatalf("member card = %s; want approved", got)
	}
	if got := countIntents(t, pool, card.ID); got != 1 {
		t.Fatalf("intents after confirm = %d; want exactly 1", got)
	}

	// Replay (resume): idempotent — already_authorized, still exactly one intent.
	replay, err := svc.ConfirmBulkSelection(ctx, lineage, v1, time.Now().UTC())
	if err != nil {
		t.Fatalf("replay confirm: %v", err)
	}
	if st := itemFor(t, replay.Items, card.RecommendationID).State; st != recommendation.BulkItemAlreadyAuthorized {
		t.Fatalf("replay member state = %s; want already_authorized", st)
	}
	if got := countIntents(t, pool, card.ID); got != 1 {
		t.Fatalf("intents after replay = %d; want still exactly 1", got)
	}
}

// TestConfirmBulkSelection_BlockedMemberNeverExecutes proves a blocked member is
// reported excluded and never authorized or dispatched.
func TestConfirmBulkSelection_BlockedMemberNeverExecutes(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	// An executable member (has an awaiting card) plus a blocked member.
	execCard := awaitingCard(t, svc, account, variant)
	variant2 := seedSecondVariant(t, q, account)
	blockedRec := seedBlockedRecommendation(t, svc, account, variant2)

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "bulk-90", nil,
		[]recommendation.PreviewMemberInput{
			{VariantID: variant, RecommendationID: execCard.RecommendationID},
			{VariantID: variant2, RecommendationID: blockedRec},
		})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	out, err := svc.ConfirmBulkSelection(ctx, res.Set.LineageID, res.Set.Version, time.Now().UTC())
	if err != nil {
		t.Fatalf("confirm bulk: %v", err)
	}
	blocked := itemFor(t, out.Items, blockedRec)
	if blocked.Disposition != recommendation.DispositionBlocked || blocked.State != recommendation.BulkItemExcluded {
		t.Fatalf("blocked member = disp %s / state %s; want blocked/excluded", blocked.Disposition, blocked.State)
	}
	// The blocked recommendation has no live card at all; assert none was fabricated.
	if _, err := db.New(pool).GetCurrentApprovalCardByRecommendation(ctx, blockedRec); err == nil {
		t.Fatalf("blocked member unexpectedly has an approval card")
	}
	// The executable member IS authorized (durable partial: the blocked one did not
	// block the eligible one).
	if st := itemFor(t, out.Items, execCard.RecommendationID).State; st != recommendation.BulkItemAuthorized {
		t.Fatalf("executable member state = %s; want authorized", st)
	}
}

// TestConfirmBulkSelection_SupersededMemberInvalidatedButOthersAuthorized proves
// per-item partial semantics: a member whose live card was superseded (a price edit
// minted a new Draft version) fails closed (invalidated, no execution) while an
// unaffected executable member is still authorized in the SAME confirmation.
func TestConfirmBulkSelection_SupersededMemberInvalidatedButOthersAuthorized(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	keepCard := awaitingCard(t, svc, account, variant)
	variant2 := seedSecondVariant(t, q, account)
	supCard := awaitingCard(t, svc, account, variant2)

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "bulk-90", nil,
		[]recommendation.PreviewMemberInput{
			{VariantID: variant, RecommendationID: keepCard.RecommendationID},
			{VariantID: variant2, RecommendationID: supCard.RecommendationID},
		})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// Supersede the second member's live card with a new version (CHAT-044 edit).
	newPrice := mustMoney(t, 111100, "IRR", -2)
	if _, err := svc.EditPrice(ctx, supCard.ID, newPrice, time.Now().UTC()); err != nil {
		t.Fatalf("edit price: %v", err)
	}

	out, err := svc.ConfirmBulkSelection(ctx, res.Set.LineageID, res.Set.Version, time.Now().UTC())
	if err != nil {
		t.Fatalf("confirm bulk: %v", err)
	}
	if st := itemFor(t, out.Items, keepCard.RecommendationID).State; st != recommendation.BulkItemAuthorized {
		t.Fatalf("unaffected member = %s; want authorized", st)
	}
	if st := itemFor(t, out.Items, supCard.RecommendationID).State; st != recommendation.BulkItemInvalidated {
		t.Fatalf("superseded member = %s; want invalidated", st)
	}
	// The superseded original card must NOT have been approved or dispatched.
	if got := countIntents(t, pool, supCard.ID); got != 0 {
		t.Fatalf("superseded member enqueued %d intents; want 0", got)
	}
}

// TestConfirmBulkSelection_CrossAccountMemberRejected is the tenant-integrity
// negative test (never-cut, PRD §4.6): an executable member whose live approval
// card belongs to a DIFFERENT marketplace_account_id than the selection set must
// fail closed in authorizeBulkMember (account_mismatch), never be approved or
// dispatched. The set is minted under account A (so its member resolves — the
// recommendation belongs to A), but the member's live card was minted under a
// different tenant (account B); the cross-account guard must reject it.
func TestConfirmBulkSelection_CrossAccountMemberRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA := seedVariant(t, q)
	accountB, _ := seedVariant(t, q) // a DIFFERENT tenant; a valid FK for the mis-tenanted card.
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	// A recommendation that BELONGS to account A (so PreviewBulkSelection accepts it
	// and resolves it executable), but a live approval card minted under account B —
	// the cross-account corruption the tenant guard must catch.
	in := baseValidInput(t)
	in.AccountID = accountA
	in.VariantID = variantA
	in.EventID = uuid.Nil
	rec := recommendation.Assemble(in)
	if !rec.Approvable() {
		t.Fatalf("seed recommendation is not approvable")
	}
	persisted, err := svc.Persist(ctx, uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation: %v", err)
	}
	card, err := svc.CreateCard(ctx, persisted.ID, uuid.New(), accountB, rec) // WRONG tenant.
	if err != nil {
		t.Fatalf("create cross-account card: %v", err)
	}
	// Drive the mis-tenanted card to a live, control-bearing state so, absent the
	// tenant guard, it WOULD be an approvable control.
	if _, err := svc.Advance(ctx, card.ID, approval.StateDraft, approval.StateReadyForReview, "ready"); err != nil {
		t.Fatalf("advance draft→ready: %v", err)
	}
	if _, err := svc.Advance(ctx, card.ID, approval.StateReadyForReview, approval.StateAwaitingConfirmation, "open"); err != nil {
		t.Fatalf("advance ready→awaiting: %v", err)
	}

	// Account A's selection set: its single member resolves executable (the
	// recommendation belongs to A) — the preview cannot see the card's tenant.
	res, err := svc.PreviewBulkSelection(ctx, accountA, uuid.Nil, "bulk-90", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variantA, RecommendationID: persisted.ID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(res.Members) != 1 || res.Members[0].Disposition != recommendation.DispositionExecutable {
		t.Fatalf("seeded member not executable: %+v", res.Members)
	}

	out, err := svc.ConfirmBulkSelection(ctx, res.Set.LineageID, res.Set.Version, time.Now().UTC())
	if err != nil {
		t.Fatalf("confirm bulk: %v", err)
	}
	item := itemFor(t, out.Items, persisted.ID)
	if item.State != recommendation.BulkItemInvalidated {
		t.Fatalf("cross-account member state = %s; want invalidated", item.State)
	}
	if item.Reason != "account_mismatch" {
		t.Fatalf("cross-account member reason = %q; want account_mismatch", item.Reason)
	}
	// The mis-tenanted card stays a live control — never approved, never advanced.
	if got := reloadState(t, svc, card.ID); got != approval.StateAwaitingConfirmation {
		t.Fatalf("cross-account card advanced to %s; want awaiting_confirmation", got)
	}
	// Fail closed: no execution intent dispatched, and no pending-execution signal.
	if got := countIntents(t, pool, card.ID); got != 0 {
		t.Fatalf("cross-account confirm enqueued %d execution intents; want 0", got)
	}
	if out.ExecutionPending {
		t.Fatalf("cross-account confirm reported ExecutionPending; want false")
	}
}

// TestConfirmBulkSelection_UnknownLineageFailsClosed proves an unknown lineage is a
// fail-closed error, never a silent empty success.
func TestConfirmBulkSelection_UnknownLineageFailsClosed(t *testing.T) {
	pool, _ := newPool(t)
	svc := recommendation.NewService(pool)
	if _, err := svc.ConfirmBulkSelection(context.Background(), uuid.New(), 1, time.Now().UTC()); err == nil {
		t.Fatalf("unknown lineage returned no error; want fail-closed")
	}
}

// reloadState returns a card's current §8.4 state.
func reloadState(t *testing.T, svc *recommendation.Service, id uuid.UUID) approval.State {
	t.Helper()
	got, err := svc.GetCard(context.Background(), id)
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	return approval.State(got.State)
}
