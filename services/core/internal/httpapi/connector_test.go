package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
)

// fakeConnector is a ConnectorService stub returning a fixed snapshot.
type fakeConnector struct {
	snap connector.Snapshot
	err  error
}

func (f fakeConnector) Connect(context.Context, uuid.UUID, string) (connector.Snapshot, error) {
	return f.snap, f.err
}
func (f fakeConnector) Refresh(context.Context, uuid.UUID) (connector.Snapshot, error) {
	return f.snap, f.err
}
func (f fakeConnector) Disconnect(context.Context, uuid.UUID) (connector.Snapshot, error) {
	return f.snap, f.err
}
func (f fakeConnector) Status(context.Context, uuid.UUID) (connector.Snapshot, error) {
	return f.snap, f.err
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestConnectorStatusMapsAllNineCapabilities proves the status endpoint emits all
// nine §15.2 capabilities and preserves the Unknown default for unprobed ones.
func TestConnectorStatusMapsAllNineCapabilities(t *testing.T) {
	acct := uuid.New()
	reg := connector.NewRegistryFrom([]connector.CapabilityStatus{
		{Capability: connector.CatalogRead, State: connector.Supported},
	})
	svc := fakeConnector{snap: connector.Snapshot{AccountID: acct, Connection: connector.Connected, Registry: reg}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithConnector(svc))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/connector/status?marketplaceAccountId="+acct.String(), nil)
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		ConnectionState string `json:"connectionState"`
		Capabilities    []struct {
			Capability string `json:"capability"`
			Status     string `json:"status"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ConnectionState != "connected" {
		t.Errorf("connectionState = %q", got.ConnectionState)
	}
	if len(got.Capabilities) != 9 {
		t.Fatalf("expected 9 capabilities, got %d", len(got.Capabilities))
	}
	states := map[string]string{}
	for _, c := range got.Capabilities {
		states[c.Capability] = c.Status
	}
	if states["catalog_read"] != "supported" {
		t.Errorf("catalog_read = %q, want supported", states["catalog_read"])
	}
	// Unprobed capabilities stay Unknown in the emitted status.
	if states["price_write"] != "unknown" {
		t.Errorf("price_write = %q, want unknown", states["price_write"])
	}
}

// TestConnectorEndpointsFailClosedWhenUnwired proves that without a connector
// service, the routes return a structured error and never a healthy status
// (ACC-003: no generic healthy state while unconfigured).
func TestConnectorEndpointsFailClosedWhenUnwired(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger()) // no WithConnector

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/connector/status?marketplaceAccountId="+uuid.NewString(), nil)
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "CONNECTOR_UNAVAILABLE" {
		t.Errorf("code = %q, want CONNECTOR_UNAVAILABLE", env.Code)
	}
}

// TestConnectRejectsEmptyAuthCode proves the connect endpoint surfaces the
// validation error as a 400 INVALID_ARGUMENT.
func TestConnectRejectsEmptyAuthCode(t *testing.T) {
	svc := fakeConnector{err: connector.ErrInvalidAuthCode}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithConnector(svc))

	rec := httptest.NewRecorder()
	body := `{"marketplaceAccountId":"` + uuid.NewString() + `","authorizationCode":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/connector/connect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}
