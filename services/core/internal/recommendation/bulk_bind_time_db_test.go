package recommendation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// refreshOnFirstDispatch wraps the real dispatcher and runs a hook exactly once,
// during the FIRST member's dispatch — i.e. INSIDE the per-member authorization loop,
// after the binding transaction has committed and released the lineage lock. It is
// the only way to place a concurrent refresh at that precise point deterministically.
type refreshOnFirstDispatch struct {
	inner recommendation.ExecutionDispatcher
	once  sync.Once
	hook  func(first db.ApprovalCard)
}

func (d *refreshOnFirstDispatch) DispatchApprovedTx(ctx context.Context, tx pgx.Tx, card db.ApprovalCard) error {
	if err := d.inner.DispatchApprovedTx(ctx, tx, card); err != nil {
		return err
	}
	d.once.Do(func() { d.hook(card) })
	return nil
}

// TestConfirmBulkSelection_RefreshDuringMemberLoopDoesNotRetractTheBinding is the
// issue #90 fix-cycle-1 M3 regression fixture. It ASSERTS the actual, decided
// semantics rather than leaving them assumed: the binding is decided ONCE, at bind
// time, under the per-lineage lock; the lock is released before the member loop so a
// partial failure stays durable; and a refresh that commits a NEW, NARROWER version
// while that loop is still running does NOT retract the in-flight confirmation.
//
// Concretely: v1 = {X, Y}; during X's dispatch a concurrent refresh mints v2 = {X}
// (Y dropped); the still-running v1 loop then authorizes Y. Y ends `approved` even
// though the CURRENT selection version no longer contains it. The operator approved
// v1 — which sealed exactly {X, Y} — at bind time, and a later client-driven
// narrowing is not retroactive.
//
// This is NOT a server-side evidence/policy escape: any such change mints a new CARD
// version and is caught per-member by the individual confirm's authoritative-binding
// gate (APR-001). Only a client-driven membership narrowing races this window.
func TestConfirmBulkSelection_RefreshDuringMemberLoopDoesNotRetractTheBinding(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	svc := recommendation.NewService(pool)
	_, account, variantX := seedTenant(t, q)
	variantY := seedSecondVariant(t, q, account)
	cardX := awaitingCard(t, svc, account, variantX)
	cardY := awaitingCard(t, svc, account, variantY)
	recToVariant := map[uuid.UUID]uuid.UUID{
		cardX.RecommendationID: variantX,
		cardY.RecommendationID: variantY,
	}

	res, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "bind-time", nil,
		[]recommendation.PreviewMemberInput{
			{VariantID: variantX, RecommendationID: cardX.RecommendationID},
			{VariantID: variantY, RecommendationID: cardY.RecommendationID},
		})
	if err != nil {
		t.Fatalf("preview v1: %v", err)
	}
	lineage, v1 := res.Set.LineageID, res.Set.Version
	if len(res.Members) != 2 {
		t.Fatalf("v1 has %d members, want 2", len(res.Members))
	}

	// During the FIRST member's dispatch, a concurrent refresh mints v2 containing
	// ONLY that first member — the second is dropped from the current selection while
	// the v1 loop is still walking it.
	var refreshed, kept, dropped uuid.UUID
	var v2 int32
	hook := &refreshOnFirstDispatch{inner: realDispatcherFor(t, pool)}
	hook.hook = func(first db.ApprovalCard) {
		kept = first.RecommendationID
		for rec := range recToVariant {
			if rec != kept {
				dropped = rec
			}
		}
		out, err := svc.PreviewBulkSelection(ctx, account, lineage, "narrowed", nil,
			[]recommendation.PreviewMemberInput{{VariantID: recToVariant[kept], RecommendationID: kept}})
		if err != nil {
			t.Errorf("mid-loop refresh: %v", err)
			return
		}
		refreshed = out.Set.LineageID
		v2 = out.Set.Version
	}
	svc.SetExecutionDispatcher(hook)

	out, err := svc.ConfirmBulkSelection(ctx, account, lineage, v1, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("confirm v1: %v", err)
	}
	if refreshed != lineage || v2 <= v1 {
		t.Fatalf("mid-loop refresh did not mint a newer version in the same lineage: lineage=%s v2=%d (v1=%d)", refreshed, v2, v1)
	}

	// The bound confirmation ran to completion over v1's SEALED membership.
	if !out.Valid {
		t.Fatalf("confirmation bound to v%d reported invalid; currency is decided at bind time, not mid-loop", v1)
	}
	for _, rec := range []uuid.UUID{kept, dropped} {
		if st := itemFor(t, out.Items, rec).State; st != recommendation.BulkItemAuthorized {
			t.Fatalf("member %s = %s; want authorized (v1's sealed membership is what the operator approved)", rec, st)
		}
	}

	// The DROPPED member — absent from the now-current v2 — is durably approved. This
	// is the asserted boundary: a later narrowing does not retract it.
	cardIDs := map[uuid.UUID]uuid.UUID{cardX.RecommendationID: cardX.ID, cardY.RecommendationID: cardY.ID}
	if st := reloadState(t, svc, cardIDs[dropped]); st != approval.StateApproved {
		t.Fatalf("dropped member's card = %s; want approved (the bind-time authorization stands)", st)
	}
	if got := countIntents(t, pool, cardIDs[dropped]); got != 1 {
		t.Fatalf("dropped member intents = %d; want exactly 1", got)
	}

	// And the current version genuinely no longer contains it, so the fixture really
	// exercises the window rather than a no-op refresh.
	current, err := db.New(pool).GetCurrentSelectionSet(ctx, lineage)
	if err != nil {
		t.Fatalf("read current set: %v", err)
	}
	if current.Version != v2 {
		t.Fatalf("current version = %d, want the refreshed v%d", current.Version, v2)
	}
	members, err := db.New(pool).ListSelectionSetMembers(ctx, current.ID)
	if err != nil {
		t.Fatalf("list v2 members: %v", err)
	}
	for _, m := range members {
		if m.RecommendationID.Valid && uuid.UUID(m.RecommendationID.Bytes) == dropped {
			t.Fatalf("v2 still contains the dropped member; the fixture did not narrow the set")
		}
	}

	// Re-confirming the now-STALE v1 authorizes nothing: bind-time currency is not a
	// licence to keep replaying a superseded version.
	stale, err := svc.ConfirmBulkSelection(ctx, account, lineage, v1, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("stale re-confirm: %v", err)
	}
	if stale.Valid || len(stale.Items) != 0 {
		t.Fatalf("re-confirming the superseded v%d was accepted: %+v", v1, stale)
	}
}
