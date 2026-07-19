package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// terminalAuditStates returns every terminal-event terminal_state recorded for an
// action, plus whether any external_result (a marketplace-WRITE claim) event was
// appended. It reads the append-only AUD-001 trail, transcript-independently.
func terminalAuditStates(t *testing.T, q *db.Queries, actionID uuid.UUID) (terminals []string, hasWriteClaim bool) {
	t.Helper()
	rep, err := audit.Reproduce(context.Background(), q, actionID)
	if err != nil {
		t.Fatalf("reproduce audit: %v", err)
	}
	for _, r := range rep.Records {
		switch r.EventType {
		case string(audit.EventTerminal):
			terminals = append(terminals, r.TerminalState)
		case string(audit.EventExternalResult):
			hasWriteClaim = true
		}
	}
	return terminals, hasWriteClaim
}

// TestRecommendOnlyReconciler_ExternallyExecuted_OpensOutcomeAndTerminalAudit_NoWrite
// is the unifying seam for EXE-005/OUT-001 (issue #106). An awaiting recommend-only
// action matched within its window must:
//   - reach externally_executed with a matched observation instant (evidence),
//   - open EXACTLY ONE OUT-001 outcome window (idempotent), and
//   - land a `terminal` AUD-001 record with terminal_state="externally_executed",
//
// while NEVER creating an action_executions (write) row and NEVER appending an
// external_result event — an externally-executed recommend-only action is NOT a
// marketplace write (never-cut: recommend-only vs execute separation).
func TestRecommendOnlyReconciler_ExternallyExecuted_OpensOutcomeAndTerminalAudit_NoWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)
	variant := variantOfCard(t, pool, card)

	exec := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{ctx: recommendOnlyContext(card, variant)})
	if _, err := exec.Execute(ctx, card.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (recommend-only): %v", err)
	}
	ro, err := q.GetRecommendOnlyAction(ctx, card.ActionID)
	if err != nil {
		t.Fatalf("get recommend-only: %v", err)
	}

	obs := OwnedPriceObservation{Price: mustMoney(t, ro.ApprovedPriceMantissa, ro.ApprovedPriceCurrency, int8(ro.ApprovedPriceExponent)), ObservedAt: ro.ApprovedAt.Add(time.Hour)}
	rec := NewRecommendOnlyReconciler(pool, fixedSource{variant: variant, obs: []OwnedPriceObservation{obs}}).
		WithClock(func() time.Time { return ro.ApprovedAt.Add(2 * time.Hour) })
	if _, err := rec.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	after, _ := q.GetRecommendOnlyAction(ctx, card.ActionID)
	if after.State != string(StateExternallyExecuted) {
		t.Fatalf("state = %q; want externally_executed", after.State)
	}
	if !after.MatchedObservationAt.Valid {
		t.Fatalf("externally_executed row missing matched observation instant (evidence)")
	}

	// NO write row: an externally-executed recommend-only action is not a write.
	if _, err := q.GetActionExecutionByAction(ctx, card.ActionID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("action_executions row for recommend-only action: err=%v; want ErrNoRows", err)
	}

	// Exactly one OUT-001 window, retaining the action as evidence anchor.
	win, err := q.GetOutcomeWindowByAction(ctx, card.ActionID)
	if err != nil {
		t.Fatalf("expected one outcome window; err=%v", err)
	}
	if !win.ClosesAt.After(win.OpenedAt) {
		t.Fatalf("outcome window closes_at %v not after opened_at %v", win.ClosesAt, win.OpenedAt)
	}

	// Terminal audit present, labelled externally_executed, with NO write claim.
	terminals, hasWrite := terminalAuditStates(t, q, card.ActionID)
	if hasWrite {
		t.Fatalf("recommend-only action appended an external_result (write) audit event")
	}
	if len(terminals) != 1 || terminals[0] != string(StateExternallyExecuted) {
		t.Fatalf("terminal audit states = %v; want exactly [externally_executed]", terminals)
	}
}

// TestRecommendOnlyReconciler_Lapsed_TerminalAuditNoWindowNoWrite proves a lapsed
// recommend-only action carries a `terminal` audit (terminal_state="lapsed") and
// makes NO false execution claim: no outcome window, no action_executions row, no
// external_result event (acceptance #4).
func TestRecommendOnlyReconciler_Lapsed_TerminalAuditNoWindowNoWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)
	variant := variantOfCard(t, pool, card)

	exec := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{ctx: recommendOnlyContext(card, variant)})
	if _, err := exec.Execute(ctx, card.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (recommend-only): %v", err)
	}
	ro, _ := q.GetRecommendOnlyAction(ctx, card.ActionID)

	rec := NewRecommendOnlyReconciler(pool, nil).
		WithClock(func() time.Time { return ro.ApprovedAt.Add(25 * time.Hour) })
	if _, err := rec.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	after, _ := q.GetRecommendOnlyAction(ctx, card.ActionID)
	if after.State != string(StateLapsed) {
		t.Fatalf("state = %q; want lapsed", after.State)
	}

	// NO outcome window for a lapse.
	if _, err := q.GetOutcomeWindowByAction(ctx, card.ActionID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("outcome window opened for a lapsed action: err=%v; want ErrNoRows", err)
	}
	// NO write row.
	if _, err := q.GetActionExecutionByAction(ctx, card.ActionID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("action_executions row for lapsed action: err=%v; want ErrNoRows", err)
	}
	// Terminal audit labelled lapsed, no write claim.
	terminals, hasWrite := terminalAuditStates(t, q, card.ActionID)
	if hasWrite {
		t.Fatalf("lapsed action appended an external_result (write) audit event")
	}
	if len(terminals) != 1 || terminals[0] != string(StateLapsed) {
		t.Fatalf("terminal audit states = %v; want exactly [lapsed]", terminals)
	}
}

// TestRecommendOnlyReconciler_Idempotent_SingleTerminalAndWindow proves two
// transition passes over the SAME awaiting action produce ONE terminal state, ONE
// outcome window (ON CONFLICT), and do NOT double-append the terminal audit — the
// matcher-race / replay guarantee (acceptance #5; never-cut idempotency).
func TestRecommendOnlyReconciler_Idempotent_SingleTerminalAndWindow(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)
	variant := variantOfCard(t, pool, card)

	exec := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{ctx: recommendOnlyContext(card, variant)})
	if _, err := exec.Execute(ctx, card.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (recommend-only): %v", err)
	}
	awaiting, _ := q.GetRecommendOnlyAction(ctx, card.ActionID)
	matchedAt := awaiting.ApprovedAt.Add(time.Hour)

	rec := NewRecommendOnlyReconciler(pool, nil).
		WithClock(func() time.Time { return awaiting.ApprovedAt.Add(2 * time.Hour) })

	// Two passes over the SAME awaiting row (a matcher race / replay).
	if err := rec.transition(ctx, awaiting, StateExternallyExecuted, matchedAt); err != nil {
		t.Fatalf("transition #1: %v", err)
	}
	if err := rec.transition(ctx, awaiting, StateExternallyExecuted, matchedAt); err != nil {
		t.Fatalf("transition #2 (replay): %v", err)
	}

	after, _ := q.GetRecommendOnlyAction(ctx, card.ActionID)
	if after.State != string(StateExternallyExecuted) {
		t.Fatalf("state = %q; want externally_executed", after.State)
	}
	if _, err := q.GetOutcomeWindowByAction(ctx, card.ActionID); err != nil {
		t.Fatalf("expected exactly one outcome window; err=%v", err)
	}
	terminals, _ := terminalAuditStates(t, q, card.ActionID)
	if len(terminals) != 1 {
		t.Fatalf("terminal audit records = %d; want exactly 1 (idempotent replay must not double-append)", len(terminals))
	}
}

// TestGetUnifiedAction_ResolvesRecommendOnlyAndWrite proves the common action read
// resolves BOTH a write execution and a recommend-only action (no longer 404),
// each labelled with its correct mode, and that a recommend-only action carries NO
// write external state (acceptance #2; common-read).
func TestGetUnifiedAction_ResolvesRecommendOnlyAndWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	// Write action.
	wcard, wnative := seedApprovedCard(t, pool, q)
	wexec := NewService(pool, recommendation.NewService(pool), acceptingWriter{}, fakeResolver{ctx: enabledContext(wcard, wnative)})
	if _, err := wexec.Execute(ctx, wcard.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (write): %v", err)
	}
	// Recommend-only action.
	rcard, _ := seedApprovedCard(t, pool, q)
	rvariant := variantOfCard(t, pool, rcard)
	rexec := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{ctx: recommendOnlyContext(rcard, rvariant)})
	if _, err := rexec.Execute(ctx, rcard.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (recommend-only): %v", err)
	}

	svc := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{})

	wv, err := svc.GetUnifiedAction(ctx, wcard.ActionID)
	if err != nil {
		t.Fatalf("get unified (write): %v", err)
	}
	if wv.Mode != ModeWrite {
		t.Fatalf("write action mode = %q; want write", wv.Mode)
	}
	if wv.ExternalState != StateAccepted {
		t.Fatalf("write action external state = %q; want accepted", wv.ExternalState)
	}

	rv, err := svc.GetUnifiedAction(ctx, rcard.ActionID)
	if err != nil {
		t.Fatalf("get unified (recommend-only) resolved to error (regression: 404): %v", err)
	}
	if rv.Mode != ModeRecommendOnly {
		t.Fatalf("recommend-only action mode = %q; want recommend_only", rv.Mode)
	}
	if rv.ExternalState != "" {
		t.Fatalf("recommend-only action carries a write external state %q (false write claim)", rv.ExternalState)
	}
	if rv.RecommendOnlyState != StateAwaitingExternalExecution {
		t.Fatalf("recommend-only state = %q; want awaiting_external_execution", rv.RecommendOnlyState)
	}

	// Unknown action fails closed with ErrNoRows.
	if _, err := svc.GetUnifiedAction(ctx, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown action get: err=%v; want ErrNoRows", err)
	}
}

// TestListUnifiedByAccount_BothModes proves the account projection returns BOTH a
// write action and a recommend-only action, each with its correct mode + canonical
// state (acceptance #1).
func TestListUnifiedByAccount_BothModes(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	wcard, wnative := seedApprovedCard(t, pool, q)
	account := wcard.MarketplaceAccountID
	wexec := NewService(pool, recommendation.NewService(pool), acceptingWriter{}, fakeResolver{ctx: enabledContext(wcard, wnative)})
	if _, err := wexec.Execute(ctx, wcard.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (write): %v", err)
	}

	// A recommend-only action in the SAME account.
	rcard := seedApprovedCardInAccount(t, pool, q, account, variantOfCard(t, pool, wcard))
	rexec := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{ctx: recommendOnlyContext(rcard, variantOfCard(t, pool, rcard))})
	if _, err := rexec.Execute(ctx, rcard.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (recommend-only): %v", err)
	}

	svc := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{})
	rows, err := svc.ListUnifiedByAccount(ctx, account, 100)
	if err != nil {
		t.Fatalf("list unified: %v", err)
	}
	byMode := map[Mode]UnifiedAction{}
	for _, r := range rows {
		byMode[r.Mode] = r
	}
	if _, ok := byMode[ModeWrite]; !ok {
		t.Fatalf("account list missing the write action (modes present: %v)", modesOf(rows))
	}
	if ro, ok := byMode[ModeRecommendOnly]; !ok {
		t.Fatalf("account list missing the recommend-only action (modes present: %v)", modesOf(rows))
	} else if ro.Canonical != CanonicalAwaiting {
		t.Fatalf("recommend-only canonical = %q; want awaiting", ro.Canonical)
	}
	if got := byMode[ModeWrite].Canonical; got != CanonicalSucceeded {
		t.Fatalf("write canonical = %q; want succeeded", got)
	}
}

func modesOf(rows []UnifiedAction) []Mode {
	out := make([]Mode, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Mode)
	}
	return out
}

// seedApprovedCardInAccount seeds a NEW recommendation + approval card for an
// EXISTING account/variant and advances it to Approved (so two actions can share
// one account for the unified-list projection).
func seedApprovedCardInAccount(t *testing.T, pool *pgxpool.Pool, q *db.Queries, account, variant uuid.UUID) db.ApprovalCard {
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
	binding := approval.Binding{
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1,
		PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(30 * time.Minute),
	}
	card, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: recID, MarketplaceAccountID: account, LineageID: uuid.New(),
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
		EvidenceVersions: []byte("{}"), IdempotencyKey: binding.IdempotencyKey(),
		State: string(approval.StateDraft), PriceMantissa: 95000, PriceCurrency: "IRR", PriceExponent: 0,
		ExpiresAt: binding.Expiry,
	})
	if err != nil {
		t.Fatalf("insert card: %v", err)
	}
	svc := recommendation.NewService(pool)
	for _, step := range []struct{ from, to approval.State }{
		{approval.StateDraft, approval.StateReadyForReview},
		{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
		{approval.StateAwaitingConfirmation, approval.StateApproved},
	} {
		if _, err := svc.Advance(ctx, card.ID, step.from, step.to, "seed"); err != nil {
			t.Fatalf("advance %s→%s: %v", step.from, step.to, err)
		}
	}
	approved, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("get approved card: %v", err)
	}
	return approved
}

// acceptingWriter returns a definitive Accepted result (terminal write) so the
// write path reaches a terminal external state.
type acceptingWriter struct{}

func (acceptingWriter) WritePrice(_ context.Context, _ WriteRequest) WriteResult {
	return WriteResult{Outcome: OutcomeAccepted}
}
