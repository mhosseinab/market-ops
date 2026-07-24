package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// ── Overlay-coverage regression for issue #106 finding F1 ────────────────────
//
// EXE-005 / OUT-001 / §4.6 (no false execution claim, no silent fallback):
// GET /actions renders the card page and overlays each row's execution mode +
// canonical state. The page and the overlay read DIFFERENT tables with DIFFERENT
// sort keys (approval_cards.created_at DESC vs recommend_only_actions.approved_at
// DESC), so a separately-limited "newest N" overlay does NOT cover the page it is
// overlaid onto: an execution-bearing card inside the page but outside the
// overlay's own top-N is emitted with executionMode / canonicalState /
// recommendOnlyState ALL ABSENT, which the queue renders as "not executed yet" —
// a false claim about a tracked AwaitingExternalExecution action.
//
// The fix makes coverage STRUCTURAL: the overlay is fetched for the EXACT card
// ids of the returned page, so every execution-bearing row on the page carries
// its own overlay by construction, at any limit.

// overlayGapFixture is one account holding several recommend-only-tracked
// Approved cards whose approved_at order is the REVERSE of their created_at
// order — the shape that separates a created_at-ordered page from an
// approved_at-ordered overlay page.
type overlayGapFixture struct {
	org     uuid.UUID
	account uuid.UUID
	// cards in created_at ASCENDING order (cards[len-1] is the newest).
	cards []db.ApprovalCard
}

// seedOverlayGapAccount seeds org → account → product → variant → recommendation
// and then n single-version approval cards, each driven to Approved through the
// REAL §8.4 state machine and tracked recommend-only through the SAME production
// query the execution service uses (InsertRecommendOnlyAction, which leaves the
// card Approved).
//
// approved_at runs OPPOSITE to created_at: the FIRST card created gets the NEWEST
// approved_at. Everything is produced by production queries — no fabricated row
// the SQL could not produce.
func seedOverlayGapAccount(t *testing.T, pool *pgxpool.Pool, q *db.Queries, n int) overlayGapFixture {
	t.Helper()
	ctx := context.Background()

	org, err := q.CreateOrganization(ctx, "overlay-gap-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Overlay Gap Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID, NativeProductID: nativeProduct, Title: "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	variant, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID, ProductID: prod.ID,
		NativeVariantID: nativeVariant, NativeProductID: nativeProduct,
		SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}

	recLineage := uuid.New()
	var recID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO recommendations (
			marketplace_account_id, variant_id, lineage_id, version, objective,
			current_price_mantissa, current_price_currency, current_price_exponent,
			readiness, evidence_quality,
			cost_profile_version, policy_version, context_version, parameter_version)
		VALUES ($1,$2,$3,1,'maximize_contribution',100000,'IRR',0,'complete','verified',1,1,1,1)
		RETURNING id`, acct.ID, variant.ID, recLineage).Scan(&recID); err != nil {
		t.Fatalf("insert recommendation: %v", err)
	}

	rec := recommendation.NewService(pool)
	approvedBase := time.Now().UTC()
	cards := make([]db.ApprovalCard, 0, n)
	for i := 0; i < n; i++ {
		actionID := uuid.New()
		binding := approval.Binding{
			ActionID: actionID, ParameterVersion: 1, ContextVersion: 1,
			PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(time.Hour),
		}
		card, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
			RecommendationID: recID, MarketplaceAccountID: acct.ID, LineageID: uuid.New(),
			ActionID: actionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
			EvidenceVersions: []byte("{}"), IdempotencyKey: binding.IdempotencyKey(),
			State: string(approval.StateDraft), PriceMantissa: int64(95000 + i), PriceCurrency: "IRR", PriceExponent: 0,
			ExpiresAt: binding.Expiry,
		})
		if err != nil {
			t.Fatalf("insert card %d: %v", i, err)
		}
		for _, step := range []struct{ from, to approval.State }{
			{approval.StateDraft, approval.StateReadyForReview},
			{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
			{approval.StateAwaitingConfirmation, approval.StateApproved},
		} {
			if _, err := rec.Advance(ctx, card.ID, step.from, step.to, "seed"); err != nil {
				t.Fatalf("advance card %d %s→%s: %v", i, step.from, step.to, err)
			}
		}
		card, err = q.GetApprovalCard(ctx, card.ID)
		if err != nil {
			t.Fatalf("reload card %d: %v", i, err)
		}
		// approved_at DESCENDS as created_at ASCENDS: the first card created is the
		// newest by approved_at, so an approved_at-ordered "newest N" overlay page
		// covers exactly the cards a created_at-ordered page does NOT.
		approvedAt := approvedBase.Add(-time.Duration(i) * time.Minute)
		if _, err := q.InsertRecommendOnlyAction(ctx, db.InsertRecommendOnlyActionParams{
			CardID: card.ID, ActionID: card.ActionID, MarketplaceAccountID: acct.ID, VariantID: variant.ID,
			ApprovedPriceMantissa: card.PriceMantissa, ApprovedPriceCurrency: card.PriceCurrency,
			ApprovedPriceExponent: card.PriceExponent,
			ApprovedAt:            approvedAt, WindowExpiresAt: approvedAt.Add(24 * time.Hour),
		}); err != nil {
			t.Fatalf("track card %d recommend-only: %v", i, err)
		}
		cards = append(cards, card)
	}

	// The scenario is only meaningful if created_at is strictly increasing (so the
	// page order is deterministic and genuinely opposite to approved_at order).
	for i := 1; i < len(cards); i++ {
		if !cards[i].CreatedAt.After(cards[i-1].CreatedAt) {
			t.Fatalf("seed produced non-increasing created_at at %d (%s !> %s); the page/overlay divergence cannot be exercised",
				i, cards[i].CreatedAt, cards[i-1].CreatedAt)
		}
	}
	return overlayGapFixture{org: org.ID, account: acct.ID, cards: cards}
}

// overlayGapServer wires the REAL approval + execution services (read-only: no
// writer, no resolver) with a session principal for the fixture's org.
func overlayGapServer(t *testing.T, pool *pgxpool.Pool, f overlayGapFixture, token string) *http.Server {
	t.Helper()
	fa := newFakeAuth()
	fa.principals[token] = auth.Principal{
		UserID:         uuid.New(),
		OrganizationID: f.org,
		Email:          "owner@x.io",
		Role:           perm.RoleOwner,
		ExpiresAt:      time.Now().Add(time.Hour).UTC(),
	}
	rec := recommendation.NewService(pool)
	exec := execution.NewService(pool, rec, nil, nil)
	return NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithApproval(rec), WithExecution(exec), WithCookieSecure(false))
}

func listActionsWithLimit(t *testing.T, srv *http.Server, token, account string, limit int) gateway.ActionList {
	t.Helper()
	path := "/actions?marketplaceAccountId=" + account + "&limit=" + strconv.Itoa(limit)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	res := httptest.NewRecorder()
	srv.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, body = %s", path, res.Code, res.Body.String())
	}
	var out gateway.ActionList
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode ActionList: %v", err)
	}
	return out
}

// TestListActions_OverlayCoversEveryReturnedPageRow is the issue #106 F1
// regression: EVERY execution-bearing row on the returned page must carry its
// execution overlay, at ANY limit.
//
// Three recommend-only-tracked Approved cards whose approved_at order is the
// reverse of their created_at order, read at limit 2: the page (created_at DESC)
// returns cards[2] and cards[1], while an approved_at-ordered overlay page of the
// same limit covers cards[0] and cards[1] — leaving cards[2] with no overlay. A
// row with no overlay is rendered by the queue as a pre-execution card ("not
// executed yet"), which is a FALSE claim about a tracked
// AwaitingExternalExecution action (EXE-005, §4.6 no silent fallback).
func TestListActions_OverlayCoversEveryReturnedPageRow(t *testing.T) {
	pool, q := newIntegrationPool(t)
	f := seedOverlayGapAccount(t, pool, q, 3)
	srv := overlayGapServer(t, pool, f, "tok-owner")

	const limit = 2
	items := listActionsWithLimit(t, srv, "tok-owner", f.account.String(), limit).Items
	if len(items) != limit {
		t.Fatalf("GET /actions?limit=%d returned %d rows; want %d", limit, len(items), limit)
	}

	for _, item := range items {
		if item.ExecutionMode == nil || item.CanonicalState == nil {
			t.Fatalf("card %s is recommend-only TRACKED but came back with executionMode=%v canonicalState=%v — the queue renders it as a pre-execution card, a false 'not executed yet' claim about an AwaitingExternalExecution action (EXE-005, §4.6). Overlay coverage must be structural: fetch the overlay for the EXACT ids of the returned page.",
				item.Id, item.ExecutionMode, item.CanonicalState)
		}
		if *item.ExecutionMode != gateway.ExecutionMode(execution.ModeRecommendOnly) {
			t.Fatalf("card %s executionMode = %v; want recommend_only", item.Id, *item.ExecutionMode)
		}
		if item.RecommendOnlyState == nil ||
			*item.RecommendOnlyState != gateway.RecommendOnlyState(execution.StateAwaitingExternalExecution) {
			t.Fatalf("card %s recommendOnlyState = %v; want awaiting_external_execution", item.Id, item.RecommendOnlyState)
		}
		// A recommend-only action NEVER carries a write externalState.
		if item.ExternalState != nil {
			t.Fatalf("recommend-only card %s carries write externalState %v — a false write claim (never-cut)", item.Id, *item.ExternalState)
		}
	}

	// The page itself is still the newest-by-created_at prefix (ordering unchanged).
	newest := f.cards[len(f.cards)-1].ID
	if items[0].Id != newest {
		t.Fatalf("page[0] = %s; want the newest card %s (page ordering must not change)", items[0].Id, newest)
	}
}

// TestListUnifiedByCardIDs_CallerSuppliedIDsStayAccountScoped is the issue #102
// negative over the page-exact overlay: the card id set is caller-derived, so it
// must NEVER become an unscoped read.
//
//   - A foreign account's card id inside the set is silently absent from the
//     projection — no execution state of another tenant's action is disclosed.
//   - A foreign requestedAccount is ErrAccountNotFound, whatever the ids say.
func TestListUnifiedByCardIDs_CallerSuppliedIDsStayAccountScoped(t *testing.T) {
	pool, q := newIntegrationPool(t)
	mine := seedOverlayGapAccount(t, pool, q, 1)
	theirs := seedOverlayGapAccount(t, pool, q, 1)

	svc := execution.NewService(pool, recommendation.NewService(pool), nil, nil)
	ctx := context.Background()

	mineCard := mine.cards[0].ID
	theirsCard := theirs.cards[0].ID

	got, err := svc.ListUnifiedByCardIDsForOrg(ctx, mine.org, mine.account, []uuid.UUID{mineCard, theirsCard})
	if err != nil {
		t.Fatalf("own-account overlay: %v", err)
	}
	seen := map[uuid.UUID]bool{}
	for _, u := range got {
		seen[u.CardID] = true
	}
	if seen[theirsCard] {
		t.Fatalf("foreign card %s was projected into account %s's overlay — a caller-supplied id set must stay account-scoped (issue #102)",
			theirsCard, mine.account)
	}
	if !seen[mineCard] {
		t.Fatalf("own card %s missing from its own overlay (cards seen: %v)", mineCard, seen)
	}

	// A foreign requested account is rejected outright, ids notwithstanding.
	if _, err := svc.ListUnifiedByCardIDsForOrg(ctx, mine.org, theirs.account, []uuid.UUID{theirsCard}); err == nil {
		t.Fatalf("foreign requestedAccount was accepted; want ErrAccountNotFound")
	} else if !errors.Is(err, execution.ErrAccountNotFound) {
		t.Fatalf("foreign requestedAccount: err = %v; want ErrAccountNotFound", err)
	}
}
