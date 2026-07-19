package httpapi

import (
	"context"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// fakeExecution is an ExecutionService stub for ExecuteAction transport tests. Only
// ExecuteForOrg carries behavior; the remaining methods satisfy the interface and
// are unused by these tests.
type fakeExecution struct {
	execErr error
	execRes execution.ExecuteResult
}

func (f *fakeExecution) ExecuteForOrg(context.Context, uuid.UUID, uuid.UUID, audit.Actor) (execution.ExecuteResult, error) {
	return f.execRes, f.execErr
}

func (f *fakeExecution) RetryForOrg(context.Context, uuid.UUID, uuid.UUID, audit.Actor) (execution.RetryResult, error) {
	return execution.RetryResult{}, nil
}

func (f *fakeExecution) GetUnifiedActionForOrg(context.Context, uuid.UUID, uuid.UUID) (execution.UnifiedAction, error) {
	return execution.UnifiedAction{}, nil
}

func (f *fakeExecution) ListUnifiedByAccountForOrg(context.Context, uuid.UUID, uuid.UUID, int32) ([]execution.UnifiedAction, error) {
	return nil, nil
}

func (f *fakeExecution) ListPendingReconciliationForOrg(context.Context, uuid.UUID, uuid.UUID, int32) ([]db.ActionExecution, error) {
	return nil, nil
}

// TestExecuteActionRejectedTransitionMaps409 pins the issue #138 fix: a
// recommendation.ErrRejectedTransition surfacing from ExecuteForOrg is a
// client-caused invalid §8.4 state transition (the card is no longer in the
// FROM-guarded Approved state), so it MUST map to 409 Conflict — consistent with
// execution.ErrNotApproved — never the default 500. The body stays a controlled
// EXECUTION_ERROR envelope (free-text containment: no raw internal text beyond the
// fixed sentinel), and no write is claimed.
func TestExecuteActionRejectedTransitionMaps409(t *testing.T) {
	gs := &gatewayServer{execution: &fakeExecution{execErr: recommendation.ErrRejectedTransition}}

	resp, err := gs.ExecuteAction(context.Background(), gateway.ExecuteActionRequestObject{
		Body: &gateway.ExecuteActionRequest{CardId: uuid.New()},
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	got, ok := resp.(gateway.ExecuteActiondefaultJSONResponse)
	if !ok {
		t.Fatalf("response type = %T, want ExecuteActiondefaultJSONResponse", resp)
	}
	if got.StatusCode != 409 {
		t.Fatalf("status = %d, want 409 for ErrRejectedTransition (issue #138)", got.StatusCode)
	}
	if got.Body.Code != "EXECUTION_ERROR" {
		t.Fatalf("body code = %q, want EXECUTION_ERROR (containment envelope)", got.Body.Code)
	}
}

// TestExecuteActionConcurrentRaceLoserGets409 models the issue #138 concurrent
// race: two callers race the same Approved→Revalidating advance; the winner
// executes, the loser reads the card as Approved but loses the FROM-guarded advance
// and surfaces recommendation.ErrRejectedTransition. The loser MUST receive 409
// (an accurate "retry/refresh" signal), never a 500 that reads as a server fault.
func TestExecuteActionConcurrentRaceLoserGets409(t *testing.T) {
	winner := &fakeExecution{execRes: execution.ExecuteResult{
		ActionID: uuid.New(),
		CardID:   uuid.New(),
		Mode:     execution.ModeWrite,
		DidWrite: true,
	}}
	loser := &fakeExecution{execErr: recommendation.ErrRejectedTransition}

	cardID := uuid.New()
	req := gateway.ExecuteActionRequestObject{Body: &gateway.ExecuteActionRequest{CardId: cardID}}

	winResp, err := (&gatewayServer{execution: winner}).ExecuteAction(context.Background(), req)
	if err != nil {
		t.Fatalf("winner handler err: %v", err)
	}
	if _, ok := winResp.(gateway.ExecuteAction200JSONResponse); !ok {
		t.Fatalf("winner response = %T, want 200", winResp)
	}

	loseResp, err := (&gatewayServer{execution: loser}).ExecuteAction(context.Background(), req)
	if err != nil {
		t.Fatalf("loser handler err: %v", err)
	}
	got, ok := loseResp.(gateway.ExecuteActiondefaultJSONResponse)
	if !ok {
		t.Fatalf("loser response = %T, want ExecuteActiondefaultJSONResponse", loseResp)
	}
	if got.StatusCode != 409 {
		t.Fatalf("race loser status = %d, want 409 (issue #138 concurrent race)", got.StatusCode)
	}
}
