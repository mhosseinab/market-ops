package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// TestListActions_StateFilterAppliedBeforeLimit is the issue #142 crux (Red):
// the state predicate must run in the authoritative SQL, BEFORE LIMIT — a page's
// limit bounds MATCHING rows, never an unfiltered newest-N prefix. We seed one
// OLDER Approved lineage head, then MORE-THAN-`limit` NEWER Draft heads. With the
// filter applied after the limit (the bug), the Approved head falls outside the
// newest-`limit` Draft window and vanishes; asking for state=approved returns
// empty even though a qualifying action exists.
func TestListActionsStateFilterAppliedBeforeLimit(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})

	// Oldest lineage head, driven to Approved (§8.4 happy path). Separate
	// transactions give each card a strictly increasing created_at (now() is
	// transaction-start), so this one is unambiguously the oldest.
	approvedCard := persistApprovableCard(t, svc, account, variant)
	driveToState(t, svc, approvedCard.ID, approval.StateApproved)

	// More-than-`limit` newer Draft lineage heads.
	const limit = 3
	for i := 0; i < limit+1; i++ {
		persistApprovableCard(t, svc, account, variant)
	}

	got, err := svc.ListActions(context.Background(), account, string(approval.StateApproved), limit)
	if err != nil {
		t.Fatalf("ListActions(approved, limit=%d): %v", limit, err)
	}
	if len(got) != 1 {
		t.Fatalf("ListActions(approved) = %d rows, want 1 — the older Approved head must not be hidden behind the newest-%d Draft window (issue #142)", len(got), limit)
	}
	if got[0].ID != approvedCard.ID {
		t.Fatalf("ListActions(approved) returned card %s, want the Approved head %s", got[0].ID, approvedCard.ID)
	}
	if got[0].State != string(approval.StateApproved) {
		t.Fatalf("returned card state = %s, want approved (the filter must be authoritative)", got[0].State)
	}
}

// TestListActions_StateFilterBoundsMatchingRows proves a filtered page bounds
// MATCHING rows: with more Draft heads than the limit and a limit smaller than the
// Draft count, every returned row is Draft and the page is full (never short
// because non-matching rows consumed the window).
func TestListActionsStateFilterBoundsMatchingRows(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})

	// One Approved head plus several Draft heads.
	approvedCard := persistApprovableCard(t, svc, account, variant)
	driveToState(t, svc, approvedCard.ID, approval.StateApproved)
	const draftCount = 5
	for i := 0; i < draftCount; i++ {
		persistApprovableCard(t, svc, account, variant)
	}

	const limit = 3
	got, err := svc.ListActions(context.Background(), account, string(approval.StateDraft), limit)
	if err != nil {
		t.Fatalf("ListActions(draft, limit=%d): %v", limit, err)
	}
	if len(got) != limit {
		t.Fatalf("ListActions(draft) = %d rows, want %d (a full page of MATCHING rows)", len(got), limit)
	}
	for _, r := range got {
		if r.State != string(approval.StateDraft) {
			t.Fatalf("returned non-matching card state %s, want draft", r.State)
		}
	}
}

// TestListActions_FilteredOnCurrentLineageHeadOnly proves the state filter is
// evaluated against the CURRENT (greatest-version) lineage head, not any historic
// version. A lineage whose head is Draft but that HAS an older Approved version in
// its history must NOT appear under state=approved.
func TestListActionsFilteredOnCurrentLineageHeadOnly(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})

	// Drive the ORIGINAL card (v1) to Approved FIRST, so the lineage genuinely
	// has an Approved version in its append-only history. Without this the query
	// could wrongly filter on a historic version and the test would still pass
	// (there'd be no Approved row at all) — it would not discriminate the bug.
	original := persistApprovableCard(t, svc, account, variant)
	driveToState(t, svc, original.ID, approval.StateApproved)

	// A price edit to the account's authoritative proposal (feasHigh 1050) then
	// mints a NEW Draft head (v2) in the SAME lineage. The Approved v1 now sits in
	// history; the CURRENT head is Draft.
	newPrice, err := money.New(1050, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	edited, err := svc.EditPrice(context.Background(), original.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice: %v", err)
	}

	// state=approved must NOT surface this lineage: the head is Draft even though
	// an Approved version exists in history. This now fails closed against a query
	// that (wrongly) matches a historic version's state.
	got, err := svc.ListActions(context.Background(), account, string(approval.StateApproved), 200)
	if err != nil {
		t.Fatalf("ListActions(approved): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListActions(approved) = %d rows, want 0 — the head is Draft; a historic Approved version must never surface the lineage", len(got))
	}

	// state=draft must surface exactly the CURRENT (edited) head — proving the
	// filter is evaluated on the greatest-version lineage head, not a stale one.
	draftHeads, err := svc.ListActions(context.Background(), account, string(approval.StateDraft), 200)
	if err != nil {
		t.Fatalf("ListActions(draft): %v", err)
	}
	if len(draftHeads) != 1 {
		t.Fatalf("ListActions(draft) = %d rows, want 1 (the current Draft head)", len(draftHeads))
	}
	if draftHeads[0].ID != edited.ID {
		t.Fatalf("ListActions(draft) returned card %s, want the current head %s", draftHeads[0].ID, edited.ID)
	}
}

// TestListActions_ReturnsCurrentVersionPerLineage_NewestFirst proves the
// actions queue read groups by lineage (current version only) and orders
// newest first (PD-3 item 5).
func TestListActionsReturnsCurrentVersionPerLineageNewestFirst(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})
	first := persistApprovableCard(t, svc, account, variant)

	// Edit to the authoritative proposal (feasHigh 1050): admitted by the policy
	// re-check (#134).
	newPrice, err := money.New(1050, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	edited, err := svc.EditPrice(context.Background(), first.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice: %v", err)
	}

	rows, err := svc.ListActions(context.Background(), account, "", 0)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListActions = %d rows, want 1 (one lineage, current version only)", len(rows))
	}
	if rows[0].ID != edited.ID {
		t.Fatalf("ListActions returned card %s, want the CURRENT (edited) version %s", rows[0].ID, edited.ID)
	}

	// State filter narrows to the exact state.
	filtered, err := svc.ListActions(context.Background(), account, "draft", 0)
	if err != nil {
		t.Fatalf("ListActions(draft): %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("ListActions(draft) = %d, want 1", len(filtered))
	}
	none, err := svc.ListActions(context.Background(), account, "approved", 0)
	if err != nil {
		t.Fatalf("ListActions(approved): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ListActions(approved) = %d, want 0 (card is in draft)", len(none))
	}
}
