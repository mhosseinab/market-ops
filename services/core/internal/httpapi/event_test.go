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
	"github.com/jackc/pgx/v5/pgtype"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// fakeEvent is an EventService stub for transport tests. It records the org and
// account/event it was called with so tests can assert the handler derives scope
// from the authenticated principal, never from request input (issue #67).
type fakeEvent struct {
	open   []db.MarketEvent
	today  []event.Ranked
	detail db.MarketEvent
	rec    db.EventRelevanceFeedback
	err    error

	lastOrg            uuid.UUID
	lastAccount        uuid.UUID
	lastRelevanceUser  uuid.UUID
	lastRelevanceEvent uuid.UUID
}

func (f *fakeEvent) ListOpenForOrg(_ context.Context, org, account uuid.UUID) ([]db.MarketEvent, error) {
	f.lastOrg, f.lastAccount = org, account
	return f.open, f.err
}
func (f *fakeEvent) TodayForOrg(_ context.Context, org, account uuid.UUID) ([]event.Ranked, error) {
	f.lastOrg, f.lastAccount = org, account
	return f.today, f.err
}
func (f *fakeEvent) GetForOrg(_ context.Context, org, id uuid.UUID) (db.MarketEvent, error) {
	f.lastOrg = org
	return f.detail, f.err
}
func (f *fakeEvent) RecordRelevanceForOrg(_ context.Context, org, eventID, user uuid.UUID, _, _ string) (db.EventRelevanceFeedback, error) {
	f.lastOrg = org
	f.lastRelevanceEvent = eventID
	f.lastRelevanceUser = user
	return f.rec, f.err
}

// TestEventRoutesFailClosedWhenUnwired asserts every event route returns a
// structured 503 when no event service is injected (fail-closed stub invariant).
func TestEventRoutesFailClosedWhenUnwired(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger())
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/events?marketplaceAccountId=" + uuid.New().String(), ""},
		{http.MethodGet, "/event?eventId=" + uuid.New().String(), ""},
		{http.MethodGet, "/today?marketplaceAccountId=" + uuid.New().String(), ""},
		{http.MethodPost, "/events/relevance", `{"eventId":"` + uuid.New().String() + `","relevance":"muted"}`},
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

// knownEventRow is a market event with a KNOWN exposure.
func knownEventRow() db.MarketEvent {
	return db.MarketEvent{
		ID:                   uuid.New(),
		MarketplaceAccountID: uuid.New(),
		VariantID:            uuid.New(),
		EventType:            string(event.TypeContributionFloor),
		Severity:             string(event.SeverityWarning),
		State:                string(event.LifecycleOpen),
		ExposureKnown:        true,
		ExposureMantissa:     pgtype.Int8{Int64: 40, Valid: true},
		ExposureCurrency:     "IRR",
		ExposureExponent:     0,
		ConfidenceBp:         10000,
		UrgencyBp:            6000,
		EvidenceQuality:      string(event.QualityVerified),
		FirstDetectedAt:      time.Now().UTC(),
		LastEvidenceAt:       time.Now().UTC(),
		ExpiresAt:            time.Now().UTC().Add(time.Hour),
	}
}

// unknownEventRow is a market event with an UNKNOWN exposure (EVT-005).
func unknownEventRow() db.MarketEvent {
	return db.MarketEvent{
		ID:                   uuid.New(),
		MarketplaceAccountID: uuid.New(),
		VariantID:            uuid.New(),
		EventType:            string(event.TypeWinningState),
		Severity:             string(event.SeverityCritical),
		State:                string(event.LifecycleOpen),
		ExposureKnown:        false, // no number
		ConfidenceBp:         8000,
		UrgencyBp:            10000,
		EvidenceQuality:      string(event.QualitySupported),
		FirstDetectedAt:      time.Now().UTC(),
		LastEvidenceAt:       time.Now().UTC(),
		ExpiresAt:            time.Now().UTC().Add(time.Hour),
	}
}

// eventServer builds a server with a fake auth (middleware armed) and a fake event
// service, so transport tests exercise the real authenticated route path.
func eventServer(fa *fakeAuth, fe *fakeEvent) *http.Server {
	return NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithEvent(fe))
}

// TestListEventsMapping asserts toGatewayEvent maps the known-exposure and
// unknown-exposure rows onto the contract faithfully — including the EVT-005 case
// where an unknown exposure exposes NO amount at all. It also asserts the handler
// scopes the read to the AUTHENTICATED organization (issue #67).
func TestListEventsMapping(t *testing.T) {
	known := knownEventRow()
	unknown := unknownEventRow()
	fake := &fakeEvent{open: []db.MarketEvent{known, unknown}}
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	srv := eventServer(fa, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events?marketplaceAccountId="+known.MarketplaceAccountID.String(), nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastOrg != orgID {
		t.Fatalf("service org = %v, want authenticated org %v (never request input)", fake.lastOrg, orgID)
	}
	if fake.lastAccount != known.MarketplaceAccountID {
		t.Fatalf("service account = %v, want %v", fake.lastAccount, known.MarketplaceAccountID)
	}
	var list gateway.MarketEventList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(list.Items))
	}

	// Known-exposure item: factors and Money triple mapped through.
	k := list.Items[0]
	if k.Type != gateway.EventType(event.TypeContributionFloor) || k.Severity != "warning" {
		t.Errorf("known event core fields wrong: %+v", k)
	}
	if k.Factors.ConfidenceBp != 10000 || k.Factors.UrgencyBp != 6000 {
		t.Errorf("confidence/urgency factors not exposed: %+v", k.Factors)
	}
	if !k.Factors.Exposure.Known || k.Factors.Exposure.Amount == nil {
		t.Fatalf("known exposure must carry an amount: %+v", k.Factors.Exposure)
	}
	if k.Factors.Exposure.Amount.Mantissa != "40" || k.Factors.Exposure.Amount.Currency != "IRR" {
		t.Errorf("exposure amount mismapped: %+v", *k.Factors.Exposure.Amount)
	}

	// Unknown-exposure item (EVT-005): known=false and NO amount at all.
	u := list.Items[1]
	if u.Factors.Exposure.Known {
		t.Fatal("unknown exposure must map to known=false")
	}
	if u.Factors.Exposure.Amount != nil {
		t.Fatal("unknown exposure must expose NO amount — never a fabricated number (EVT-005)")
	}
}

// TestTodayFeedMapping asserts the ranked Today feed carries the rank and the
// three factors from each Ranked item.
func TestTodayFeedMapping(t *testing.T) {
	known := knownEventRow()
	fake := &fakeEvent{today: event.Rank([]db.MarketEvent{known})}
	fa := newFakeAuth()
	ownerSession(fa)
	srv := eventServer(fa, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/today?marketplaceAccountId="+known.MarketplaceAccountID.String(), nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var feed gateway.TodayFeed
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(feed.Items) != 1 || feed.Items[0].Rank != 1 {
		t.Fatalf("expected 1 ranked item at rank 1, got %+v", feed.Items)
	}
	if feed.Items[0].Factors.ConfidenceBp != 10000 || feed.Items[0].Factors.UrgencyBp != 6000 {
		t.Errorf("ranked factors not exposed: %+v", feed.Items[0].Factors)
	}
	if !feed.Items[0].Factors.Exposure.Known || feed.Items[0].Factors.Exposure.Amount == nil {
		t.Error("ranked known exposure must carry an amount")
	}
}

// TestGetEventNotFound asserts an unknown (or foreign) event id maps to 404 with the
// FIXED no-oracle body (distinguished from a 500 by the handler).
func TestGetEventNotFound(t *testing.T) {
	fake := &fakeEvent{err: event.ErrEventNotFound}
	fa := newFakeAuth()
	ownerSession(fa)
	srv := eventServer(fa, fake)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/event?eventId="+uuid.New().String(), nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	var env struct{ Code, Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "NOT_FOUND" || env.Message != "not found" {
		t.Fatalf("want fixed no-oracle body {NOT_FOUND, not found}, got %+v", env)
	}
}

// TestEventHandlersRequireSession proves every event route fails closed with 401
// when no human session is presented — the event service is never reached without an
// authenticated organization (issue #67).
func TestEventHandlersRequireSession(t *testing.T) {
	fake := &fakeEvent{}
	fa := newFakeAuth()
	ownerSession(fa) // a valid token exists, but requests omit it
	srv := eventServer(fa, fake)

	acct := uuid.NewString()
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/events?marketplaceAccountId=" + acct, ""},
		{http.MethodGet, "/event?eventId=" + uuid.NewString(), ""},
		{http.MethodGet, "/today?marketplaceAccountId=" + acct, ""},
		{http.MethodPost, "/events/relevance", `{"eventId":"` + uuid.NewString() + `","relevance":"muted"}`},
	}
	for _, tc := range cases {
		fake.lastOrg = uuid.Nil
		rec := httptest.NewRecorder()
		var req *http.Request
		if tc.body == "" {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		} else {
			req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
		}
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without session = %d, want 401", tc.method, tc.path, rec.Code)
		}
		if fake.lastOrg != uuid.Nil {
			t.Fatalf("%s %s reached the event service without a session", tc.method, tc.path)
		}
	}
}

// TestEventHandlersRejectForeignScope is the two-organization authz matrix (issue
// #67): a list/Today request for a foreign account and a detail/relevance request
// for a foreign event id are all REJECTED with an identical 404 no-oracle body — a
// foreign id is indistinguishable from an unknown one, so nothing leaks. The service
// (org-scoped) returns the sentinel; the handler maps it to the fixed 404.
func TestEventHandlersRejectForeignScope(t *testing.T) {
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	foreignAccount := uuid.New()
	foreignEvent := uuid.New()

	cases := []struct {
		name, method, path, body string
		svcErr                   error
	}{
		{"list-foreign-account", http.MethodGet, "/events?marketplaceAccountId=" + foreignAccount.String(), "", event.ErrAccountNotFound},
		{"today-foreign-account", http.MethodGet, "/today?marketplaceAccountId=" + foreignAccount.String(), "", event.ErrAccountNotFound},
		{"detail-foreign-event", http.MethodGet, "/event?eventId=" + foreignEvent.String(), "", event.ErrEventNotFound},
		{"relevance-foreign-event", http.MethodPost, "/events/relevance", `{"eventId":"` + foreignEvent.String() + `","relevance":"muted"}`, event.ErrEventNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEvent{err: tc.svcErr}
			srv := eventServer(fa, fake)
			rec := httptest.NewRecorder()
			var req *http.Request
			if tc.body == "" {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			} else {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
			}
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s: status = %d, want 404, body=%s", tc.name, rec.Code, rec.Body.String())
			}
			var env struct{ Code, Message string }
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if env.Code != "NOT_FOUND" || env.Message != "not found" {
				t.Fatalf("%s: want fixed no-oracle body, got %+v", tc.name, env)
			}
			// The org the service saw is always the authenticated caller's — never input.
			if fake.lastOrg != orgID {
				t.Fatalf("%s: service org = %v, want authenticated org %v", tc.name, fake.lastOrg, orgID)
			}
		})
	}
}

// TestRelevanceSourcesUserFromPrincipal proves the acting user comes from the
// authenticated principal in the request context, NEVER from the request body
// (free-text/identity containment). The handler is invoked directly with a
// principal-bearing context.
func TestRelevanceSourcesUserFromPrincipal(t *testing.T) {
	fake := &fakeEvent{}
	gs := &gatewayServer{event: fake}
	principalUser := uuid.New()
	eventID := uuid.New()

	ctx := context.WithValue(context.Background(), principalKey, auth.Principal{
		UserID: principalUser,
		Role:   perm.RoleOwner,
	})
	note := "noise"
	resp, err := gs.RecordEventRelevance(ctx, gateway.RecordEventRelevanceRequestObject{
		Body: &gateway.EventRelevanceRequest{
			EventId:   eventID,
			Relevance: gateway.EventRelevanceKind("not_relevant"),
			Note:      &note,
		},
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if _, ok := resp.(gateway.RecordEventRelevance202JSONResponse); !ok {
		t.Fatalf("expected 202 response, got %T", resp)
	}
	if fake.lastRelevanceUser != principalUser {
		t.Fatalf("acting user = %v, want the principal %v (never the body)", fake.lastRelevanceUser, principalUser)
	}
	if fake.lastRelevanceEvent != eventID {
		t.Fatalf("event id = %v, want %v", fake.lastRelevanceEvent, eventID)
	}
}
