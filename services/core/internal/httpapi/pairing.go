package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/pairing"
)

// selfRevokeRoute is the stable route label on the self-revoke boundary's logs
// and metrics — a technical identifier, never display copy.
const selfRevokeRoute = "/ext/pairing/self-revoke"

// pairingSelfRevokeMetric is the counter name the §18 dashboards / §20.1 alerts
// query for extension kill-switch activity. Test fixtures and prod telemetry
// share this name (CLAUDE.md observability). It is repeated as a LITERAL in the
// constructor below because deploy/obs/inventory_drift_test.py parses the
// first string-literal argument of each instrument constructor — an instrument
// named only by a constant would silently escape the metric-inventory guard.
const pairingSelfRevokeMetric = "ext.pairing.self_revoke"

// pairingTelemetry is the instrument set for the extension pairing plane's
// credential-revocation boundary. A metrics-wiring hiccup degrades to a no-op
// instrument — telemetry never breaks a revocation.
type pairingTelemetry struct {
	selfRevoke metric.Int64Counter
}

// newPairingTelemetry binds the revocation counter against whatever global
// MeterProvider is installed when the server is built.
func newPairingTelemetry() *pairingTelemetry {
	c, err := otel.Meter(instrumentationName).Int64Counter(
		"ext.pairing.self_revoke", // == pairingSelfRevokeMetric (see the const)
		metric.WithDescription("extension capture-credential self-revocations, labeled by outcome (EXT-009 kill switch)"),
	)
	if err != nil {
		c, _ = noopMeter.Int64Counter(pairingSelfRevokeMetric)
	}
	return &pairingTelemetry{selfRevoke: c}
}

// PairingService is the extension-pairing seam the gateway depends on (PRD §14
// EXT-001). *pairing.Service satisfies it. The interface keeps httpapi testable
// with a fake and free of DB wiring. It also authenticates capture credentials
// for the capture-upload route (ResolveCredential), so the middleware never
// stores or shapes a credential itself.
type PairingService interface {
	MintCode(ctx context.Context, organizationID uuid.UUID) (pairing.Code, error)
	Claim(ctx context.Context, rawCode string) (pairing.Credential, error)
	ResolveCredential(ctx context.Context, rawCredential string) (pairing.Resolved, error)
	RevokeForOrganization(ctx context.Context, organizationID uuid.UUID) error
	// RevokeCredentialByID revokes EXACTLY the one credential record the
	// middleware resolved from the presented capture credential (issue #149). It
	// reports whether the record transitioned, so an already-revoked credential
	// is an unambiguous no-op rather than an error.
	RevokeCredentialByID(ctx context.Context, credentialID uuid.UUID) (bool, error)
}

// CreatePairingCode mints a short-lived, single-use pairing code for the caller's
// marketplace account (EXT-001). The raw code is returned once for display; it is
// never a seller-API token — pairing only ever yields a scoped capture credential
// downstream.
func (s *gatewayServer) CreatePairingCode(
	ctx context.Context, _ gateway.CreatePairingCodeRequestObject,
) (gateway.CreatePairingCodeResponseObject, error) {
	if s.pairing == nil {
		return gateway.CreatePairingCodedefaultJSONResponse{StatusCode: 503, Body: pairingUnavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.CreatePairingCodedefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	code, err := s.pairing.MintCode(ctx, p.OrganizationID)
	if err != nil {
		if errors.Is(err, pairing.ErrNoAccount) {
			return gateway.CreatePairingCodedefaultJSONResponse{StatusCode: 409, Body: pairingErr(err)}, nil
		}
		return gateway.CreatePairingCodedefaultJSONResponse{StatusCode: 500, Body: pairingErr(err)}, nil
	}
	return gateway.CreatePairingCode201JSONResponse{
		Code:                 code.Code,
		MarketplaceAccountId: code.MarketplaceAccountID,
		ExpiresAt:            code.ExpiresAt,
	}, nil
}

// ClaimPairingCode exchanges a pairing code for a scoped capture credential. This
// route carries NO human session — the extension is not logged in — so it is
// authenticated only by the single-use code. An unknown, expired, revoked, or
// already-claimed code fails closed with 401.
func (s *gatewayServer) ClaimPairingCode(
	ctx context.Context, req gateway.ClaimPairingCodeRequestObject,
) (gateway.ClaimPairingCodeResponseObject, error) {
	if s.pairing == nil {
		return gateway.ClaimPairingCodedefaultJSONResponse{StatusCode: 503, Body: pairingUnavailableErr()}, nil
	}
	if req.Body == nil || req.Body.Code == "" {
		return gateway.ClaimPairingCode401JSONResponse(invalidPairingCodeErr()), nil
	}
	cred, err := s.pairing.Claim(ctx, req.Body.Code)
	if err != nil {
		if errors.Is(err, pairing.ErrInvalidCode) {
			return gateway.ClaimPairingCode401JSONResponse(invalidPairingCodeErr()), nil
		}
		return gateway.ClaimPairingCodedefaultJSONResponse{StatusCode: 500, Body: pairingErr(err)}, nil
	}
	return gateway.ClaimPairingCode200JSONResponse{
		Credential:           cred.Credential,
		CredentialId:         cred.CredentialID,
		MarketplaceAccountId: cred.MarketplaceAccountID,
		ExpiresAt:            cred.ExpiresAt,
	}, nil
}

// RevokePairing revokes the capture credential(s) for the caller's marketplace
// account (EXT-001/EXT-009 kill switch). After this, the extension's next capture
// upload fails closed with 401. Idempotent.
func (s *gatewayServer) RevokePairing(
	ctx context.Context, _ gateway.RevokePairingRequestObject,
) (gateway.RevokePairingResponseObject, error) {
	if s.pairing == nil {
		return gateway.RevokePairingdefaultJSONResponse{StatusCode: 503, Body: pairingUnavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.RevokePairingdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	if err := s.pairing.RevokeForOrganization(ctx, p.OrganizationID); err != nil {
		if errors.Is(err, pairing.ErrNoAccount) {
			return gateway.RevokePairingdefaultJSONResponse{StatusCode: 409, Body: pairingErr(err)}, nil
		}
		return gateway.RevokePairingdefaultJSONResponse{StatusCode: 500, Body: pairingErr(err)}, nil
	}
	return gateway.RevokePairing204Response{}, nil
}

// SelfRevokeCapturePairing revokes the PRESENTED capture credential itself
// (issue #149 / PD-4(B), EXT-009). It is the extension's kill switch at the
// authority that verifies the credential: local deletion is cleanup, not
// revocation, so a copied credential must stop authenticating here.
//
// Identity quarantine (§4.6): the credential record acted on is read SOLELY from
// the middleware-injected identity that the presented Bearer resolved to. The
// contract declares no body/query/path parameter and this handler reads none, so
// a caller can never redirect the revocation at another device or account. It
// also never falls back to the ACCOUNT-WIDE kill switch (POST /ext/pairing/
// revoke, cookieAuth) — a self-revoke kills exactly one credential.
//
// Idempotent: a first call returns 204; any later call presents an
// already-revoked credential and is refused with 401 by the middleware, which a
// client treats as CONFIRMED revocation (documented in the contract).
func (s *gatewayServer) SelfRevokeCapturePairing(
	ctx context.Context, _ gateway.SelfRevokeCapturePairingRequestObject,
) (gateway.SelfRevokeCapturePairingResponseObject, error) {
	if s.pairing == nil {
		// Unreachable in practice (with no pairing plane the middleware cannot
		// authenticate a capture credential at all), but fail closed rather than
		// report a revocation that never happened.
		s.recordSelfRevoke(ctx, uuid.Nil, uuid.Nil, "unavailable")
		return gateway.SelfRevokeCapturePairing503JSONResponse(pairingUnavailableErr()), nil
	}
	credentialID, ok := captureCredentialFrom(ctx)
	if !ok || credentialID == uuid.Nil {
		s.recordSelfRevoke(ctx, uuid.Nil, uuid.Nil, "no_credential_identity")
		return gateway.SelfRevokeCapturePairing401JSONResponse(noSessionErr()), nil
	}
	account, _ := captureAccountFrom(ctx)

	transitioned, err := s.pairing.RevokeCredentialByID(ctx, credentialID)
	if err != nil {
		// NEVER report success on a failed revocation: the client keeps capture
		// disabled and retries against a durable pending-revocation marker.
		s.recordSelfRevoke(ctx, account, credentialID, "error")
		s.logger.WarnContext(ctx, "extension capture credential self-revoke failed",
			"route", selfRevokeRoute, "account", account.String(),
			"credential_id", credentialID.String(), "error", err.Error())
		return gateway.SelfRevokeCapturePairingdefaultJSONResponse{StatusCode: 500, Body: pairingErr(err)}, nil
	}
	outcome := "already_revoked"
	if transitioned {
		outcome = "revoked"
	}
	s.recordSelfRevoke(ctx, account, credentialID, outcome)
	return gateway.SelfRevokeCapturePairing204Response{}, nil
}

// recordSelfRevoke emits the kill-switch boundary's observability: a counter
// labeled by outcome plus a structured log with STABLE keys (CLAUDE.md — a
// kill switch engaging without an emitted, traced, audited event is a bug). The
// RAW capture credential never appears; only the record id and account do, and
// no locale copy or free text is ever a diagnostic identifier.
func (s *gatewayServer) recordSelfRevoke(ctx context.Context, account, credentialID uuid.UUID, outcome string) {
	if s.pairingTelemetry != nil {
		s.pairingTelemetry.selfRevoke.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	}
	s.logger.InfoContext(ctx, "extension capture credential self-revoke",
		"route", selfRevokeRoute, "account", account.String(),
		"credential_id", credentialID.String(), "outcome", outcome)
}

func pairingErr(err error) gateway.ErrorEnvelope {
	code := "PAIRING_ERROR"
	if errors.Is(err, pairing.ErrNoAccount) {
		code = "NO_MARKETPLACE_ACCOUNT"
	}
	return gateway.ErrorEnvelope{Code: code, Message: err.Error()}
}

func invalidPairingCodeErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "INVALID_PAIRING_CODE", Message: "unknown, expired, revoked, or already-claimed pairing code"}
}

func pairingUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "PAIRING_UNAVAILABLE", Message: "pairing service is not configured"}
}
