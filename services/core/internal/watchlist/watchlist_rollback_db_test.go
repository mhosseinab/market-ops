package watchlist_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/watchlist"
)

// TestWatchlistAddRollsBackInsertWhenAuditAppendFails exercises the ROLLBACK
// branch of the mutation+audit atomic seam: a failure injected BETWEEN the
// watchlist insert and the AUD-001 audit append (via a DB-level trigger that
// raises on the audit insert, scoped to this test's account — the service opens
// its own pool transaction, so there is no Go-level seam to inject at without a
// production fault hook this code deliberately does not have) must roll the
// WHOLE transaction back. It asserts BOTH that the watchlist entry is absent AND
// that no audit row was appended — the add is never persisted without its
// reproducible audit trail, and never persisted alone.
func TestWatchlistAddRollsBackInsertWhenAuditAppendFails(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	variant := seedConfirmedVariant(t, q, account)
	svc := watchlist.NewService(pool)
	actor := audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}

	installAuditInsertFault(t, pool, "watchlist_rollback", account)

	if _, err := svc.Add(context.Background(), account, variant, actor); err == nil {
		t.Fatal("expected Add to fail when the audit append errors mid-transaction, got nil")
	}

	// The watchlist entry must NOT be persisted: the whole tx rolled back.
	entries, err := svc.List(context.Background(), account)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("watchlist entry persisted despite audit-append failure — rollback branch is broken, got %d", len(entries))
	}

	// And no audit row may exist either — the whole tx rolled back atomically.
	assertNoAuditRowsForAccount(t, pool, account)
}

// installAuditInsertFault installs a BEFORE INSERT trigger on audit_records that
// raises an exception whenever a row is inserted for the given account, then
// registers cleanup to drop it. Scoping to the test's account keeps the fault
// from touching any other row or test. This is the DB-gated fault-injection
// approach (spec #135): no production fault hook is added.
func installAuditInsertFault(t *testing.T, pool *pgxpool.Pool, name string, account uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	fn := name + "_fault_fn"
	trg := name + "_fault_trg"

	stmt := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION %s() RETURNS trigger AS $fn$
BEGIN
	IF NEW.marketplace_account_id = '%s'::uuid THEN
		RAISE EXCEPTION 'injected audit-insert fault for rollback test';
	END IF;
	RETURN NEW;
END;
$fn$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS %s ON audit_records;
CREATE TRIGGER %s BEFORE INSERT ON audit_records
	FOR EACH ROW EXECUTE FUNCTION %s();`, fn, account, trg, trg, fn)

	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Fatalf("install audit-insert fault: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON audit_records; DROP FUNCTION IF EXISTS %s();", trg, fn))
	})
}

// assertNoAuditRowsForAccount fails if any audit record exists for the account.
func assertNoAuditRowsForAccount(t *testing.T, pool *pgxpool.Pool, account uuid.UUID) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT count(*) FROM audit_records WHERE marketplace_account_id = $1`, account)
	if err != nil {
		t.Fatalf("query audit count: %v", err)
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan audit count: %v", err)
		}
	}
	if n != 0 {
		t.Fatalf("audit_records for account = %d after rollback, want 0", n)
	}
}
