package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// awaitingCard persists an approvable card and drives it to AwaitingConfirmation
// (a live, control-bearing state) so its structured control can be confirmed.
func awaitingCard(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID) db.ApprovalCard {
	t.Helper()
	ctx := context.Background()
	card := persistApprovableCard(t, svc, account, variant)
	if _, err := svc.Advance(ctx, card.ID, approval.StateDraft, approval.StateReadyForReview, "ready"); err != nil {
		t.Fatalf("advance draft→ready: %v", err)
	}
	if _, err := svc.Advance(ctx, card.ID, approval.StateReadyForReview, approval.StateAwaitingConfirmation, "open"); err != nil {
		t.Fatalf("advance ready→awaiting: %v", err)
	}
	got, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("reload awaiting card: %v", err)
	}
	return got
}

// bindingOf reconstructs the exact APR-001 binding a client would echo from a
// persisted card row.
func bindingOf(t *testing.T, card db.ApprovalCard) approval.Binding {
	t.Helper()
	ev, err := recommendation.DecodeEvidenceVersions(card.EvidenceVersions)
	if err != nil {
		t.Fatalf("decode evidence versions: %v", err)
	}
	return approval.Binding{
		ActionID:           card.ActionID,
		ParameterVersion:   card.ParameterVersion,
		ContextVersion:     card.ContextVersion,
		PolicyVersion:      card.PolicyVersion,
		CostProfileVersion: card.CostProfileVersion,
		EvidenceVersions:   ev,
		Expiry:             card.ExpiresAt,
	}
}

// TestConfirmIndividual_SupersededCardFailsClosed is the issue #85 core defect:
// an EXACT, unchanged V1 binding must NOT approve V1 once V2 was minted in the
// same lineage (EditPrice). The superseded control fails closed: Invalidated,
// NO execution intent, and V1 ends Invalidated (APR-001).
func TestConfirmIndividual_SupersededCardFailsClosed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})

	v1 := awaitingCard(t, svc, account, variant)
	presentedV1 := bindingOf(t, v1) // the exact, unchanged V1 control binding.

	// V2 is minted in the SAME lineage (EditPrice → new card version). The edited
	// price EQUALS the account's authoritative Hold/MaximizeContribution proposal
	// (feasHigh 1050), so the policy re-check (issue #134) admits it and the version
	// mint proceeds.
	newPrice, err := money.New(1050, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	v2, err := svc.EditPrice(ctx, v1.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice (mint V2): %v", err)
	}
	if v2.LineageID != v1.LineageID || v2.Version <= v1.Version {
		t.Fatalf("V2 must be a greater version in the same lineage; got v1=%d v2=%d", v1.Version, v2.Version)
	}

	// Confirming V1 with its perfectly-replayed binding must fail closed.
	outcome, err := svc.ConfirmIndividual(ctx, v1.ID, presentedV1, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("ConfirmIndividual (superseded V1): unexpected error %v", err)
	}
	if outcome.State != approval.StateInvalidated {
		t.Fatalf("superseded V1 confirm state = %s; want invalidated", outcome.State)
	}
	if outcome.ExecutionPending {
		t.Fatalf("superseded V1 confirm produced execution intent (ExecutionPending true) — APR-001 violated")
	}
	// The only in-lineage mint (EditPrice) bumps the parameter version, so the
	// superseded control's reason is parameter_version_changed, resolved against
	// the authoritative current binding (not the client echo). It must never be
	// ReasonNone (which would imply the stale control was still valid).
	if outcome.Reason != approval.ReasonParameterChanged {
		t.Fatalf("superseded V1 reason = %q; want %q", outcome.Reason, approval.ReasonParameterChanged)
	}

	// The persisted V1 card must now be Invalidated; V2 is untouched (still Draft).
	reV1, err := svc.GetCard(ctx, v1.ID)
	if err != nil {
		t.Fatalf("reload V1: %v", err)
	}
	if reV1.State != string(approval.StateInvalidated) {
		t.Fatalf("persisted V1 state = %s; want invalidated", reV1.State)
	}
	reV2, err := svc.GetCard(ctx, v2.ID)
	if err != nil {
		t.Fatalf("reload V2: %v", err)
	}
	if reV2.State != string(approval.StateDraft) {
		t.Fatalf("V2 state = %s; want draft (untouched by the stale V1 confirm)", reV2.State)
	}
}

// TestConfirmIndividual_EveryDimensionInvalidatesControl proves the authoritative
// confirm still enforces per-dimension binding on the GENUINELY CURRENT card: a
// change to any single bound dimension routes to Invalidated with the exact
// APR-001 reason and no execution intent. A card-version supersede is its own
// dimension (ReasonSuperseded), proven by TestConfirmIndividual_SupersededCardFailsClosed.
func TestConfirmIndividual_EveryDimensionInvalidatesControl(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	cases := []struct {
		name   string
		mutate func(b *approval.Binding)
		want   approval.InvalidationReason
	}{
		{"action", func(b *approval.Binding) { b.ActionID = uuid.New() }, approval.ReasonActionMismatch},
		{"parameter", func(b *approval.Binding) { b.ParameterVersion++ }, approval.ReasonParameterChanged},
		{"context", func(b *approval.Binding) { b.ContextVersion++ }, approval.ReasonContextChanged},
		{"policy", func(b *approval.Binding) { b.PolicyVersion++ }, approval.ReasonPolicyChanged},
		{"cost", func(b *approval.Binding) { b.CostProfileVersion++ }, approval.ReasonCostChanged},
		{"evidence", func(b *approval.Binding) { b.EvidenceVersions = map[uuid.UUID]int64{uuid.New(): 9} }, approval.ReasonEvidenceChanged},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			card := awaitingCard(t, svc, account, variant)
			presented := bindingOf(t, card)
			c.mutate(&presented)
			outcome, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor())
			if err != nil {
				t.Fatalf("ConfirmIndividual: %v", err)
			}
			if outcome.State != approval.StateInvalidated {
				t.Fatalf("state = %s; want invalidated", outcome.State)
			}
			if outcome.Reason != c.want {
				t.Fatalf("reason = %q; want %q", outcome.Reason, c.want)
			}
			if outcome.ExecutionPending {
				t.Fatalf("invalidated confirm must not produce execution intent")
			}
		})
	}
}

// TestConfirmIndividual_CurrentCardApprovesAndReplayIsSafe proves the happy path
// (the genuinely current card, matching binding, approves and reports execution
// pending for S18) and that an idempotent replay of that same confirmation is
// safe: the second call finds no live control and fails closed with no second
// approval and no execution intent.
func TestConfirmIndividual_CurrentCardApprovesAndReplayIsSafe(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	card := awaitingCard(t, svc, account, variant)
	presented := bindingOf(t, card)

	outcome, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("ConfirmIndividual (current card): %v", err)
	}
	if outcome.State != approval.StateApproved {
		t.Fatalf("current-card confirm state = %s; want approved", outcome.State)
	}
	if !outcome.ExecutionPending {
		t.Fatalf("approved card must report ExecutionPending true (execution is S18)")
	}

	// Idempotent replay: the card is now Approved (no live control). A replay must
	// not re-approve or produce a second execution intent — it fails closed.
	_, err = svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor())
	if err != approval.ErrNoControl {
		t.Fatalf("replay of an already-confirmed control: err = %v; want ErrNoControl", err)
	}
	reloaded, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.State != string(approval.StateApproved) {
		t.Fatalf("card state after replay = %s; want still approved (no double transition)", reloaded.State)
	}
}

// TestConfirmIndividual_SerializesAgainstConcurrentMint proves the lineage lock:
// a confirm that races a concurrent V2 mint SERIALIZES behind it and, once the
// newer version is authoritative, fails closed — a stale approval never wins.
//
// The test holds the lineage advisory lock in a controlling transaction, launches
// the V1 confirm (which must BLOCK on that lock), mints V2 while the lock is held,
// then releases it. The confirm then observes V2 as current and invalidates V1
// with no execution intent.
func TestConfirmIndividual_SerializesAgainstConcurrentMint(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	v1 := awaitingCard(t, svc, account, variant)
	presentedV1 := bindingOf(t, v1)

	// Controlling transaction: take the lineage lock and hold it.
	ctrl, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ctrl tx: %v", err)
	}
	defer func() { _ = ctrl.Rollback(ctx) }()
	cq := db.New(ctrl)
	if err := cq.LockApprovalLineage(ctx, v1.LineageID); err != nil {
		t.Fatalf("lock lineage in ctrl tx: %v", err)
	}

	type result struct {
		outcome recommendation.ConfirmOutcome
		err     error
	}
	done := make(chan result, 1)
	go func() {
		o, e := svc.ConfirmIndividual(ctx, v1.ID, presentedV1, time.Now().UTC(), testActor())
		done <- result{o, e}
	}()

	// The confirm must be BLOCKED on the lineage lock we hold — it cannot have
	// resolved an outcome yet. (Without serialization it would approve V1 here.)
	select {
	case r := <-done:
		t.Fatalf("confirm completed while lineage lock was held (no serialization): %+v err=%v", r.outcome, r.err)
	case <-time.After(200 * time.Millisecond):
	}

	// Mint V2 in the same lineage while holding the lock (simulating the racing
	// EditPrice that won the lock first), then commit to release it.
	if _, err := cq.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID:     v1.RecommendationID,
		MarketplaceAccountID: v1.MarketplaceAccountID,
		LineageID:            v1.LineageID,
		ActionID:             v1.ActionID,
		ParameterVersion:     v1.ParameterVersion + 1,
		ContextVersion:       v1.ContextVersion,
		PolicyVersion:        v1.PolicyVersion,
		CostProfileVersion:   v1.CostProfileVersion,
		EvidenceVersions:     v1.EvidenceVersions,
		IdempotencyKey:       "action:" + v1.ActionID.String() + ":pv:concurrent",
		State:                string(approval.StateDraft),
		PriceMantissa:        v1.PriceMantissa,
		PriceCurrency:        v1.PriceCurrency,
		PriceExponent:        v1.PriceExponent,
		ExpiresAt:            v1.ExpiresAt,
	}); err != nil {
		t.Fatalf("mint V2 in ctrl tx: %v", err)
	}
	if err := ctrl.Commit(ctx); err != nil {
		t.Fatalf("commit ctrl tx: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("confirm after mint: unexpected error %v", r.err)
		}
		if r.outcome.State != approval.StateInvalidated {
			t.Fatalf("racing confirm state = %s; want invalidated (stale must not win)", r.outcome.State)
		}
		if r.outcome.ExecutionPending {
			t.Fatalf("racing confirm produced execution intent — stale approval won")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirm did not complete after the lineage lock was released")
	}

	reV1, err := svc.GetCard(ctx, v1.ID)
	if err != nil {
		t.Fatalf("reload V1: %v", err)
	}
	if reV1.State != string(approval.StateInvalidated) {
		t.Fatalf("V1 state = %s; want invalidated", reV1.State)
	}
}
