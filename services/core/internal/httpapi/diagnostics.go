package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/diagnostics"
)

// DiagnosticsService is the account-scoped, READ-ONLY listing/image diagnostics
// read model the gateway depends on (S26, LST-001). *diagnostics.ReadService
// satisfies it. It is an interface so the transport can be tested with a fake and
// httpapi stays free of DB wiring.
//
// The organization id is a MANDATORY argument derived from the session principal —
// never request input — so possession of a marketplace-account UUID cannot grant
// cross-account access (fail closed). The interface exposes ONLY a read: there is
// no generate/publish/remediate method, structurally preserving LST-001.
type DiagnosticsService interface {
	GetVariantDiagnostics(ctx context.Context, organizationID, accountID, variantID uuid.UUID) (diagnostics.Report, error)
}

// ListProductDiagnostics returns the READ-ONLY listing/image diagnostics report
// for a variant (LST-001). It is org-scoped and fails closed cross-account (a
// foreign or unknown variant is 404). No response path here writes, generates, or
// publishes content.
func (s *gatewayServer) ListProductDiagnostics(
	ctx context.Context, req gateway.ListProductDiagnosticsRequestObject,
) (gateway.ListProductDiagnosticsResponseObject, error) {
	if s.diagnostics == nil {
		return gateway.ListProductDiagnosticsdefaultJSONResponse{StatusCode: 503, Body: diagnosticsUnavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.ListProductDiagnosticsdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	report, err := s.diagnostics.GetVariantDiagnostics(ctx, p.OrganizationID, req.Params.MarketplaceAccountId, req.Params.VariantId)
	if err != nil {
		return gateway.ListProductDiagnosticsdefaultJSONResponse{StatusCode: diagnosticsErrStatus(err), Body: diagnosticsErr(err)}, nil
	}
	return gateway.ListProductDiagnostics200JSONResponse(toGatewayDiagnosticsReport(report)), nil
}

func toGatewayDiagnosticsReport(r diagnostics.Report) gateway.ListingDiagnosticsReport {
	items := make([]gateway.ListingDiagnostic, 0, len(r.Items))
	for _, d := range r.Items {
		items = append(items, toGatewayDiagnostic(d))
	}
	out := gateway.ListingDiagnosticsReport{
		Items:       items,
		EvaluatedAt: r.EvaluatedAt,
	}
	if id, err := uuid.Parse(r.VariantID); err == nil {
		out.VariantId = id
	}
	if id, err := uuid.Parse(r.MarketplaceAccountID); err == nil {
		out.MarketplaceAccountId = id
	}
	return out
}

func toGatewayDiagnostic(d diagnostics.Diagnostic) gateway.ListingDiagnostic {
	out := gateway.ListingDiagnostic{
		Entity:      gateway.ListingDiagnosticEntity(d.Entity),
		Field:       gateway.ListingDiagnosticField(d.Field),
		RuleId:      d.RuleID,
		RuleVersion: d.RuleVersion,
		Result:      gateway.ListingDiagnosticResult(d.Result),
		Observed: gateway.ListingObservedMeta{
			State: gateway.ListingObservedState(d.Observed.State),
		},
		EvidenceRef: d.EvidenceRef,
		CapturedAt:  d.CapturedAt,
	}
	if d.Observed.CharacterLength != nil {
		v := int32(*d.Observed.CharacterLength)
		out.Observed.CharacterLength = &v
	}
	return out
}

// diagnosticsErrStatus maps read-model errors to HTTP status. A foreign/unknown
// account or variant is 404 (no existence oracle); anything else is 500.
func diagnosticsErrStatus(err error) int {
	switch {
	case errors.Is(err, diagnostics.ErrAccountNotFound), errors.Is(err, diagnostics.ErrVariantNotFound):
		return 404
	default:
		return 500
	}
}

func diagnosticsErr(err error) gateway.ErrorEnvelope {
	code := "DIAGNOSTICS_ERROR"
	msg := err.Error()
	switch {
	case errors.Is(err, diagnostics.ErrAccountNotFound):
		code = "NOT_FOUND"
		msg = "marketplace account not found"
	case errors.Is(err, diagnostics.ErrVariantNotFound):
		code = "NOT_FOUND"
		msg = "variant not found"
	}
	return gateway.ErrorEnvelope{Code: code, Message: msg}
}

func diagnosticsUnavailableErr() gateway.ErrorEnvelope {
	// Fail closed: an unwired diagnostics read model serves no diagnostics.
	return gateway.ErrorEnvelope{Code: "DIAGNOSTICS_UNAVAILABLE", Message: "diagnostics read model is not configured"}
}
