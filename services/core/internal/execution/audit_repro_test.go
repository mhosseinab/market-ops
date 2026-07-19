package execution

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// TestAudit_ReproducibleWithoutConversation is the AUD-001 / CHAT-008 never-cut
// proof: a completed action's audit reproduces from the append-only records alone
// AFTER every conversation row is deleted. The trail is transcript-independent.
func TestAudit_ReproducibleWithoutConversation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, _ := countingMockDK(t)

	// Seed a conversation + message that reference the account's org (a chat the
	// action might have been prepared in). It carries NO action state.
	var orgID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT organization_id FROM marketplace_accounts WHERE id = $1`, card.MarketplaceAccountID).Scan(&orgID); err != nil {
		t.Fatalf("org of account: %v", err)
	}
	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (organization_id, email, role) VALUES ($1,$2,'owner') RETURNING id`,
		orgID, "u-"+uuid.NewString()+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var convID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO conversations (organization_id, opened_by_user_id) VALUES ($1,$2) RETURNING id`,
		orgID, userID).Scan(&convID); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO conversation_messages (conversation_id, author, body) VALUES ($1,'user','approve it')`,
		convID); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	// Run the full write lifecycle, producing the append-only audit trail.
	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, recommendation.NewService(pool), writer, fakeResolver{ctx: enabledContext(card, native)})
	if _, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Delete the ENTIRE conversation surface.
	if _, err := pool.Exec(ctx, `DELETE FROM conversation_messages`); err != nil {
		t.Fatalf("delete messages: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM conversations`); err != nil {
		t.Fatalf("delete conversations: %v", err)
	}

	// Reproduce the action from the audit trail alone.
	repro, err := audit.Reproduce(ctx, db.New(pool), card.ActionID)
	if err != nil {
		t.Fatalf("reproduce: %v", err)
	}
	if len(repro.Records) == 0 {
		t.Fatalf("no audit records after conversation deletion (audit not transcript-independent)")
	}
	if !repro.HasTerminal() {
		t.Fatalf("reproduction missing terminal record")
	}
	// The AUD-001 fields must be present on the execution-started record.
	var sawStart, sawResult bool
	for _, r := range repro.Records {
		switch r.EventType {
		case string(audit.EventExecutionStarted):
			sawStart = true
			if r.ActionID != card.ActionID || r.ParameterVersion != card.ParameterVersion {
				t.Fatalf("execution_started record missing bound versions")
			}
		case string(audit.EventExternalResult):
			sawResult = true
			if r.TerminalState == "" {
				t.Fatalf("external_result record missing terminal state")
			}
		}
	}
	if !sawStart || !sawResult {
		t.Fatalf("reproduction missing execution_started (%v) or external_result (%v)", sawStart, sawResult)
	}

	// The append-only §8.4 card state history is also intact and independent.
	hist, err := recommendation.NewService(pool).History(ctx, card.ID)
	if err != nil {
		t.Fatalf("card history: %v", err)
	}
	if got := hist[len(hist)-1].ToState; got != string(approval.StateAccepted) {
		t.Fatalf("final card state = %q; want accepted", got)
	}
}

// TestExecute_GateBlockedWritePreventsWrite proves the EXE-001 write path honors
// the gate matrix end-to-end: an injected cost-version change blocks the write,
// invalidates the card, and records the block — with ZERO external writes.
func TestExecute_GateBlockedWritePreventsWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	rc := enabledContext(card, native)
	// Inject a server-resolved cost-profile version that diverges from the bound
	// version: the costs gate must block the write.
	boundBinding, err := bindingOf(card)
	if err != nil {
		t.Fatalf("bindingOf: %v", err)
	}
	rc.Inputs.Bound = boundBinding
	rc.Inputs.Current = boundBinding
	rc.Inputs.Current.CostProfileVersion = 999

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, recommendation.NewService(pool), writer, fakeResolver{ctx: rc})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Blocked || res.FailedGate != GateCosts {
		t.Fatalf("gate block: blocked=%v gate=%q; want blocked at costs", res.Blocked, res.FailedGate)
	}
	if got := atomic.LoadInt32(writes); got != 0 {
		t.Fatalf("gate-blocked write performed %d external writes; want 0", got)
	}
	after, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if after.State != string(approval.StateInvalidated) {
		t.Fatalf("gate-blocked card state = %q; want invalidated", after.State)
	}
}
