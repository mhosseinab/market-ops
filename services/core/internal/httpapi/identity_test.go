package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
)

// fakeIdentity is an IdentityService stub for transport tests.
type fakeIdentity struct {
	queue       []identity.QueueItem
	mapping     db.MarketProductIdentity
	err         error
	scopeErr    error
	lastActor   identity.Actor
	lastNote    string
	lastID      uuid.UUID
	lastOrg     uuid.UUID
	lastAccount uuid.UUID
}

func (f *fakeIdentity) NeedsReviewQueue(context.Context, uuid.UUID) ([]identity.QueueItem, error) {
	return f.queue, f.err
}
func (f *fakeIdentity) NeedsReviewQueueForOrg(_ context.Context, org, account uuid.UUID) ([]identity.QueueItem, error) {
	f.lastOrg, f.lastAccount = org, account
	if f.scopeErr != nil {
		return nil, f.scopeErr
	}
	return f.queue, f.err
}
func (f *fakeIdentity) Confirm(_ context.Context, id uuid.UUID, actor identity.Actor) (db.MarketProductIdentity, error) {
	f.lastID, f.lastActor = id, actor
	return f.mapping, f.err
}
func (f *fakeIdentity) Reject(_ context.Context, id uuid.UUID, actor identity.Actor, note string) (db.MarketProductIdentity, error) {
	f.lastID, f.lastActor, f.lastNote = id, actor, note
	return f.mapping, f.err
}
func (f *fakeIdentity) Defer(_ context.Context, id uuid.UUID, actor identity.Actor, note string) (db.MarketProductIdentity, error) {
	f.lastID, f.lastActor, f.lastNote = id, actor, note
	return f.mapping, f.err
}

// TestIdentityRoutesFailClosedWhenUnwired asserts every /identity route returns a
// structured 503 when no identity service is injected — Unknown never enables a
// dependent surface.
func TestIdentityRoutesFailClosedWhenUnwired(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger())
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/identity/needs-review?marketplaceAccountId=" + uuid.New().String(), ""},
		{http.MethodPost, "/identity/confirm", `{"identityId":"` + uuid.New().String() + `"}`},
		{http.MethodPost, "/identity/reject", `{"identityId":"` + uuid.New().String() + `"}`},
		{http.MethodPost, "/identity/defer", `{"identityId":"` + uuid.New().String() + `"}`},
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

// TestNeedsReviewQueueRoundTrips asserts the queue endpoint maps the read model
// onto the contract shape, including the SKU / titles / native-id evidence.
func TestNeedsReviewQueueRoundTrips(t *testing.T) {
	fake := &fakeIdentity{queue: []identity.QueueItem{{
		IdentityID:      uuid.New(),
		VariantID:       uuid.New(),
		NativeVariantID: 42,
		NativeProductID: 7,
		SupplierCode:    "SKU-1",
		VariantTitle:    "Red",
		ProductTitle:    "Widget",
		CandidateSource: "exact_native_id",
		Version:         1,
	}}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithIdentity(fake))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/identity/needs-review?marketplaceAccountId="+uuid.New().String(), nil)
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []struct {
			SupplierCode    string `json:"supplierCode"`
			ProductTitle    string `json:"productTitle"`
			NativeProductId int64  `json:"nativeProductId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].SupplierCode != "SKU-1" || got.Items[0].NativeProductId != 7 {
		t.Fatalf("unexpected queue payload: %s", rec.Body.String())
	}
}

// TestConfirmErrorStatusMapping pins the identity confirm error → HTTP contract
// (issue #35). An ownership conflict — whether detected by the pre-write check or
// by the partial-unique-index race — is a deterministic 409 with the stable
// IDENTITY_CONFLICT machine code, NOT a 500. Not-found stays 404, not-pending
// stays 409/NOT_PENDING, and any other (infrastructure) failure stays 500 so a
// real fault is never masked as a client conflict.
func TestConfirmErrorStatusMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantMach string
	}{
		{"ownership conflict", identity.ErrIdentityConflict, http.StatusConflict, "IDENTITY_CONFLICT"},
		{"not pending", identity.ErrNotPending, http.StatusConflict, "NOT_PENDING"},
		{"not found", identity.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"internal failure", errors.New("connection reset"), http.StatusInternalServerError, "IDENTITY_ERROR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := uuid.New()
			fake := &fakeIdentity{err: c.err}
			srv := NewServer(":0", BuildInfo{}, testLogger(), WithIdentity(fake))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/identity/confirm", strings.NewReader(`{"identityId":"`+id.String()+`"}`))
			req.Header.Set("Content-Type", "application/json")
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != c.wantCode {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, c.wantCode, rec.Body.String())
			}
			var got struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Code != c.wantMach {
				t.Fatalf("machine code = %q, want %q", got.Code, c.wantMach)
			}
		})
	}
}

// TestConfirmRoutesToService asserts the confirm endpoint decodes the body and
// returns the mapping shape.
func TestConfirmRoutesToService(t *testing.T) {
	id := uuid.New()
	fake := &fakeIdentity{mapping: db.MarketProductIdentity{
		ID:              id,
		State:           string(identity.StateConfirmed),
		Active:          true,
		CandidateSource: "exact_native_id",
		Version:         2,
	}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithIdentity(fake))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/identity/confirm", strings.NewReader(`{"identityId":"`+id.String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastID != id {
		t.Fatalf("service received id %s, want %s", fake.lastID, id)
	}
	var got struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != "confirmed" {
		t.Fatalf("state = %q, want confirmed", got.State)
	}
}
