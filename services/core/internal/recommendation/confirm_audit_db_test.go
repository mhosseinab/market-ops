package recommendation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// confirmationAudit is the AUD-001 evidence a confirmation must record.
type confirmationAudit struct {
	actor     string
	actorRole string
	surface   string
	actionID  uuid.UUID
	accountID uuid.UUID
	paramVer  int64
}

// countConfirmationAudits returns the number of immutable confirmation audit
// records for a card (event_type='confirmation'). Exactly one must exist per
// genuine Approved outcome, and zero for any non-approval outcome (issue #103).
func countConfirmationAudits(t *testing.T, pool *pgxpool.Pool, cardID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_records WHERE event_type = 'confirmation' AND card_id = $1`,
		cardID).Scan(&n); err != nil {
		t.Fatalf("count confirmation audits: %v", err)
	}
	return n
}

// loadConfirmationAudit returns the single confirmation record for a card.
func loadConfirmationAudit(t *testing.T, pool *pgxpool.Pool, cardID uuid.UUID) confirmationAudit {
	t.Helper()
	var got confirmationAudit
	if err := pool.QueryRow(context.Background(),
		`SELECT actor, actor_role, surface, action_id, marketplace_account_id, parameter_version
		   FROM audit_records WHERE event_type = 'confirmation' AND card_id = $1`,
		cardID).Scan(&got.actor, &got.actorRole, &got.surface, &got.actionID, &got.accountID, &got.paramVer); err != nil {
		t.Fatalf("load confirmation audit: %v", err)
	}
	return got
}

// TestConfirmIndividual_ApprovedWritesExactlyOneActorAttributedAudit is the issue
// #103 core requirement: a genuine confirmation commits Approved AND, in the SAME
// transaction, appends EXACTLY ONE confirmation audit record attributed to the
// authenticated actor, carrying the APR-001 bindings and the card's action/account.
func TestConfirmIndividual_ApprovedWritesExactlyOneActorAttributedAudit(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	card := awaitingCard(t, svc, account, variant)
	presented := bindingOf(t, card)
	actor := audit.Actor{ID: "actor-101", Role: "owner", Surface: "screen"}

	outcome, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), actor)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if outcome.State != approval.StateApproved {
		t.Fatalf("state = %s; want approved", outcome.State)
	}
	// Atomic with Approved: the card is Approved AND exactly one confirmation exists.
	reloaded, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.State != string(approval.StateApproved) {
		t.Fatalf("card state = %s; want approved", reloaded.State)
	}
	if n := countConfirmationAudits(t, pool, card.ID); n != 1 {
		t.Fatalf("confirmation audit count = %d; want exactly 1", n)
	}
	rec := loadConfirmationAudit(t, pool, card.ID)
	if rec.actor != actor.ID || rec.actorRole != actor.Role || rec.surface != actor.Surface {
		t.Fatalf("audit actor = {%q,%q,%q}; want {%q,%q,%q}",
			rec.actor, rec.actorRole, rec.surface, actor.ID, actor.Role, actor.Surface)
	}
	if rec.actionID != card.ActionID {
		t.Fatalf("audit action_id = %s; want %s", rec.actionID, card.ActionID)
	}
	if rec.accountID != account {
		t.Fatalf("audit account = %s; want %s", rec.accountID, account)
	}
	if rec.paramVer != card.ParameterVersion {
		t.Fatalf("audit parameter_version = %d; want %d", rec.paramVer, card.ParameterVersion)
	}
}

// TestConfirmIndividual_ForcedAuditFailureLeavesCardUnconfirmed proves atomicity in
// the failing direction (issue #103): when the audit append fails, the whole
// confirmation rolls back — the card stays AwaitingConfirmation and NO audit row is
// left behind. A state-changing approval can never exist without its evidence.
func TestConfirmIndividual_ForcedAuditFailureLeavesCardUnconfirmed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	boom := errors.New("audit boom (test)")
	svc.SetAuditAppendForTest(func(context.Context, *db.Queries, audit.Event) (db.AuditRecord, error) {
		return db.AuditRecord{}, boom
	})

	card := awaitingCard(t, svc, account, variant)
	presented := bindingOf(t, card)

	_, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor())
	if !errors.Is(err, boom) {
		t.Fatalf("confirm error = %v; want the forced audit failure", err)
	}
	reloaded, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.State != string(approval.StateAwaitingConfirmation) {
		t.Fatalf("card state = %s; want awaiting_confirmation (unconfirmed)", reloaded.State)
	}
	if n := countConfirmationAudits(t, pool, card.ID); n != 0 {
		t.Fatalf("confirmation audit count = %d; want 0 after rollback", n)
	}
}

// TestConfirmIndividual_ReplayDoesNotDuplicateAuditEvents proves a duplicate /
// replayed confirmation cannot create a second audit event (issue #103): the first
// confirm approves and writes one record; the replay hits the free-text/no-control
// gate (the card is no longer AwaitingConfirmation) and writes nothing.
func TestConfirmIndividual_ReplayDoesNotDuplicateAuditEvents(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	card := awaitingCard(t, svc, account, variant)
	presented := bindingOf(t, card)

	if _, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor()); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	// Replay the identical confirmation: the card is Approved (no live control), so it
	// fails closed with ErrNoControl and appends nothing.
	if _, err := svc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), testActor()); !errors.Is(err, approval.ErrNoControl) {
		t.Fatalf("replay error = %v; want approval.ErrNoControl", err)
	}
	if n := countConfirmationAudits(t, pool, card.ID); n != 1 {
		t.Fatalf("confirmation audit count = %d; want exactly 1 after replay", n)
	}
}

// TestConfirmIndividual_ExpiredRecordsNoConfirmationAudit proves a non-approval
// outcome records ONLY the required non-approval evidence — the append-only
// state-history transition — and NO confirmation event (issue #103). Confirming a
// lapsed control routes to Expired; no confirmation audit may be fabricated.
func TestConfirmIndividual_ExpiredRecordsNoConfirmationAudit(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	card := awaitingCard(t, svc, account, variant)
	presented := bindingOf(t, card)
	// now is strictly after the card's expiry: the presented binding still matches
	// every version, but the control has lapsed → Expired.
	lapsed := card.ExpiresAt.Add(time.Hour)

	outcome, err := svc.ConfirmIndividual(ctx, card.ID, presented, lapsed, testActor())
	if err != nil {
		t.Fatalf("confirm expired: %v", err)
	}
	if outcome.State != approval.StateExpired {
		t.Fatalf("state = %s; want expired", outcome.State)
	}
	if n := countConfirmationAudits(t, pool, card.ID); n != 0 {
		t.Fatalf("confirmation audit count = %d; want 0 for a non-approval outcome", n)
	}
	// The required non-approval evidence — the append-only Expired transition — is
	// present in the state history.
	hist, err := svc.History(ctx, card.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	last := hist[len(hist)-1]
	if last.ToState != string(approval.StateExpired) {
		t.Fatalf("last history state = %s; want expired", last.ToState)
	}
}
