package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
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
	// variant / nativeVariant / recommendation back the fixture's cards; they are
	// retained so a test can add a FURTHER card to the SAME account and drive it
	// through the real write path (execution.Service.Execute).
	variant        uuid.UUID
	nativeVariant  int64
	recommendation uuid.UUID
}

// unlapsableApprovalHorizon is how far into the future an overlay fixture's
// recommend-only approval instant is dated so the fixture is UN-LAPSABLE by any
// concurrent reconciler pass, and the projection assertion can stay a STRICT
// equality against the durable row.
//
// recommend_only_actions state is GLOBALLY mutable during a test run: the EXE-005
// matcher batch (ListAwaitingRecommendOnlyActions) is account-unscoped, so another
// package's reconciler test running RunOnce with a clock past the window (e.g.
// internal/execution jobs_db_test.go / unify_recommend_only_db_test.go at
// ApprovedAt+25h) sweeps EVERY awaiting action in the shared database, this
// fixture's included, and can land between the projection read and the durable
// read-back.
//
// The lapse deadline is derived by execution.Match from approved_at + the 24h match
// window CONSTANT — NOT from the window_expires_at column (which is stored evidence
// the matcher does not read). So the un-lapsable knob is approved_at, not
// window_expires_at: dating the approval far enough ahead puts the derived deadline
// beyond any concurrent +25h clock, leaving the row awaiting for the whole run.
// window_expires_at stays the honest approved_at+24h so the stored row is exactly
// what production would write.
const unlapsableApprovalHorizon = 30 * 24 * time.Hour

// seedOverlayGapAccount seeds org → account → product → variant → recommendation
// and then n single-version approval cards, each driven to Approved through the
// REAL §8.4 state machine and tracked recommend-only through the SAME production
// query the execution service uses (InsertRecommendOnlyAction, which leaves the
// card Approved). Its approval instants are un-lapsable (see
// unlapsableApprovalHorizon).
//
// approved_at runs OPPOSITE to created_at: the FIRST card created gets the NEWEST
// approved_at. Everything is produced by production queries — no fabricated row
// the SQL could not produce.
func seedOverlayGapAccount(t *testing.T, pool *pgxpool.Pool, q *db.Queries, n int) overlayGapFixture {
	t.Helper()
	return seedOverlayGapAccountApprovedAt(t, pool, q, n, time.Now().UTC().Add(unlapsableApprovalHorizon))
}

// seedOverlayGapAccountApprovedAt is seedOverlayGapAccount with an explicit
// approval instant for the newest recommend-only row, so a fixture can also be
// seeded with an ALREADY-EXPIRED window (the deliberate-lapse projection fixture).
func seedOverlayGapAccountApprovedAt(
	t *testing.T, pool *pgxpool.Pool, q *db.Queries, n int, approvedBase time.Time,
) overlayGapFixture {
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

	f := overlayGapFixture{
		org: org.ID, account: acct.ID,
		variant: variant.ID, nativeVariant: nativeVariant, recommendation: recID,
	}
	cards := make([]db.ApprovalCard, 0, n)
	for i := 0; i < n; i++ {
		card := seedApprovedCardInOverlayAccount(t, pool, q, f, int64(95000+i))
		// approved_at DESCENDS as created_at ASCENDS: the first card created is the
		// newest by approved_at, so an approved_at-ordered "newest N" overlay page
		// covers exactly the cards a created_at-ordered page does NOT.
		approvedAt := approvedBase.Add(-time.Duration(i) * time.Minute)
		trackRecommendOnly(t, q, f, card, card.ActionID, approvedAt)
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
	f.cards = cards
	return f
}

// seedApprovedCardInOverlayAccount adds ONE more single-version approval card to an
// existing overlay fixture's account/recommendation and drives it to Approved
// through the REAL §8.4 state machine. It carries its own action id and its own
// stable idempotency key, so it is independently executable (EXE-002).
func seedApprovedCardInOverlayAccount(
	t *testing.T, pool *pgxpool.Pool, q *db.Queries, f overlayGapFixture, priceMantissa int64,
) db.ApprovalCard {
	t.Helper()
	ctx := context.Background()
	actionID := uuid.New()
	binding := approval.Binding{
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1,
		PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(time.Hour),
	}
	card, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: f.recommendation, MarketplaceAccountID: f.account, LineageID: uuid.New(),
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
		EvidenceVersions: []byte("{}"), IdempotencyKey: binding.IdempotencyKey(),
		State: string(approval.StateDraft), PriceMantissa: priceMantissa, PriceCurrency: "IRR", PriceExponent: 0,
		ExpiresAt: binding.Expiry,
	})
	if err != nil {
		t.Fatalf("insert card: %v", err)
	}
	rec := recommendation.NewService(pool)
	for _, step := range []struct{ from, to approval.State }{
		{approval.StateDraft, approval.StateReadyForReview},
		{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
		{approval.StateAwaitingConfirmation, approval.StateApproved},
	} {
		if _, err := rec.Advance(ctx, card.ID, step.from, step.to, "seed"); err != nil {
			t.Fatalf("advance card %s→%s: %v", step.from, step.to, err)
		}
	}
	approved, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	return approved
}

// trackRecommendOnly records an EXE-005 recommend-only tracking row for a card
// through the SAME production query the execution service uses. actionID is passed
// explicitly so a data-integrity fixture can bind a row whose action id does NOT
// match its card's action id (the overlay-anomaly case).
func trackRecommendOnly(
	t *testing.T, q *db.Queries, f overlayGapFixture, card db.ApprovalCard, actionID uuid.UUID, approvedAt time.Time,
) {
	t.Helper()
	if _, err := q.InsertRecommendOnlyAction(context.Background(), db.InsertRecommendOnlyActionParams{
		CardID: card.ID, ActionID: actionID, MarketplaceAccountID: f.account, VariantID: f.variant,
		ApprovedPriceMantissa: card.PriceMantissa, ApprovedPriceCurrency: card.PriceCurrency,
		ApprovedPriceExponent: card.PriceExponent,
		ApprovedAt:            approvedAt, WindowExpiresAt: approvedAt.Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("track card %s recommend-only: %v", card.ID, err)
	}
}

// seedWriteExecutedCard adds an Approved card to the fixture's account and drives it
// through the REAL EXE-001/EXE-002 write path (execution.Service.Execute with a
// write-ENABLED revalidation context and a definitively accepting writer), so the
// production `action_executions` row is written by production code — never inserted
// by the test. It returns the executed card version.
//
// This is what makes the WRITE half of the page-exact overlay (and its issue #102
// tenant predicate) load-bearing: without an action_executions row the write query
// returns zero rows and every assertion over it is vacuous.
func seedWriteExecutedCard(t *testing.T, pool *pgxpool.Pool, q *db.Queries, f overlayGapFixture) db.ApprovalCard {
	t.Helper()
	card := seedApprovedCardInOverlayAccount(t, pool, q, f, 99000)
	svc := execution.NewService(pool, recommendation.NewService(pool), acceptWriter{},
		fixedResolver{card: card, nativeVariant: f.nativeVariant})
	res, err := svc.Execute(context.Background(), card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute (write) card %s: %v", card.ID, err)
	}
	if !res.DidWrite || res.ExternalState != execution.StateAccepted {
		t.Fatalf("seed write: didWrite=%v externalState=%q; want a definitive accepted write",
			res.DidWrite, res.ExternalState)
	}
	if _, err := q.GetActionExecutionByAction(context.Background(), card.ActionID); err != nil {
		t.Fatalf("seed write: no durable action_executions row for action %s: %v", card.ActionID, err)
	}
	executed, err := q.GetApprovalCard(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("reload executed card: %v", err)
	}
	return executed
}

// overlayGapServer wires the REAL approval + execution services (read-only: no
// writer, no resolver) with a session principal for the fixture's org.
func overlayGapServer(t *testing.T, pool *pgxpool.Pool, f overlayGapFixture, token string) *http.Server {
	t.Helper()
	return overlayGapServerWithLogger(t, pool, f, token, testLogger())
}

// overlayGapServerWithLogger is overlayGapServer with an explicit structured
// logger, so a test can assert the boundary's structured anomaly output.
func overlayGapServerWithLogger(
	t *testing.T, pool *pgxpool.Pool, f overlayGapFixture, token string, logger *slog.Logger,
) *http.Server {
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
	return NewServer(":0", BuildInfo{}, logger,
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

// printPtr renders a *T as its VALUE (fmt prints a bare hex address for a
// pointer-to-string, which makes a failure message unreadable).
func printPtr[T any](p *T) any {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// assertRecommendOnlyOverlay asserts a projected row's overlay faithfully mirrors
// its DURABLE recommend_only_actions row:
//
//   - mode is recommend_only and the row carries NO write externalState (a
//     recommend-only action never makes a write claim — never-cut);
//   - canonicalState is exactly the canonical mapping of the projected
//     recommend-only state (no invented or defaulted canonical state);
//   - the projected recommend-only state equals the durable state EXACTLY.
//
// The equality is STRICT (no "an awaiting reading is also acceptable" escape): a
// relaxed clause would pass a hypothetical projection that hardcoded
// awaiting_external_execution, i.e. exactly the false "still tracking" claim this
// test exists to prevent (EXE-005, §4.6). Concurrent-resolution flake is removed at
// the SOURCE instead — the fixtures' approval instants are un-lapsable
// (unlapsableApprovalHorizon), so no foreign reconciler pass can move the durable
// row mid-test, and a divergence is therefore always a real projection bug.
func assertRecommendOnlyOverlay(t *testing.T, q *db.Queries, item gateway.ActionSummary, actionID uuid.UUID) {
	t.Helper()
	if item.ExecutionMode == nil || *item.ExecutionMode != gateway.ExecutionMode(execution.ModeRecommendOnly) {
		t.Fatalf("card %s executionMode = %v; want recommend_only", item.Id, printPtr(item.ExecutionMode))
	}
	// A recommend-only action NEVER carries a write externalState.
	if item.ExternalState != nil {
		t.Fatalf("recommend-only card %s carries write externalState %v — a false write claim (never-cut)", item.Id, *item.ExternalState)
	}
	if item.RecommendOnlyState == nil {
		t.Fatalf("card %s carries no recommendOnlyState", item.Id)
	}
	got := execution.RecommendOnlyState(*item.RecommendOnlyState)

	row, err := q.GetRecommendOnlyAction(context.Background(), actionID)
	if err != nil {
		t.Fatalf("read durable recommend-only row for action %s: %v", actionID, err)
	}
	durable := execution.RecommendOnlyState(row.State)
	if got != durable {
		t.Fatalf("card %s recommendOnlyState = %q; durable row is %q", item.Id, got, durable)
	}
	if item.CanonicalState == nil ||
		*item.CanonicalState != gateway.ActionCanonicalState(execution.Canonical(execution.ModeRecommendOnly, string(got))) {
		t.Fatalf("card %s canonicalState = %v; want %q for recommend-only state %q",
			item.Id, printPtr(item.CanonicalState), execution.Canonical(execution.ModeRecommendOnly, string(got)), got)
	}
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

	actionOf := map[uuid.UUID]uuid.UUID{}
	for _, c := range f.cards {
		actionOf[c.ID] = c.ActionID
	}
	for _, item := range items {
		if item.ExecutionMode == nil || item.CanonicalState == nil {
			t.Fatalf("card %s is recommend-only TRACKED but came back with executionMode=%v canonicalState=%v — the queue renders it as a pre-execution card, a false 'not executed yet' claim about an AwaitingExternalExecution action (EXE-005, §4.6). Overlay coverage must be structural: fetch the overlay for the EXACT ids of the returned page.",
				item.Id, printPtr(item.ExecutionMode), printPtr(item.CanonicalState))
		}
		assertRecommendOnlyOverlay(t, q, item, actionOf[item.Id])
	}

	// The page itself is still the newest-by-created_at prefix (ordering unchanged).
	newest := f.cards[len(f.cards)-1].ID
	if items[0].Id != newest {
		t.Fatalf("page[0] = %s; want the newest card %s (page ordering must not change)", items[0].Id, newest)
	}
}

// assertWriteOverlay asserts a projected row faithfully mirrors its DURABLE
// action_executions row: mode write, the exact EXE-003 externalState, the canonical
// mapping of that state, and NO recommendOnlyState (a write action never carries a
// recommend-only tracking state — the never-cut mode separation holds in BOTH
// directions on the list surface).
func assertWriteOverlay(t *testing.T, q *db.Queries, item gateway.ActionSummary, actionID uuid.UUID) {
	t.Helper()
	if item.ExecutionMode == nil || *item.ExecutionMode != gateway.ExecutionMode(execution.ModeWrite) {
		t.Fatalf("card %s executionMode = %v; want write", item.Id, printPtr(item.ExecutionMode))
	}
	if item.RecommendOnlyState != nil {
		t.Fatalf("write card %s carries recommendOnlyState %v — a write action is not recommend-only tracked (never-cut)",
			item.Id, *item.RecommendOnlyState)
	}
	if item.ExternalState == nil {
		t.Fatalf("write card %s carries no externalState", item.Id)
	}
	row, err := q.GetActionExecutionByAction(context.Background(), actionID)
	if err != nil {
		t.Fatalf("read durable action_executions row for action %s: %v", actionID, err)
	}
	if string(*item.ExternalState) != row.ExternalState {
		t.Fatalf("card %s externalState = %q; durable row is %q", item.Id, *item.ExternalState, row.ExternalState)
	}
	want := execution.Canonical(execution.ModeWrite, row.ExternalState)
	if item.CanonicalState == nil || *item.CanonicalState != gateway.ActionCanonicalState(want) {
		t.Fatalf("card %s canonicalState = %v; want %q for write external state %q",
			item.Id, printPtr(item.CanonicalState), want, row.ExternalState)
	}
}

// TestListActions_ProjectsWriteModeOverlay is the issue #106 acceptance-#1 WRITE
// half on the PRODUCTION transport seam: an account whose page carries BOTH a
// write-executed card and a recommend-only-tracked card must project each with its
// OWN mode, its own raw state, and the canonical mapping of that state.
//
// Before this fixture no test in the repository made the page-exact write overlay
// query (ListActionExecutionsByCardIDs) return a single row, so its result shape —
// and its issue #102 tenant predicate — were unverified on the seam GET /actions
// actually uses.
func TestListActions_ProjectsWriteModeOverlay(t *testing.T) {
	pool, q := newIntegrationPool(t)
	f := seedOverlayGapAccount(t, pool, q, 1)
	written := seedWriteExecutedCard(t, pool, q, f)
	srv := overlayGapServer(t, pool, f, "tok-owner")

	items := listActionsWithLimit(t, srv, "tok-owner", f.account.String(), 50).Items
	byCard := map[uuid.UUID]gateway.ActionSummary{}
	for _, it := range items {
		byCard[it.Id] = it
	}

	writeItem, ok := byCard[written.ID]
	if !ok {
		t.Fatalf("write-executed card %s absent from the actions page (%d rows)", written.ID, len(items))
	}
	assertWriteOverlay(t, q, writeItem, written.ActionID)

	// The recommend-only card in the SAME page keeps its own mode: both modes are
	// grouped by canonical state in ONE list (issue #106 acceptance #1).
	roCard := f.cards[0]
	roItem, ok := byCard[roCard.ID]
	if !ok {
		t.Fatalf("recommend-only card %s absent from the actions page", roCard.ID)
	}
	assertRecommendOnlyOverlay(t, q, roItem, roCard.ActionID)
}

// TestListUnifiedByCardIDs_WriteWinsOverStrayRecommendOnlyRow covers the precedence
// branch of the page-exact overlay: for ONE card version carrying BOTH a write
// execution and a recommend-only row, the WRITE wins and is the ONLY projection.
//
// A stray recommend-only row must never mask a real external write — that would
// render an executed action as "awaiting external execution", a false claim in the
// opposite direction (EXE-003/EXE-005, §4.6).
func TestListUnifiedByCardIDs_WriteWinsOverStrayRecommendOnlyRow(t *testing.T) {
	pool, q := newIntegrationPool(t)
	f := seedOverlayGapAccount(t, pool, q, 1)
	written := seedWriteExecutedCard(t, pool, q, f)
	// A stray recommend-only row on the SAME card version, bound to the SAME action.
	trackRecommendOnly(t, q, f, written, written.ActionID, time.Now().UTC().Add(unlapsableApprovalHorizon))

	svc := execution.NewService(pool, recommendation.NewService(pool), nil, nil)
	got, err := svc.ListUnifiedByCardIDsForOrg(context.Background(), f.org, f.account, []uuid.UUID{written.ID})
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("card %s projected %d overlay rows; want exactly 1 (the write)", written.ID, len(got))
	}
	if got[0].Mode != execution.ModeWrite {
		t.Fatalf("card %s projected mode %q; a stray recommend-only row masked the real write", written.ID, got[0].Mode)
	}
	if got[0].ExternalState != execution.StateAccepted {
		t.Fatalf("card %s externalState = %q; want accepted", written.ID, got[0].ExternalState)
	}
	if got[0].RecommendOnlyState != "" {
		t.Fatalf("write projection carries recommendOnlyState %q (never-cut mode separation)", got[0].RecommendOnlyState)
	}
}

// TestListActions_ProjectsTerminalLapsedRecommendOnlyState proves the overlay is
// faithful for a TERMINAL recommend-only state, not only for awaiting: an action
// the EXE-005 reconciler LAPSED is projected as lapsed / canonical lapsed at the
// HTTP boundary, with NO write externalState.
//
// Without this fixture every httpapi projection fixture is awaiting-only, so a
// projection that hardcoded awaiting_external_execution would satisfy them all
// while telling the queue a closed action is still tracking — a false "still
// pending" claim (EXE-005; §4.6 no false execution claim).
//
// The lapse is produced by the PRODUCTION reconciler (RunOnce), not by a hand-written
// state row. The fixture is dated with an ALREADY-EXPIRED window and the reconciler
// runs on its DEFAULT real clock, so the pass lapses this fixture deliberately while
// leaving normally-dated awaiting rows elsewhere in the shared database untouched.
func TestListActions_ProjectsTerminalLapsedRecommendOnlyState(t *testing.T) {
	pool, q := newIntegrationPool(t)
	// Approved 25h ago: the derived 24h match deadline has already passed at the
	// reconciler's real-now clock.
	f := seedOverlayGapAccountApprovedAt(t, pool, q, 1, time.Now().UTC().Add(-25*time.Hour))
	srv := overlayGapServer(t, pool, f, "tok-owner")
	card := f.cards[0]

	if _, err := execution.NewRecommendOnlyReconciler(pool, nil).RunOnce(context.Background()); err != nil {
		t.Fatalf("reconciler RunOnce: %v", err)
	}
	durable, err := q.GetRecommendOnlyAction(context.Background(), card.ActionID)
	if err != nil {
		t.Fatalf("read durable recommend-only row: %v", err)
	}
	if durable.State != string(execution.StateLapsed) {
		t.Fatalf("fixture setup: durable state = %q; want lapsed (the deliberate terminal state)", durable.State)
	}

	items := listActionsWithLimit(t, srv, "tok-owner", f.account.String(), 50).Items
	var got *gateway.ActionSummary
	for i := range items {
		if items[i].Id == card.ID {
			got = &items[i]
		}
	}
	if got == nil {
		t.Fatalf("lapsed card %s absent from the actions page (%d rows)", card.ID, len(items))
	}
	assertRecommendOnlyOverlay(t, q, *got, card.ActionID)
	if *got.RecommendOnlyState != gateway.RecommendOnlyState(execution.StateLapsed) {
		t.Fatalf("card %s recommendOnlyState = %q; want lapsed", card.ID, *got.RecommendOnlyState)
	}
	if *got.CanonicalState != gateway.ActionCanonicalState(execution.CanonicalLapsed) {
		t.Fatalf("card %s canonicalState = %q; want %q", card.ID, *got.CanonicalState, execution.CanonicalLapsed)
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
	// BOTH halves of the overlay must be load-bearing: each account also holds a
	// WRITE-executed card, so the write query's account predicate is exercised with
	// rows that actually exist. Without a foreign action_executions row the write
	// half returns zero rows and the "foreign card not projected" assertion is
	// satisfied vacuously — the tenant predicate could be neutralised unnoticed.
	mineWrite := seedWriteExecutedCard(t, pool, q, mine)
	theirsWrite := seedWriteExecutedCard(t, pool, q, theirs)

	svc := execution.NewService(pool, recommendation.NewService(pool), nil, nil)
	ctx := context.Background()

	mineCard := mine.cards[0].ID
	theirsCard := theirs.cards[0].ID

	got, err := svc.ListUnifiedByCardIDsForOrg(ctx, mine.org, mine.account,
		[]uuid.UUID{mineCard, mineWrite.ID, theirsCard, theirsWrite.ID})
	if err != nil {
		t.Fatalf("own-account overlay: %v", err)
	}
	seen := map[uuid.UUID]execution.Mode{}
	for _, u := range got {
		seen[u.CardID] = u.Mode
	}
	if _, ok := seen[theirsCard]; ok {
		t.Fatalf("foreign recommend-only card %s was projected into account %s's overlay — a caller-supplied id set must stay account-scoped (issue #102)",
			theirsCard, mine.account)
	}
	if _, ok := seen[theirsWrite.ID]; ok {
		t.Fatalf("foreign WRITE card %s was projected into account %s's overlay — another tenant's execution state (mode/externalState/timing) must never be overlaid onto this caller's actions list (issue #102)",
			theirsWrite.ID, mine.account)
	}
	if mode, ok := seen[mineCard]; !ok {
		t.Fatalf("own card %s missing from its own overlay (cards seen: %v)", mineCard, seen)
	} else if mode != execution.ModeRecommendOnly {
		t.Fatalf("own card %s projected mode %q; want recommend_only", mineCard, mode)
	}
	if mode, ok := seen[mineWrite.ID]; !ok {
		t.Fatalf("own WRITE card %s missing from its own overlay (cards seen: %v) — the write half must return rows for the test to be load-bearing",
			mineWrite.ID, seen)
	} else if mode != execution.ModeWrite {
		t.Fatalf("own write card %s projected mode %q; want write", mineWrite.ID, mode)
	}

	// A foreign requested account is rejected outright, ids notwithstanding.
	if _, err := svc.ListUnifiedByCardIDsForOrg(ctx, mine.org, theirs.account, []uuid.UUID{theirsCard, theirsWrite.ID}); err == nil {
		t.Fatalf("foreign requestedAccount was accepted; want ErrAccountNotFound")
	} else if !errors.Is(err, execution.ErrAccountNotFound) {
		t.Fatalf("foreign requestedAccount: err = %v; want ErrAccountNotFound", err)
	}
}
