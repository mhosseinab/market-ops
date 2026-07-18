package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/pairing"
)

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
