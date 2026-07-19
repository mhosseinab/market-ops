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
	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// fakeApproval is an ApprovalService stub for transport tests.
type fakeApproval struct {
	card        db.ApprovalCard
	history     []db.ApprovalCardState
	outcome     recommendation.ConfirmOutcome
	confirmErr  error
	bulkOutcome recommendation.BulkConfirmOutcome
	bulkErr     error
	err         error
	gotActor    audit.Actor

	editedCard   db.ApprovalCard
	editErr      error
	actions      []db.ApprovalCard
	rec          db.Recommendation
	recErr       error
	preview      recommendation.PreviewResult
	previewErr   error
	previewCalls int
}

func (f *fakeApproval) GetCardForOrg(context.Context, uuid.UUID, uuid.UUID) (db.ApprovalCard, error) {
	return f.card, f.err
}
func (f *fakeApproval) History(context.Context, uuid.UUID) ([]db.ApprovalCardState, error) {
	return f.history, f.err
}
func (f *fakeApproval) ConfirmIndividualForOrg(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ approval.Binding, _ time.Time, actor audit.Actor) (recommendation.ConfirmOutcome, error) {
	f.gotActor = actor
	return f.outcome, f.confirmErr
}
func (f *fakeApproval) ConfirmBulkSelectionForOrg(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ int32, _ time.Time, actor audit.Actor) (recommendation.BulkConfirmOutcome, error) {
	f.gotActor = actor
	return f.bulkOutcome, f.bulkErr
}
func (f *fakeApproval) EditPriceForOrg(context.Context, uuid.UUID, uuid.UUID, money.Money, time.Time) (db.ApprovalCard, error) {
	return f.editedCard, f.editErr
}
func (f *fakeApproval) ListActionsForOrg(context.Context, uuid.UUID, uuid.UUID, string, int32) ([]db.ApprovalCard, error) {
	return f.actions, f.err
}
func (f *fakeApproval) GetRecommendationForOrg(context.Context, uuid.UUID, uuid.UUID) (db.Recommendation, error) {
	return f.rec, f.recErr
}
func (f *fakeApproval) PreviewBulkSelectionForOrg(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, map[string]string, []recommendation.PreviewMemberInput) (recommendation.PreviewResult, error) {
	f.previewCalls++
	return f.preview, f.previewErr
}

func confirmBody(t *testing.T, cardID, action uuid.UUID, paramVersion int64) string {
	t.Helper()
	req := gateway.ApprovalConfirmRequest{
		CardId: cardID,
		Binding: gateway.ApprovalBinding{
			ActionId:         action,
			ParameterVersion: paramVersion,
			ExpiresAt:        time.Now().Add(time.Minute),
			EvidenceVersions: []gateway.EvidenceVersion{},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestApprovalRoutesFailClosedWhenUnwired asserts the approval routes return a
// structured 503 when no approval service is injected (fail-closed stub).
func TestApprovalRoutesFailClosedWhenUnwired(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger())
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/approvals/card?cardId=" + uuid.New().String(), ""},
		{http.MethodPost, "/approvals/confirm", confirmBody(t, uuid.New(), uuid.New(), 1)},
		{http.MethodPost, "/approvals/bulk/confirm", `{"selectionSetLineage":"` + uuid.New().String() + `","boundVersion":1}`},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d, want 503, body=%s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// TestConfirmApproval_MapsInvalidation proves the transport surfaces the §8.4
// Invalidated outcome with the exact APR-001 reason and no execution.
func TestConfirmApproval_MapsInvalidation(t *testing.T) {
	cardID := uuid.New()
	fake := &fakeApproval{outcome: recommendation.ConfirmOutcome{
		State:            approval.StateInvalidated,
		Reason:           approval.ReasonParameterChanged,
		ExecutionPending: false,
	}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(fake))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approvals/confirm", strings.NewReader(confirmBody(t, cardID, uuid.New(), 1)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out gateway.ApprovalConfirmResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.State != gateway.ApprovalState(approval.StateInvalidated) {
		t.Fatalf("state = %s; want invalidated", out.State)
	}
	if out.Reason != gateway.ApprovalInvalidationReason(approval.ReasonParameterChanged) {
		t.Fatalf("reason = %s; want parameter_version_changed", out.Reason)
	}
	if out.ExecutionPending {
		t.Fatalf("invalidated card must not report executionPending")
	}
}

// TestConfirmApproval_ApprovedIsExecutionPendingS18 proves an Approved card
// reports executionPending true — execution is the S18 boundary, not done here.
func TestConfirmApproval_ApprovedIsExecutionPendingS18(t *testing.T) {
	fake := &fakeApproval{outcome: recommendation.ConfirmOutcome{
		State:            approval.StateApproved,
		Reason:           approval.ReasonNone,
		ExecutionPending: true,
	}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(fake))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approvals/confirm", strings.NewReader(confirmBody(t, uuid.New(), uuid.New(), 1)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)

	var out gateway.ApprovalConfirmResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.State != gateway.ApprovalState(approval.StateApproved) || !out.ExecutionPending {
		t.Fatalf("approved card must report executionPending; got state=%s pending=%v", out.State, out.ExecutionPending)
	}
}

// TestConfirmApproval_ActorDerivedFromPrincipalNotBody proves the AUD-001 actor a
// confirmation records is derived from the authenticated session principal (stable
// user id, role, and the screen surface) and NEVER from the request body (issue
// #103). The request body carries only the card id and binding; there is no actor
// field to supply, and the transport passes the principal-derived actor to the
// service.
func TestConfirmApproval_ActorDerivedFromPrincipalNotBody(t *testing.T) {
	fa := newFakeAuth()
	p := principal(perm.RoleOwner)
	fa.principals["tok-owner"] = p
	fake := &fakeApproval{outcome: recommendation.ConfirmOutcome{
		State:            approval.StateApproved,
		ExecutionPending: true,
	}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithApproval(fake), WithCookieSecure(false))

	req := httptest.NewRequest(http.MethodPost, "/approvals/confirm", strings.NewReader(confirmBody(t, uuid.New(), uuid.New(), 1)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	want := audit.Actor{ID: p.UserID.String(), Role: string(perm.RoleOwner), Surface: "screen"}
	if fake.gotActor != want {
		t.Fatalf("actor = %+v; want %+v (derived from session principal, not request body)", fake.gotActor, want)
	}
}

// TestConfirmApproval_NoControlIsRejected proves a card with no structured
// control (free text / not AwaitingConfirmation) cannot approve (PRC-002, §8).
func TestConfirmApproval_NoControlIsRejected(t *testing.T) {
	fake := &fakeApproval{confirmErr: approval.ErrNoControl}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(fake))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approvals/confirm", strings.NewReader(confirmBody(t, uuid.New(), uuid.New(), 1)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("no-control confirm: status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}

// TestConfirmBulkApproval_StaleVersionInvalid proves a bulk confirmation bound to
// a stale selection-set version is rejected as invalid, authorizes nothing, and
// surfaces the current version (CHAT-052, issue #90).
func TestConfirmBulkApproval_StaleVersionInvalid(t *testing.T) {
	fake := &fakeApproval{bulkOutcome: recommendation.BulkConfirmOutcome{
		BoundVersion:     1,
		CurrentVersion:   2,
		Valid:            false,
		ExecutionPending: false,
	}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(fake))

	body := `{"selectionSetLineage":"` + uuid.New().String() + `","boundVersion":1}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approvals/bulk/confirm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)

	var out gateway.BulkApprovalConfirmResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Valid || out.ExecutionPending {
		t.Fatalf("stale bulk preview must be invalid and not execute")
	}
	if len(out.Items) != 0 {
		t.Fatalf("stale bulk result authorized %d items; want 0", len(out.Items))
	}
	if out.CurrentVersion == nil || *out.CurrentVersion != 2 {
		t.Fatalf("stale bulk result should surface the current version 2; got %v", out.CurrentVersion)
	}
}

// TestConfirmBulkApproval_MapsPerItemResults proves the transport maps the
// authoritative per-item outcomes (issue #90): a valid confirmation returns each
// member's disposition + state, and executionPending reflects a durable pending
// authorization.
func TestConfirmBulkApproval_MapsPerItemResults(t *testing.T) {
	execRec := uuid.New()
	blockedRec := uuid.New()
	fake := &fakeApproval{bulkOutcome: recommendation.BulkConfirmOutcome{
		BoundVersion:     3,
		CurrentVersion:   3,
		Valid:            true,
		ExecutionPending: true,
		Items: []recommendation.BulkItemResult{
			{VariantID: uuid.New(), RecommendationID: execRec, Disposition: recommendation.DispositionExecutable, State: recommendation.BulkItemAuthorized, Reason: "authorized"},
			{VariantID: uuid.New(), RecommendationID: blockedRec, Disposition: recommendation.DispositionBlocked, State: recommendation.BulkItemExcluded, Reason: "blocked"},
		},
	}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(fake))

	body := `{"selectionSetLineage":"` + uuid.New().String() + `","boundVersion":3}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approvals/bulk/confirm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)

	var out gateway.BulkApprovalConfirmResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Valid || !out.ExecutionPending {
		t.Fatalf("valid confirm: valid=%v pending=%v; want both true", out.Valid, out.ExecutionPending)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d; want 2", len(out.Items))
	}
	byRec := map[uuid.UUID]gateway.BulkApprovalItemResult{}
	for _, it := range out.Items {
		byRec[it.RecommendationId] = it
	}
	if got := byRec[execRec]; got.State != gateway.BulkApprovalItemStateAuthorized || got.Disposition != gateway.SelectionSetDispositionExecutable {
		t.Fatalf("exec item = %+v; want authorized/executable", got)
	}
	if got := byRec[blockedRec]; got.State != gateway.BulkApprovalItemStateExcluded || got.Disposition != gateway.SelectionSetDispositionBlocked {
		t.Fatalf("blocked item = %+v; want excluded/blocked", got)
	}
}
