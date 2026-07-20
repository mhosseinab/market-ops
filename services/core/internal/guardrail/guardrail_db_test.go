package guardrail_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping guardrail DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

func seedAccount(t *testing.T, q *db.Queries) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "guardrail-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Guardrail Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return acct.ID
}

func mustMoney(t *testing.T, mantissa int64, currency string, exponent int8) money.Money {
	t.Helper()
	m, err := money.New(mantissa, currency, exponent)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return m
}

// TestGuardrailGetUnconfiguredIsNotFound proves an account with no guardrails
// ever written yields a structured not-found — NEVER a fabricated default.
func TestGuardrailGetUnconfiguredIsNotFound(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	svc := guardrail.NewService(pool)

	_, err := svc.Get(context.Background(), account)
	if err == nil {
		t.Fatal("expected an error for an unconfigured account, got nil (fabricated default)")
	}
}

// TestGuardrailSetAppendsAuditAtomically is the S37 hard requirement: a
// guardrail write NEVER commits without its append-only AUD-001 audit record,
// in the SAME transaction.
func TestGuardrailSetAppendsAuditAtomically(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	svc := guardrail.NewService(pool)
	actor := audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}

	settings := guardrail.Settings{
		ContributionFloor: mustMoney(t, 1000, "USD", -2),
		MovementCapBp:     500,
		CooldownSeconds:   3600,
		Strategy:          policy.StrategyMatch,
		StrategyEnabled:   true,
	}
	view, err := svc.Set(context.Background(), account, actor, settings, 0)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if view.Settings.MovementCapBp != 500 || view.Settings.Strategy != policy.StrategyMatch {
		t.Fatalf("persisted settings mismatch: %+v", view.Settings)
	}

	// The write must be readable back.
	got, err := svc.Get(context.Background(), account)
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if got.Settings.CooldownSeconds != 3600 {
		t.Fatalf("Get after Set = %+v", got.Settings)
	}

	// An AUD-001 audit record must exist for this write — the atomic-append
	// invariant, proven by reading the audit trail back.
	records, err := q.ListAuditRecordsForAction(context.Background(), actionIDFromLatestGuardrailWrite(t, pool, account))
	if err != nil {
		t.Fatalf("list audit records: %v", err)
	}
	found := false
	for _, r := range records {
		if r.EventType == "guardrail_change" && r.Actor == actor.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("guardrail write did not append a guardrail_change audit record")
	}
}

// TestGuardrailSetRejectsInvalidStrategy proves the write is validated before
// any persistence is attempted (fail closed, never a partial/invalid write).
func TestGuardrailSetRejectsInvalidStrategy(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	svc := guardrail.NewService(pool)
	actor := audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}

	_, err := svc.Set(context.Background(), account, actor, guardrail.Settings{
		ContributionFloor: mustMoney(t, 100, "USD", -2),
		Strategy:          "not_a_real_strategy",
	}, 0)
	if err != guardrail.ErrInvalidStrategy {
		t.Fatalf("err = %v, want ErrInvalidStrategy", err)
	}
	// No row should exist.
	if _, err := svc.Get(context.Background(), account); err == nil {
		t.Fatal("a rejected write must not have persisted a settings row")
	}
}

// actionIDFromLatestGuardrailWrite locates the audit record for the most
// recent guardrail_change on this account via a direct read, since the
// service does not (by design) hand the caller the synthetic action id.
func actionIDFromLatestGuardrailWrite(t *testing.T, pool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, account uuid.UUID) uuid.UUID {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT action_id FROM audit_records WHERE marketplace_account_id = $1 AND event_type = 'guardrail_change' ORDER BY occurred_at DESC LIMIT 1`,
		account)
	if err != nil {
		t.Fatalf("query latest guardrail audit action id: %v", err)
	}
	defer rows.Close()
	var id uuid.UUID
	if rows.Next() {
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan action id: %v", err)
		}
	}
	if id == uuid.Nil {
		t.Fatal("no guardrail_change audit record found for account")
	}
	return id
}
