// L3 commercial guardrails read + write (PD-3 item 6). The write is Owner-only
// and appends its AUD-001 audit record atomically with the mutation.
package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// GuardrailService backs the /guardrails routes (PD-3 item 6).
// *guardrail.Service satisfies it.
//
// Both methods take the authenticated organization id (issue #237, mirroring
// issue #102) so the service resolves the caller's OWN marketplace account and
// predicates the read/write on it. The requested account is validated against the
// resolved one, never trusted from the request body; a foreign or org-less caller
// is a uniform not-found (guardrail.ErrAccountNotFound) with no disclosure and — for
// SetForOrg, a money/policy write — no mutation and no audit row.
type GuardrailService interface {
	GetForOrg(ctx context.Context, organizationID, account uuid.UUID) (guardrail.ConfigView, error)
	SetForOrg(ctx context.Context, organizationID, account uuid.UUID, actor audit.Actor, settings guardrail.Settings, expectedVersion int64) (guardrail.ConfigView, error)
}

// GetGuardrails reads an account's L3 commercial guardrails (PD-3 item 6). L1
// read, every role; absent (never configured) is a structured 404.
func (s *gatewayServer) GetGuardrails(
	ctx context.Context, req gateway.GetGuardrailsRequestObject,
) (gateway.GetGuardrailsResponseObject, error) {
	if s.guardrail == nil {
		return gateway.GetGuardrailsdefaultJSONResponse{StatusCode: 503, Body: guardrailUnavailableErr()}, nil
	}
	// Tenant scoping (issue #237): the account is resolved from the authenticated
	// org, and the requested id is validated against it — never trusted from the
	// query param. A foreign or org-less caller is the SAME uniform 404 as a genuinely
	// unconfigured account (no existence oracle).
	view, err := s.guardrail.GetForOrg(ctx, orgFromCtx(ctx), req.Params.MarketplaceAccountId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, guardrail.ErrAccountNotFound) {
			return gateway.GetGuardrailsdefaultJSONResponse{StatusCode: 404, Body: guardrailErr(err)}, nil
		}
		return gateway.GetGuardrailsdefaultJSONResponse{StatusCode: 500, Body: guardrailErr(err)}, nil
	}
	return gateway.GetGuardrails200JSONResponse(toGuardrailConfigView(view)), nil
}

// SetGuardrails writes an account's L3 commercial guardrails, Owner ONLY
// (perm.ActionWriteGuardrails; never reachable by the machine gateway
// credential, §12.3). Every write appends an AUD-001 audit record ATOMICALLY
// with the mutation (internal/guardrail.Service.Set, same transaction).
func (s *gatewayServer) SetGuardrails(
	ctx context.Context, req gateway.SetGuardrailsRequestObject,
) (gateway.SetGuardrailsResponseObject, error) {
	if s.guardrail == nil {
		return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 503, Body: guardrailUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	floor, err := moneyFromGateway(req.Body.Settings.ContributionFloor)
	if err != nil {
		return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr(err.Error())}, nil
	}
	settings := guardrail.Settings{
		ContributionFloor: floor,
		MovementCapBp:     req.Body.Settings.MovementCapBasisPoints,
		CooldownSeconds:   req.Body.Settings.CooldownSeconds,
		Strategy:          policy.Strategy(req.Body.Settings.Strategy),
		StrategyEnabled:   req.Body.Settings.StrategyEnabled,
	}
	// Optimistic concurrency (issue #101): the caller echoes the version it last
	// read (GuardrailConfigView.version). Absent ⇒ 0 ⇒ "first write" — which fails
	// closed for an already-configured account (its version is ≥ 1), forcing a
	// read-before-write. Never inferred from anything but the explicit field.
	var expectedVersion int64
	if req.Body.ExpectedVersion != nil {
		expectedVersion = *req.Body.ExpectedVersion
	}
	// Tenant scoping (issue #237): SetForOrg resolves the caller's OWN account from
	// the authenticated org and enforces ownership BEFORE any state change or audit
	// append. A foreign or org-less caller — including one supplying another tenant's
	// account id in the body — is a uniform 404 with NO guardrail mutation and NO
	// audit row for the foreign account (a money/policy write is the highest-severity
	// tenant-integrity surface, §4.6).
	view, err := s.guardrail.SetForOrg(ctx, orgFromCtx(ctx), req.Body.MarketplaceAccountId, actorFromPrincipal(ctx, "screens"), settings, expectedVersion)
	if err != nil {
		switch {
		case errors.Is(err, guardrail.ErrAccountNotFound):
			return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 404, Body: guardrailErr(err)}, nil
		case errors.Is(err, guardrail.ErrInvalidStrategy):
			return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 400, Body: guardrailErr(err)}, nil
		case errors.Is(err, guardrail.ErrNotStricter):
			// A loosening is a legitimate request the stricter-only gate (PRC-004 /
			// §8.3) declined — a structured 400, nothing persisted (fail closed).
			return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 400, Body: guardrailNotStricterErr(err)}, nil
		case errors.Is(err, guardrail.ErrVersionConflict):
			// A stale-version write: a SAFE 409 conflict, never a lost update. The
			// caller must reload the current values and retry (issue #101).
			return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 409, Body: guardrailConflictErr(err)}, nil
		default:
			return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 500, Body: guardrailErr(err)}, nil
		}
	}
	return gateway.SetGuardrails200JSONResponse(toGuardrailConfigView(view)), nil
}

func toGuardrailConfigView(v guardrail.ConfigView) gateway.GuardrailConfigView {
	out := gateway.GuardrailConfigView{
		MarketplaceAccountId: v.AccountID,
		Settings: gateway.GuardrailSettings{
			ContributionFloor:      toMoneyAmount(v.Settings.ContributionFloor),
			MovementCapBasisPoints: v.Settings.MovementCapBp,
			CooldownSeconds:        v.Settings.CooldownSeconds,
			Strategy:               gateway.PolicyStrategy(v.Settings.Strategy),
			StrategyEnabled:        v.Settings.StrategyEnabled,
		},
		Version:   v.Version,
		UpdatedAt: v.UpdatedAt,
	}
	if v.UpdatedBy != "" {
		u := v.UpdatedBy
		out.UpdatedBy = &u
	}
	return out
}

func guardrailErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "GUARDRAIL_ERROR", Message: err.Error()}
}

func guardrailNotStricterErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "GUARDRAIL_NOT_STRICTER", Message: err.Error()}
}

func guardrailConflictErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "GUARDRAIL_VERSION_CONFLICT", Message: err.Error()}
}

func guardrailUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "GUARDRAIL_UNAVAILABLE", Message: "guardrail service is not configured"}
}
