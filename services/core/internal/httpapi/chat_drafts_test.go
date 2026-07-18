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
	"github.com/jackc/pgx/v5"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/briefing"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

const testGatewayToken = "machine-draft-token"

// fakeDraft is a DraftService stub. It records the actor it was called with (so
// the machine-actor wiring is observable) and returns a configurable ticket/err.
type fakeDraft struct {
	ticket   recommendation.DraftTicket
	proposal recommendation.ProposalTicket
	err      error
	gotActor audit.Actor
}

func (f *fakeDraft) PrepareRecommendationDraft(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (recommendation.DraftTicket, error) {
	return f.ticket, f.err
}
func (f *fakeDraft) PrepareSelectionSetDraft(context.Context, uuid.UUID, string) (recommendation.DraftTicket, error) {
	return f.ticket, f.err
}
func (f *fakeDraft) PrepareLevel2Proposal(_ context.Context, _ uuid.UUID, actor audit.Actor, _, _, _ string) (recommendation.ProposalTicket, error) {
	f.gotActor = actor
	return f.proposal, f.err
}

// fakeBriefing is a BriefingService stub.
type fakeBriefing struct {
	b   briefing.Briefing
	err error
}

func (f *fakeBriefing) Get(context.Context, uuid.UUID, time.Time) (briefing.Briefing, error) {
	return f.b, f.err
}

// draftServer builds a server with auth armed, the gateway (machine) token set,
// and the draft/briefing services wired.
func draftServer(t *testing.T, fa *fakeAuth, fd *fakeDraft, fb *fakeBriefing) *http.Server {
	t.Helper()
	return NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithCookieSecure(false),
		WithGatewayToken(testGatewayToken),
		WithDraft(fd), WithBriefing(fb))
}

func recDraftBody(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(gateway.RecommendationDraftRequest{
		MarketplaceAccountId: uuid.New(),
		EntityId:             uuid.New(),
		RecommendationId:     uuid.New(),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func machineReq(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testGatewayToken)
	return req
}

// TestDraftRoutesFailClosedWhenUnwired: an authorized machine request to a Draft
// route with no draft service returns a structured 503 (fail-closed stub).
func TestDraftRoutesFailClosedWhenUnwired(t *testing.T) {
	fa := newFakeAuth()
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithGatewayToken(testGatewayToken))
	for _, path := range []string{
		"/chat/cards/recommendation-draft",
		"/chat/cards/selection-set-draft",
		"/chat/cards/level2-proposal",
	} {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, machineReq(http.MethodPost, path, `{"marketplace_account_id":"`+uuid.New().String()+`","query":"q","setting_key":"s","before_key":"b","after_key":"a","entity_id":"`+uuid.New().String()+`","recommendation_id":"`+uuid.New().String()+`"}`))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s unwired = %d, want 503, body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

// TestRecommendationDraft_MachineTokenAuthorized: the machine credential authorizes
// the Draft route and the handler maps the persisted ticket to the wire response
// with opaque string versions (the transport contract the LLM plane reads).
func TestRecommendationDraft_MachineTokenAuthorized(t *testing.T) {
	fa := newFakeAuth()
	draftID, actionID := uuid.New(), uuid.New()
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	fd := &fakeDraft{ticket: recommendation.DraftTicket{
		DraftID: draftID, ActionID: actionID,
		ContextVersion: 7, RecommendationVersion: 9, ParameterVersion: 3, ExpiresAt: exp,
	}}
	srv := draftServer(t, fa, fd, &fakeBriefing{})

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, machineReq(http.MethodPost, "/chat/cards/recommendation-draft", recDraftBody(t)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got gateway.RecommendationDraftResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.DraftId != draftID || got.ActionId != actionID {
		t.Fatalf("ids = %v/%v", got.DraftId, got.ActionId)
	}
	if got.ContextVersion != "7" || got.RecommendationVersion != "9" || got.ParameterVersion != "3" {
		t.Fatalf("versions = %q/%q/%q, want 7/9/3 (opaque strings)", got.ContextVersion, got.RecommendationVersion, got.ParameterVersion)
	}
	// The JSON keys MUST be snake_case (the LLM plane's transport contract).
	if !strings.Contains(rec.Body.String(), `"draft_id"`) || !strings.Contains(rec.Body.String(), `"context_version"`) {
		t.Fatalf("response is not snake_case: %s", rec.Body.String())
	}
}

// TestDraftRoutesRefusedForSessionPrincipal is the perm-refused-for-a-non-Draft
// principal test: a fully authenticated human session (Owner) carries NO Draft
// capability and presents no machine bearer, so every Draft-create route refuses
// it (401). Free text / a human surface can never mint the machine plane's Draft.
func TestDraftRoutesRefusedForSessionPrincipal(t *testing.T) {
	fa := newFakeAuth()
	fa.principals["tok-owner"] = principal(perm.RoleOwner)
	srv := draftServer(t, fa, &fakeDraft{}, &fakeBriefing{})

	for _, path := range []string{
		"/chat/cards/recommendation-draft",
		"/chat/cards/selection-set-draft",
		"/chat/cards/level2-proposal",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(recDraftBody(t)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s with Owner session = %d, want 401 (no Draft capability, machine-only route)", path, rec.Code)
		}
	}
}

// TestMachineTokenCannotApproveOrExecute proves the machine credential's envelope
// (perm.GatewayCan) denies the L4 approve/execute actions: presenting the gateway
// token on those protected routes is 403, never a passthrough. This is the
// core-side mirror of the model-registry containment invariant.
func TestMachineTokenCannotApproveOrExecute(t *testing.T) {
	fa := newFakeAuth()
	srv := draftServer(t, fa, &fakeDraft{}, &fakeBriefing{})
	cases := []struct{ path, body string }{
		{"/approvals/confirm", `{"cardId":"` + uuid.New().String() + `","binding":{"actionId":"` + uuid.New().String() + `","parameterVersion":1,"contextVersion":1,"policyVersion":1,"costProfileVersion":1,"evidenceVersions":[],"expiresAt":"2026-07-18T12:00:00Z"}}`},
		{"/approvals/bulk/confirm", `{"selectionSetLineage":"` + uuid.New().String() + `","boundVersion":1}`},
		{"/actions/execute", `{"cardId":"` + uuid.New().String() + `"}`},
		{"/actions/retry", `{"actionId":"` + uuid.New().String() + `"}`},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, machineReq(http.MethodPost, c.path, c.body))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("machine token on %s = %d, want 403 (GatewayCan denies approve/execute)", c.path, rec.Code)
		}
	}
}

// TestRecommendationDraft_FailClosedStatuses maps service errors to fail-closed
// statuses: unknown/foreign recommendation → 404, non-executable → 409.
func TestRecommendationDraft_FailClosedStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unknown", pgx.ErrNoRows, http.StatusNotFound},
		{"foreign", recommendation.ErrEntityMismatch, http.StatusNotFound},
		{"not_executable", recommendation.ErrNotExecutable, http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fa := newFakeAuth()
			srv := draftServer(t, fa, &fakeDraft{err: c.err}, &fakeBriefing{})
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, machineReq(http.MethodPost, "/chat/cards/recommendation-draft", recDraftBody(t)))
			if rec.Code != c.want {
				t.Fatalf("err %v → %d, want %d", c.err, rec.Code, c.want)
			}
		})
	}
}

// TestLevel2Proposal_UsesMachineActor proves the Level-2 handler passes the
// injected machine actor (identity, never free text) to the service and returns
// the scope/consequence keys.
func TestLevel2Proposal_UsesMachineActor(t *testing.T) {
	fa := newFakeAuth()
	fd := &fakeDraft{proposal: recommendation.ProposalTicket{
		DraftTicket:    recommendation.DraftTicket{DraftID: uuid.New(), ActionID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)},
		ScopeKey:       "scope.account",
		ConsequenceKey: "consequence.reversible",
	}}
	srv := draftServer(t, fa, fd, &fakeBriefing{})
	body := `{"marketplace_account_id":"` + uuid.New().String() + `","setting_key":"briefing.time","before_key":"v.8","after_key":"v.9"}`
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, machineReq(http.MethodPost, "/chat/cards/level2-proposal", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if fd.gotActor.Surface != machineSurface || fd.gotActor.ID == "" {
		t.Fatalf("actor = %+v, want a machine actor with surface %q", fd.gotActor, machineSurface)
	}
	var got gateway.Level2ProposalResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ScopeKey != "scope.account" || got.ConsequenceKey != "consequence.reversible" {
		t.Fatalf("scope/consequence = %q/%q", got.ScopeKey, got.ConsequenceKey)
	}
}

// TestBriefingRoute_ServesAndFailsClosed: a session read returns the stored
// briefing; a missing briefing is 404; an unwired service is 503.
func TestBriefingRoute_ServesAndFailsClosed(t *testing.T) {
	account := uuid.New()
	eventID := uuid.New()
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	fa := newFakeAuth()
	fa.principals["tok-owner"] = principal(perm.RoleOwner)
	fb := &fakeBriefing{b: briefing.Briefing{
		AccountID:   account,
		BusinessDay: day,
		GeneratedAt: time.Now().UTC(),
		Events:      []briefing.Event{{Rank: 1, EventID: eventID, EventType: "winning_state", Severity: "critical"}},
	}}
	srv := draftServer(t, fa, &fakeDraft{}, fb)

	req := httptest.NewRequest(http.MethodGet, "/briefing?marketplaceAccountId="+account.String()+"&businessDay=2026-07-18", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("briefing status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got gateway.DailyBriefing
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].EventId != eventID || got.Events[0].Rank != 1 {
		t.Fatalf("events = %+v", got.Events)
	}

	// Missing briefing → 404.
	fb.err = pgx.ErrNoRows
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/briefing?marketplaceAccountId="+account.String()+"&businessDay=2026-07-18", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing briefing = %d, want 404", rec.Code)
	}
}

// TestGatewayDraftRoutesReferenceDraftActions guards the policy table: every
// machine-only Draft route names a draft.* action the machine envelope grants,
// and that action is NOT reachable by any human role (it is not in the Matrix).
func TestGatewayDraftRoutesReferenceDraftActions(t *testing.T) {
	for _, p := range routePolicies {
		if p.kind != kindGatewayDraft {
			continue
		}
		if !perm.GatewayCan(p.action) {
			t.Errorf("gateway-draft route %s %s references %q which GatewayCan does not grant", p.method, p.path, p.action)
		}
		if !perm.IsDraftAction(p.action) {
			t.Errorf("gateway-draft route %s %s references %q which is not a Draft-only action", p.method, p.path, p.action)
		}
		for _, role := range perm.AllRoles {
			if perm.Can(role, p.action) {
				t.Errorf("draft action %q must not be reachable by human role %s", p.action, role)
			}
		}
	}
}
