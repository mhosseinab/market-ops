package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/outcome"
)

// ExecutionService is the execution/reconciliation seam the gateway depends on
// (PRD §7.5 EXE-001..005). *execution.Service satisfies it. It is an interface so
// the transport can be tested with a fake and httpapi stays free of DB wiring.
type ExecutionService interface {
	Execute(ctx context.Context, cardID uuid.UUID, actor audit.Actor) (execution.ExecuteResult, error)
	Retry(ctx context.Context, actionID uuid.UUID, actor audit.Actor) (execution.RetryResult, error)
	GetExecution(ctx context.Context, actionID uuid.UUID) (db.ActionExecution, error)
	// ListPendingReconciliation backs GET /ops/queues (PD-3 item 8, S37).
	ListPendingReconciliation(ctx context.Context, account uuid.UUID, limit int32) ([]db.ActionExecution, error)
}

// OutcomeService backs GET /outcomes (OUT-001). *outcome.Service satisfies it.
type OutcomeService interface {
	Get(ctx context.Context, actionID uuid.UUID) (outcome.View, error)
	// ListByAccount backs GET /outcomes/list (PD-3 item 5, S37).
	ListByAccount(ctx context.Context, account uuid.UUID, limit int32) ([]db.ListOutcomeWindowsByAccountRow, error)
}

// ExecuteAction revalidates and executes an approved card (EXE-001/002/005). The
// actor for the audit trail comes from the authenticated principal — never from
// the request body (free-text containment).
func (s *gatewayServer) ExecuteAction(
	ctx context.Context, req gateway.ExecuteActionRequestObject,
) (gateway.ExecuteActionResponseObject, error) {
	if s.execution == nil {
		return gateway.ExecuteActiondefaultJSONResponse{StatusCode: 503, Body: executionUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.ExecuteActiondefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	res, err := s.execution.Execute(ctx, req.Body.CardId, executionActorFrom(ctx))
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return gateway.ExecuteActiondefaultJSONResponse{StatusCode: 404, Body: executionErr(err)}, nil
		case errors.Is(err, execution.ErrNotApproved):
			return gateway.ExecuteActiondefaultJSONResponse{StatusCode: 409, Body: executionErr(err)}, nil
		default:
			return gateway.ExecuteActiondefaultJSONResponse{StatusCode: 500, Body: executionErr(err)}, nil
		}
	}
	out := gateway.ExecuteActionResult{
		ActionId: res.ActionID,
		CardId:   res.CardID,
		Mode:     gateway.ExecutionMode(res.Mode),
		Blocked:  res.Blocked,
		DidWrite: res.DidWrite,
	}
	if res.Blocked {
		g := gateway.ExecutionGate(res.FailedGate)
		out.FailedGate = &g
	}
	if res.Mode == execution.ModeWrite && res.ExternalState != "" {
		es := gateway.ExecutionExternalState(res.ExternalState)
		out.ExternalState = &es
	}
	if res.Mode == execution.ModeRecommendOnly && res.RecommendOnlyState != "" {
		ro := gateway.RecommendOnlyState(res.RecommendOnlyState)
		out.RecommendOnlyState = &ro
	}
	return gateway.ExecuteAction200JSONResponse(out), nil
}

// RetryAction gates a retry (EXE-003 / CHAT-074): an unreconciled action is
// refused.
func (s *gatewayServer) RetryAction(
	ctx context.Context, req gateway.RetryActionRequestObject,
) (gateway.RetryActionResponseObject, error) {
	if s.execution == nil {
		return gateway.RetryActiondefaultJSONResponse{StatusCode: 503, Body: executionUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.RetryActiondefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	res, err := s.execution.Retry(ctx, req.Body.ActionId, executionActorFrom(ctx))
	if err != nil {
		switch {
		case errors.Is(err, execution.ErrUnreconciled):
			// Pending Reconciliation: the retry is blocked (EXE-003). 409 conflict.
			return gateway.RetryActiondefaultJSONResponse{StatusCode: 409, Body: executionErr(err)}, nil
		case errors.Is(err, execution.ErrNoExecution):
			return gateway.RetryActiondefaultJSONResponse{StatusCode: 404, Body: executionErr(err)}, nil
		case errors.Is(err, execution.ErrAlreadyTerminal):
			return gateway.RetryActiondefaultJSONResponse{StatusCode: 409, Body: executionErr(err)}, nil
		default:
			return gateway.RetryActiondefaultJSONResponse{StatusCode: 500, Body: executionErr(err)}, nil
		}
	}
	out := gateway.RetryActionResult{ActionId: res.ActionID, Eligible: res.Eligible}
	if res.State != "" {
		st := gateway.ExecutionExternalState(res.State)
		out.State = &st
	}
	return gateway.RetryAction200JSONResponse(out), nil
}

// GetActionExecution returns an action's single execution record (CHAT-073 read).
func (s *gatewayServer) GetActionExecution(
	ctx context.Context, req gateway.GetActionExecutionRequestObject,
) (gateway.GetActionExecutionResponseObject, error) {
	if s.execution == nil {
		return gateway.GetActionExecutiondefaultJSONResponse{StatusCode: 503, Body: executionUnavailableErr()}, nil
	}
	rec, err := s.execution.GetExecution(ctx, req.Params.ActionId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.GetActionExecutiondefaultJSONResponse{StatusCode: 404, Body: executionErr(err)}, nil
		}
		return gateway.GetActionExecutiondefaultJSONResponse{StatusCode: 500, Body: executionErr(err)}, nil
	}
	view := gateway.ActionExecutionView{
		ActionId:      rec.ActionID,
		CardId:        rec.CardID,
		Mode:          gateway.ExecutionMode(rec.Mode),
		ExternalState: gateway.ExecutionExternalState(rec.ExternalState),
	}
	if rec.ExternalRef != "" {
		view.ExternalRef = &rec.ExternalRef
	}
	if rec.ReconciledAt.Valid {
		t := rec.ReconciledAt.Time
		view.ReconciledAt = &t
	}
	return gateway.GetActionExecution200JSONResponse(view), nil
}

// GetOutcome returns an action's seven-day outcome window and (when closed) its
// §15.3 result (OUT-001).
func (s *gatewayServer) GetOutcome(
	ctx context.Context, req gateway.GetOutcomeRequestObject,
) (gateway.GetOutcomeResponseObject, error) {
	if s.outcome == nil {
		return gateway.GetOutcomedefaultJSONResponse{StatusCode: 503, Body: executionUnavailableErr()}, nil
	}
	view, err := s.outcome.Get(ctx, req.Params.ActionId)
	if err != nil {
		if errors.Is(err, outcome.ErrNoWindow) {
			return gateway.GetOutcomedefaultJSONResponse{StatusCode: 404, Body: executionErr(err)}, nil
		}
		return gateway.GetOutcomedefaultJSONResponse{StatusCode: 500, Body: executionErr(err)}, nil
	}
	out := gateway.OutcomeView{
		ActionId: view.Window.ActionID,
		OpenedAt: view.Window.OpenedAt,
		ClosesAt: view.Window.ClosesAt,
	}
	if view.HasResult {
		out.Result = &gateway.OutcomeResultView{
			Result:     gateway.OutcomeResultViewResult(view.Result.Result),
			Confidence: gateway.OutcomeResultViewConfidence(view.Result.Confidence),
			ComputedAt: &view.Result.ComputedAt,
		}
	}
	return gateway.GetOutcome200JSONResponse(out), nil
}

// actorFrom builds the AUD-001 actor from the authenticated principal. The surface
// is the screens API; the actor id/role are the principal's — never free text.
func executionActorFrom(ctx context.Context) audit.Actor {
	if p, ok := principalFrom(ctx); ok {
		return audit.Actor{ID: p.UserID.String(), Role: string(p.Role), Surface: "screen"}
	}
	return audit.Actor{Surface: "screen"}
}

func executionErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "EXECUTION_ERROR", Message: err.Error()}
}

func executionUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "EXECUTION_UNAVAILABLE", Message: "execution service is not configured"}
}
