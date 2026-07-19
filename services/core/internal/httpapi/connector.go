package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
)

// ConnectorService is the connector orchestration the gateway depends on
// (ACC-001). *connector.Service satisfies it. Keeping it an interface lets the
// transport be tested with a fake and keeps httpapi free of DB wiring.
//
// Every method takes the authenticated organization id as a MANDATORY first
// argument (S8-AUTHZ-001). The handlers derive it from the session principal —
// never from request input — so possession of a marketplace-account UUID cannot
// grant cross-organization access.
type ConnectorService interface {
	Connect(ctx context.Context, organizationID, accountID uuid.UUID, authCode string) (connector.Snapshot, error)
	Refresh(ctx context.Context, organizationID, accountID uuid.UUID) (connector.Snapshot, error)
	Disconnect(ctx context.Context, organizationID, accountID uuid.UUID) (connector.Snapshot, error)
	Status(ctx context.Context, organizationID, accountID uuid.UUID) (connector.Snapshot, error)
	SyncCatalog(ctx context.Context, organizationID, accountID uuid.UUID) (connector.Snapshot, error)
}

// ConnectConnector exchanges an auth code and returns the reconciled status.
func (s *gatewayServer) ConnectConnector(
	ctx context.Context, req gateway.ConnectConnectorRequestObject,
) (gateway.ConnectConnectorResponseObject, error) {
	if s.connector == nil {
		return gateway.ConnectConnectordefaultJSONResponse{StatusCode: 503, Body: unavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.ConnectConnectordefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.ConnectConnectordefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	// Organization scope comes ONLY from the authenticated principal, never from
	// request input (S8-AUTHZ-001). A foreign account UUID is rejected by the
	// service with no side effect.
	snap, err := s.connector.Connect(ctx, p.OrganizationID, req.Body.MarketplaceAccountId, req.Body.AuthorizationCode)
	if err != nil {
		return gateway.ConnectConnectordefaultJSONResponse{StatusCode: connectorErrStatus(err), Body: connectorErr(err)}, nil
	}
	return gateway.ConnectConnector200JSONResponse(toGatewayStatus(snap)), nil
}

// RefreshConnector rotates the token and re-probes.
func (s *gatewayServer) RefreshConnector(
	ctx context.Context, req gateway.RefreshConnectorRequestObject,
) (gateway.RefreshConnectorResponseObject, error) {
	if s.connector == nil {
		return gateway.RefreshConnectordefaultJSONResponse{StatusCode: 503, Body: unavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.RefreshConnectordefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.RefreshConnectordefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	snap, err := s.connector.Refresh(ctx, p.OrganizationID, req.Body.MarketplaceAccountId)
	if err != nil {
		return gateway.RefreshConnectordefaultJSONResponse{StatusCode: connectorErrStatus(err), Body: connectorErr(err)}, nil
	}
	return gateway.RefreshConnector200JSONResponse(toGatewayStatus(snap)), nil
}

// DisconnectConnector severs the connection and resets capabilities to Unknown.
func (s *gatewayServer) DisconnectConnector(
	ctx context.Context, req gateway.DisconnectConnectorRequestObject,
) (gateway.DisconnectConnectorResponseObject, error) {
	if s.connector == nil {
		return gateway.DisconnectConnectordefaultJSONResponse{StatusCode: 503, Body: unavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.DisconnectConnectordefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.DisconnectConnectordefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	snap, err := s.connector.Disconnect(ctx, p.OrganizationID, req.Body.MarketplaceAccountId)
	if err != nil {
		return gateway.DisconnectConnectordefaultJSONResponse{StatusCode: connectorErrStatus(err), Body: connectorErr(err)}, nil
	}
	return gateway.DisconnectConnector200JSONResponse(toGatewayStatus(snap)), nil
}

// GetConnectorStatus returns the current connection + capability status.
func (s *gatewayServer) GetConnectorStatus(
	ctx context.Context, req gateway.GetConnectorStatusRequestObject,
) (gateway.GetConnectorStatusResponseObject, error) {
	if s.connector == nil {
		return gateway.GetConnectorStatusdefaultJSONResponse{StatusCode: 503, Body: unavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.GetConnectorStatusdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	snap, err := s.connector.Status(ctx, p.OrganizationID, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.GetConnectorStatusdefaultJSONResponse{StatusCode: connectorErrStatus(err), Body: connectorErr(err)}, nil
	}
	return gateway.GetConnectorStatus200JSONResponse(toGatewayStatus(snap)), nil
}

// SyncCatalog initiates an idempotent catalog sync and returns the reconciled
// status (ACC-004/ACC-005). Capability gating and idempotency live in the
// service; the transport only maps the outcome.
func (s *gatewayServer) SyncCatalog(
	ctx context.Context, req gateway.SyncCatalogRequestObject,
) (gateway.SyncCatalogResponseObject, error) {
	if s.connector == nil {
		return gateway.SyncCatalogdefaultJSONResponse{StatusCode: 503, Body: unavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.SyncCatalogdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.SyncCatalogdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	snap, err := s.connector.SyncCatalog(ctx, p.OrganizationID, req.Body.MarketplaceAccountId)
	if err != nil {
		return gateway.SyncCatalogdefaultJSONResponse{StatusCode: connectorErrStatus(err), Body: connectorErr(err)}, nil
	}
	return gateway.SyncCatalog200JSONResponse(toGatewayStatus(snap)), nil
}

// toGatewayStatus maps a connector.Snapshot onto the generated ConnectorStatus.
// It always emits all nine capabilities in fixed order (ACC-001).
func toGatewayStatus(snap connector.Snapshot) gateway.ConnectorStatus {
	caps := make([]gateway.CapabilityStatus, 0, len(connector.AllCapabilities()))
	for _, st := range snap.Registry.List() {
		cs := gateway.CapabilityStatus{
			Capability: gateway.ConnectorCapability(st.Capability),
			Status:     gateway.ConnectorCapabilityState(st.State),
		}
		if st.LastVerified != nil {
			t := *st.LastVerified
			cs.LastVerified = &t
		}
		if st.Detail != "" {
			d := st.Detail
			cs.Detail = &d
		}
		caps = append(caps, cs)
	}
	status := gateway.ConnectorStatus{
		MarketplaceAccountId: snap.AccountID,
		ConnectionState:      gateway.ConnectorConnectionState(snap.Connection),
		Capabilities:         caps,
	}
	if snap.CatalogSync != nil {
		cs := gateway.CatalogSyncStatus{
			State: gateway.CatalogSyncState(snap.CatalogSync.State),
		}
		if snap.CatalogSync.LastRunAt != nil {
			t := *snap.CatalogSync.LastRunAt
			cs.LastRunAt = &t
		}
		if snap.CatalogSync.Detail != "" {
			d := snap.CatalogSync.Detail
			cs.Detail = &d
		}
		status.CatalogSync = &cs
	}
	return status
}

func connectorErrStatus(err error) int {
	switch {
	case errors.Is(err, connector.ErrInvalidAuthCode):
		return 400
	// A foreign OR unknown account id is reported identically as 404 so the
	// response never reveals whether a cross-organization account exists
	// (S8-AUTHZ-001).
	case errors.Is(err, connector.ErrAccountNotFound):
		return 404
	case errors.Is(err, connector.ErrNotConnected):
		return 409
	// catalog_read not Supported: the dependent operation is refused (fail
	// closed, §15.2). 409 Conflict — the connection exists but its current
	// capability state forbids the operation.
	case errors.Is(err, connector.ErrCapabilityNotSupported):
		return 409
	// No sync enqueuer wired: the service could not initiate a sync. 503 — a
	// transient wiring/availability condition, never a healthy "queued".
	case errors.Is(err, connector.ErrSyncUnavailable):
		return 503
	default:
		return 502
	}
}

func connectorErr(err error) gateway.ErrorEnvelope {
	code := "CONNECTOR_ERROR"
	msg := err.Error()
	switch {
	case errors.Is(err, connector.ErrInvalidAuthCode):
		code = "INVALID_ARGUMENT"
	case errors.Is(err, connector.ErrAccountNotFound):
		// Fixed code + message: a foreign account and an unknown account are
		// indistinguishable to the caller (no existence oracle).
		code = "NOT_FOUND"
		msg = "marketplace account not found"
	case errors.Is(err, connector.ErrNotConnected):
		code = "NOT_CONNECTED"
	case errors.Is(err, connector.ErrCapabilityNotSupported):
		code = "CAPABILITY_NOT_SUPPORTED"
	case errors.Is(err, connector.ErrSyncUnavailable):
		code = "SYNC_UNAVAILABLE"
	}
	return gateway.ErrorEnvelope{Code: code, Message: msg}
}

func invalidArgErr(msg string) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "INVALID_ARGUMENT", Message: msg}
}

func unavailableErr() gateway.ErrorEnvelope {
	// Fail closed: an unwired connector never reports a healthy connection.
	return gateway.ErrorEnvelope{Code: "CONNECTOR_UNAVAILABLE", Message: "connector service is not configured"}
}
