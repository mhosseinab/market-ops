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
	"github.com/jackc/pgx/v5/pgtype"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// fakeEvent is an EventService stub for transport tests.
type fakeEvent struct {
	open   []db.MarketEvent
	today  []event.Ranked
	detail db.MarketEvent
	rec    db.EventRelevanceFeedback
	err    error

	lastRelevanceUser  uuid.UUID
	lastRelevanceEvent uuid.UUID
}

func (f *fakeEvent) ListOpen(context.Context, uuid.UUID) ([]db.MarketEvent, error) {
	return f.open, f.err
}
func (f *fakeEvent) Today(context.Context, uuid.UUID) ([]event.Ranked, error) {
	return f.today, f.err
}
func (f *fakeEvent) Get(context.Context, uuid.UUID) (db.MarketEvent, error) {
	return f.detail, f.err
}
func (f *fakeEvent) RecordRelevance(_ context.Context, eventID, user uuid.UUID, _, _ string) (db.EventRelevanceFeedback, error) {
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

// TestListEventsMapping asserts toGatewayEvent maps the known-exposure and
// unknown-exposure rows onto the contract faithfully — including the EVT-005 case
// where an unknown exposure exposes NO amount at all.
func TestListEventsMapping(t *testing.T) {
	known := knownEventRow()
	unknown := unknownEventRow()
	fake := &fakeEvent{open: []db.MarketEvent{known, unknown}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithEvent(fake))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events?marketplaceAccountId="+known.MarketplaceAccountID.String(), nil)
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
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
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithEvent(fake))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/today?marketplaceAccountId="+known.MarketplaceAccountID.String(), nil)
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

// TestGetEventNotFound asserts an unknown event id maps to 404 (distinguished
// from a 500 by the handler).
func TestGetEventNotFound(t *testing.T) {
	fake := &fakeEvent{err: pgx.ErrNoRows}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithEvent(fake))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/event?eventId="+uuid.New().String(), nil)
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
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
