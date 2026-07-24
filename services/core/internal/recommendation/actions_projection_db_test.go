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

// trackRecommendOnly writes the SAME production row the execution service writes
// when it records an approved action in recommend-only mode (EXE-005,
// execution.Service.recordRecommendOnly): a recommend_only_actions row bound to
// the EXACT card version that was approved, leaving the card itself Approved. It
// is the production sqlc query, not a fixture the SQL could not produce.
func trackRecommendOnly(t *testing.T, q *db.Queries, card db.ApprovalCard, variant uuid.UUID) db.RecommendOnlyAction {
	t.Helper()
	now := time.Now().UTC()
	row, err := q.InsertRecommendOnlyAction(context.Background(), db.InsertRecommendOnlyActionParams{
		CardID:                card.ID,
		ActionID:              card.ActionID,
		MarketplaceAccountID:  card.MarketplaceAccountID,
		VariantID:             variant,
		ApprovedPriceMantissa: card.PriceMantissa,
		ApprovedPriceCurrency: card.PriceCurrency,
		ApprovedPriceExponent: card.PriceExponent,
		ApprovedAt:            now,
		WindowExpiresAt:       now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("insert recommend-only action: %v", err)
	}
	return row
}

// cardIDs collects the returned card ids for readable assertions.
func cardIDs(rows []db.ApprovalCard) map[uuid.UUID]db.ApprovalCard {
	out := make(map[uuid.UUID]db.ApprovalCard, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

// TestListActions_ExecutionBearingHistoricalCardVersionStaysVisible is the PD-4
// projection-rule-(1) regression for issue #106 (EXE-005 / OUT-001 / AUD-001).
//
// The defect: the actions projection returned ONLY the greatest card version per
// lineage. The domain may legitimately mint a NEWER Draft on the SAME action
// lineage after an action was executed (recommend-only or write) — so the older
// TERMINAL, execution-bearing card version silently disappeared from the
// production action list, taking common API visibility, audit selection, and
// outcome discovery with it.
//
// The authoritative projection is: current lineage heads UNION card versions that
// carry an execution. This test builds that exact shape through the REAL DB with
// production queries only — an older recommend-only-tracked Approved card (v1) and
// a newer Draft (v2) sharing ONE action ID — and requires BOTH through
// ListActions. Approval versioning is never-cut: each version keeps its OWN
// version and parameter version; the projection must never collapse or re-stamp
// them.
func TestListActions_ExecutionBearingHistoricalCardVersionStaysVisible(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})

	// v1: approved, then tracked recommend-only (writes dark — the DEFAULT
	// production execution mode). The card stays Approved, exactly as
	// recordRecommendOnly leaves it.
	v1 := persistApprovableCard(t, svc, account, variant)
	driveToState(t, svc, v1.ID, approval.StateApproved)
	v1, err := q.GetApprovalCard(ctx, v1.ID)
	if err != nil {
		t.Fatalf("reload v1: %v", err)
	}
	trackRecommendOnly(t, q, v1, variant)

	// A SECOND lineage whose older version is NOT execution-bearing. It proves the
	// union admits ONLY execution-bearing history — never every historical version.
	plain := persistApprovableCard(t, svc, account, variant)
	driveToState(t, svc, plain.ID, approval.StateApproved)

	newPrice, err := money.New(1050, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	plainV2, err := svc.EditPrice(ctx, plain.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice (plain lineage): %v", err)
	}

	// v2: a newer Draft minted on v1's lineage, preserving the action ID.
	v2, err := svc.EditPrice(ctx, v1.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice (executed lineage): %v", err)
	}
	if v2.ActionID != v1.ActionID {
		t.Fatalf("edited card action id = %s, want the SAME action lineage %s", v2.ActionID, v1.ActionID)
	}
	if v2.LineageID != v1.LineageID {
		t.Fatalf("edited card lineage = %s, want %s", v2.LineageID, v1.LineageID)
	}

	rows, err := svc.ListActions(ctx, account, "", 200)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	byID := cardIDs(rows)

	if _, ok := byID[v2.ID]; !ok {
		t.Fatalf("ListActions omitted the current Draft head %s", v2.ID)
	}
	if _, ok := byID[v1.ID]; !ok {
		t.Fatalf("ListActions omitted the older EXECUTION-BEARING card version %s — a terminal executed card must remain addressable forever (PD-4 rule 1, EXE-005/OUT-001/AUD-001)", v1.ID)
	}
	if _, ok := byID[plain.ID]; ok {
		t.Fatalf("ListActions surfaced historical card %s that carries NO execution — the union admits execution-bearing history only", plain.ID)
	}
	if _, ok := byID[plainV2.ID]; !ok {
		t.Fatalf("ListActions omitted the current head %s of the non-executed lineage", plainV2.ID)
	}

	// Approval versioning is never-cut: each projected version keeps its OWN
	// version and parameter version; nothing is collapsed, merged, or re-stamped.
	if got := byID[v1.ID]; got.Version != v1.Version || got.ParameterVersion != v1.ParameterVersion {
		t.Fatalf("projected v1 = version %d/param %d, want %d/%d (approval versioning must not be re-stamped)",
			got.Version, got.ParameterVersion, v1.Version, v1.ParameterVersion)
	}
	if got := byID[v2.ID]; got.Version != v2.Version || got.ParameterVersion != v2.ParameterVersion {
		t.Fatalf("projected v2 = version %d/param %d, want %d/%d", got.Version, got.ParameterVersion, v2.Version, v2.ParameterVersion)
	}
	if v2.ParameterVersion == v1.ParameterVersion {
		t.Fatalf("edit must mint a NEW parameter version; both are %d", v1.ParameterVersion)
	}

	// Each card id appears EXACTLY once — the union deduplicates.
	seen := map[uuid.UUID]int{}
	for _, r := range rows {
		seen[r.ID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("card %s appeared %d times; the union must deduplicate by card id", id, n)
		}
	}
}

// TestListActions_StateFilterAuthoritativeOverUnion proves the §8.4 state filter
// stays AUTHORITATIVE over the UNIONED set (issue #142 must not regress): it is
// applied to current heads AND execution-bearing history BEFORE ORDER BY/LIMIT.
// The executed v1 is Approved and its lineage head v2 is Draft, so state=approved
// must return v1 (still addressable) and state=draft must return v2 — never the
// other way round, and never an unfiltered newest-N prefix.
func TestListActions_StateFilterAuthoritativeOverUnion(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})

	v1 := persistApprovableCard(t, svc, account, variant)
	driveToState(t, svc, v1.ID, approval.StateApproved)
	v1, err := q.GetApprovalCard(ctx, v1.ID)
	if err != nil {
		t.Fatalf("reload v1: %v", err)
	}
	trackRecommendOnly(t, q, v1, variant)

	newPrice, err := money.New(1050, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	v2, err := svc.EditPrice(ctx, v1.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice: %v", err)
	}

	// MORE-than-limit newer Draft heads: with the filter applied after the limit
	// the older Approved executed row would fall outside the window and vanish.
	const limit = 3
	for i := 0; i < limit+1; i++ {
		persistApprovableCard(t, svc, account, variant)
	}

	approved, err := svc.ListActions(ctx, account, string(approval.StateApproved), limit)
	if err != nil {
		t.Fatalf("ListActions(approved): %v", err)
	}
	if len(approved) != 1 || approved[0].ID != v1.ID {
		t.Fatalf("ListActions(approved) = %v, want exactly the executed card version %s", cardIDList(approved), v1.ID)
	}

	drafts, err := svc.ListActions(ctx, account, string(approval.StateDraft), 200)
	if err != nil {
		t.Fatalf("ListActions(draft): %v", err)
	}
	byID := cardIDs(drafts)
	if _, ok := byID[v2.ID]; !ok {
		t.Fatalf("ListActions(draft) omitted the current Draft head %s", v2.ID)
	}
	if _, ok := byID[v1.ID]; ok {
		t.Fatalf("ListActions(draft) surfaced the Approved card %s — the state filter must be authoritative", v1.ID)
	}
	for _, r := range drafts {
		if r.State != string(approval.StateDraft) {
			t.Fatalf("ListActions(draft) returned non-matching state %q", r.State)
		}
	}
}

// TestListActions_ExecutionBearingHistoryStaysTenantScoped is the issue #102
// negative: the union's execution-bearing branch is tenant-scoped exactly like the
// heads branch. Another account's executed card is never projected into this
// account's queue — a foreign account is a uniform absence, never another tenant's
// projection.
func TestListActions_ExecutionBearingHistoryStaysTenantScoped(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	mine, myVariant := seedVariant(t, q)
	theirs, theirVariant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})

	foreign := persistApprovableCard(t, svc, theirs, theirVariant)
	driveToState(t, svc, foreign.ID, approval.StateApproved)
	foreign, err := q.GetApprovalCard(ctx, foreign.ID)
	if err != nil {
		t.Fatalf("reload foreign card: %v", err)
	}
	trackRecommendOnly(t, q, foreign, theirVariant)
	newPrice, err := money.New(1050, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	if _, err := svc.EditPrice(ctx, foreign.ID, newPrice, time.Now().UTC()); err != nil {
		t.Fatalf("EditPrice (foreign): %v", err)
	}

	mineCard := persistApprovableCard(t, svc, mine, myVariant)

	rows, err := svc.ListActions(ctx, mine, "", 200)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	byID := cardIDs(rows)
	if _, ok := byID[foreign.ID]; ok {
		t.Fatalf("another account's execution-bearing card %s leaked into account %s's queue (issue #102)", foreign.ID, mine)
	}
	if _, ok := byID[mineCard.ID]; !ok {
		t.Fatalf("own card %s missing from the queue", mineCard.ID)
	}

	foreignApproved, err := svc.ListActions(ctx, mine, string(approval.StateApproved), 200)
	if err != nil {
		t.Fatalf("ListActions(approved): %v", err)
	}
	if len(foreignApproved) != 0 {
		t.Fatalf("state-filtered queue leaked %v across the tenant boundary", cardIDList(foreignApproved))
	}
}

func cardIDList(rows []db.ApprovalCard) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}
