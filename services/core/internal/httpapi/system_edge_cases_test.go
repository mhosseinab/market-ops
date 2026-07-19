package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// This file automates the PRD §16 edge-case table rows S32 named as
// offline-testable, at the SYSTEM boundary: real Postgres, real service
// packages, and — where the row is about what a caller SEES — the real
// mounted HTTP gateway, not just the owning package's own unit-level proof
// (which already exists per-row and is cited in each test's doc comment; this
// suite composes those into the end-to-end surface, which is the NEW S32
// value). Rows requiring a running LLM plane or a live DK connection are
// covered by the other S32 suites (containment replay, kill-switch journey)
// or listed as non-automatable in docs/implementation/dk-p0-progress.md.

func getJSON(t *testing.T, srv *http.Server, tok, path string, out any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("GET %s: decode: %v body=%s", path, err, rec.Body.String())
		}
	}
	return rec
}

// --- §16 "No events" — explicit no-action state, not an error. -------------
//
// The event package's own tests (internal/event) prove ranking and dedup; this
// test proves the OUTER SURFACE an operator/chat actually reads (GET /today,
// GET /events) renders a structured EMPTY state (200 + zero items) for an
// account with no market events — never a 404/500/null that a screen or the
// chat plane would have to special-case.
func TestEdgeCase_NoEvents_ExplicitNoActionState(t *testing.T) {
	pool, q := newSystemPool(t)
	account, _ := seedEventVariant(t, q)

	srv, tok := systemOwnerServerForAccount(t, q, account, WithEvent(event.NewService(pool)))

	var list gateway.MarketEventList
	rec := getJSON(t, srv, tok, "/events?marketplaceAccountId="+account.String(), &list)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /events on an account with no events: status=%d, want 200 (explicit no-action, not an error)", rec.Code)
	}
	if len(list.Items) != 0 {
		t.Fatalf("GET /events on an account with no events: got %d items, want 0", len(list.Items))
	}

	var feed gateway.TodayFeed
	rec = getJSON(t, srv, tok, "/today?marketplaceAccountId="+account.String(), &feed)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /today on an account with no events: status=%d, want 200", rec.Code)
	}
	if len(feed.Items) != 0 {
		t.Fatalf("GET /today on an account with no events: got %d items, want 0", len(feed.Items))
	}
}

// --- §16 "Duplicate event" — update the open event inside the dedup window. -
//
// internal/event/service_db_test.go's TestDedupUpdatesOpenRecordZeroDuplicates
// already proves the store-level invariant (one row, evidence_update_count
// increments). This test proves the SAME invariant is what the real gateway
// SURFACE returns: GET /today shows exactly one item after two duplicate
// detections, through the actual mounted router.
func TestEdgeCase_DuplicateEvent_SurfaceShowsOneItem(t *testing.T) {
	pool, q := newSystemPool(t)
	ctx := context.Background()
	account, variant := seedEventVariant(t, q)
	evtSvc := event.NewService(pool)
	now := time.Now().UTC()

	// This gateway-surface dedup test asserts one Today item after two detections; it
	// does not test evidence corroboration, so it uses a non-corroborated 'unverified'
	// quality — the only quality a candidate may carry without a backing observation
	// (issue #70). Corroborated verified/supported derivation is covered by the event
	// package's evidence-provenance tests.
	first, ok := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualityUnverified, Ref: "r1"},
		Now:      now, TTL: time.Hour,
	})
	if !ok {
		t.Fatal("expected the winning-state candidate to fire")
	}
	if _, err := evtSvc.RecordFor(ctx, account, first); err != nil {
		t.Fatalf("record first: %v", err)
	}
	second, ok := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualityUnverified, Ref: "r2"},
		Now:      now.Add(10 * time.Minute), TTL: time.Hour,
	})
	if !ok {
		t.Fatal("expected the repeated winning-state candidate to fire")
	}
	res, err := evtSvc.RecordFor(ctx, account, second)
	if err != nil {
		t.Fatalf("record duplicate: %v", err)
	}
	if !res.Deduped {
		t.Fatal("the second detection must dedup, not open a new event")
	}

	srv, tok := systemOwnerServerForAccount(t, q, account, WithEvent(evtSvc))
	var feed gateway.TodayFeed
	rec := getJSON(t, srv, tok, "/today?marketplaceAccountId="+account.String(), &feed)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /today: status=%d, body=%s", rec.Code, rec.Body.String())
	}
	if len(feed.Items) != 1 {
		t.Fatalf("GET /today after a duplicate detection: got %d items, want exactly 1 (§16 never-cut: no duplicate Today item)", len(feed.Items))
	}
}

// --- §16 "Unknown write result" — Pending Reconciliation; no retry. ---------
//
// internal/execution/service_db_test.go's
// TestExecute_UnknownResultParksPendingAndRetryRejected proves this at the
// service layer directly. This test drives the SAME scenario through the real
// HTTP wire (POST /actions/execute then POST /actions/retry) — the boundary a
// screen or the recommend-only reconciler actually calls — confirming the
// invariant survives transport marshalling and the fail-closed HTTP mapping.
func TestEdgeCase_UnknownWriteResult_PendingReconciliationBlocksRetry(t *testing.T) {
	pool, q := newSystemPool(t)
	card, native, org := seedApprovedCardSystem(t, pool, q)

	execSvc := execution.NewService(pool, recommendation.NewService(pool), unknownWriter{}, fixedResolver{card: card, nativeVariant: native})
	srv, tok := systemOwnerServerForOrg(t, org, WithExecution(execSvc))

	execBody, _ := json.Marshal(gateway.ExecuteActionRequest{CardId: card.ID})
	req := httptest.NewRequest(http.MethodPost, "/actions/execute", strings.NewReader(string(execBody)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out gateway.ExecuteActionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode execute: %v", err)
	}
	if out.ExternalState == nil || *out.ExternalState != gateway.ExecutionExternalState(execution.StatePendingReconciliation) {
		t.Fatalf("unknown write result: externalState=%v, want pending_reconciliation", out.ExternalState)
	}

	retryBody, _ := json.Marshal(gateway.RetryActionRequest{ActionId: card.ActionID})
	req2 := httptest.NewRequest(http.MethodPost, "/actions/retry", strings.NewReader(string(retryBody)))
	req2.Header.Set("Content-Type", "application/json")
	req2.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	rec2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("retry of a pending-reconciliation action: status=%d, want 409 (EXE-003, no retry until reconciled)", rec2.Code)
	}
}

// unknownWriter always reports an ambiguous (timeout-like) result — the §16
// "Unknown write result" trigger — mirroring
// internal/execution/service_db_test.go's identically-named unexported helper
// (kept local: it is test-only fixture behavior, not product logic to export).
type unknownWriter struct{}

func (unknownWriter) WritePrice(_ context.Context, _ execution.WriteRequest) execution.WriteResult {
	return execution.WriteResult{Outcome: execution.OutcomeUnknown, Detail: "simulated timeout"}
}

// systemOwnerServerForAccount builds a real gateway server whose Owner session is
// bound to the SEEDED ACCOUNT'S organization, so the event handlers' ownership guard
// (issue #67) admits reads for that account. Without this binding the fixed
// random-org owner session would be foreign to the freshly-seeded account and every
// read would correctly 404.
func systemOwnerServerForAccount(t *testing.T, q *db.Queries, account uuid.UUID, opts ...Option) (*http.Server, string) {
	t.Helper()
	acct, err := q.GetMarketplaceAccount(context.Background(), account)
	if err != nil {
		t.Fatalf("get account org: %v", err)
	}
	fa := newFakeAuth()
	owner := principal(perm.RoleOwner)
	owner.OrganizationID = acct.OrganizationID
	const tok = "s32-owner-session"
	fa.principals[tok] = owner
	base := []Option{WithAuth(fa), WithCookieSecure(false)}
	srv := NewServer(":0", BuildInfo{}, testLogger(), append(base, opts...)...)
	return srv, tok
}

// seedEventVariant creates org/account/product/variant for the event-engine
// scenarios (mirrors internal/event/service_db_test.go's seedVariant, which is
// unexported to its own package).
func seedEventVariant(t *testing.T, q *db.Queries) (account, variant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "s32-evt-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID: org.ID, NativeAccountID: "native-" + uuid.NewString(), DisplayName: "S32 Evt Seller",
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
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID, ProductID: prod.ID,
		NativeVariantID: nativeVariant, NativeProductID: nativeProduct,
		SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return acct.ID, v.ID
}
