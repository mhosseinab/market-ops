package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
	"github.com/mhosseinab/market-ops/services/core/internal/watchlist"
)

// This file is the issue #237 proof at the real HTTP boundary: an authorized Owner
// in org B, presenting a valid session, cannot read or write org A's guardrails,
// watchlist, or Market conflict data by supplying A's account id in the query param
// or request body. Every cross-account request returns a uniform 404 with NO
// disclosure and — for the guardrail/watchlist writes — NO state mutation and NO
// audit row for the foreign account. The SAME request from account A's own operator
// succeeds (positive control). Scope is derived from the session principal's
// organization, never from request input. SetGuardrails is a money/policy write, so
// its cross-tenant-write rejection is the highest-severity assertion here (§4.6).

// seedOrgAndAccount creates a fresh org + its single marketplace account (a distinct
// tenant) and returns both ids.
func seedOrgAndAccount(t *testing.T, q *db.Queries, label string) (orgID, accountID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "tenant-237-"+label+"-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID: org.ID, NativeAccountID: "native-" + uuid.NewString(), DisplayName: "Tenant " + label,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return org.ID, acct.ID
}

// seedGuardrails writes an initial guardrail config for account A, so the read has a
// real, owned config to hide from B and the write has an existing row a cross-tenant
// write must not alter.
func seedGuardrails(t *testing.T, pool *pgxpool.Pool, account uuid.UUID, movementCapBp int64) {
	t.Helper()
	floor, err := money.New(1000, "USD", -2)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	if _, err := guardrail.NewService(pool).Set(context.Background(), account,
		audit.Actor{ID: "seed@a.io", Role: "owner", Surface: "screens"},
		guardrail.Settings{
			ContributionFloor: floor,
			MovementCapBp:     movementCapBp,
			CooldownSeconds:   3600,
			Strategy:          policy.StrategyMatch,
			StrategyEnabled:   true,
		}); err != nil {
		t.Fatalf("seed guardrails: %v", err)
	}
}

// countGuardrailAudits counts the append-only guardrail_change audit records for an
// account — used to prove a rejected cross-tenant write appends NOTHING.
func countGuardrailAudits(t *testing.T, pool *pgxpool.Pool, account uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_records WHERE marketplace_account_id = $1 AND event_type = 'guardrail_change'`,
		account).Scan(&n); err != nil {
		t.Fatalf("count guardrail audits: %v", err)
	}
	return n
}

// TestTenantScopingS37_CrossAccountGuardrailsAreNotFound proves GetGuardrails and
// SetGuardrails reject a cross-account request with a uniform 404, and that the
// rejected write leaves A's guardrails and audit trail untouched (the money/policy
// cross-tenant-write hole, issue #237). Positive control: A's own operator reads and
// writes its guardrails.
func TestTenantScopingS37_CrossAccountGuardrailsAreNotFound(t *testing.T) {
	pool, q := newSystemPool(t)

	orgA, accountA := seedOrgAndAccount(t, q, "A")
	orgB, _ := seedOrgAndAccount(t, q, "B")
	seedGuardrails(t, pool, accountA, 500)

	guard := guardrail.NewService(pool)
	srvB, tokB := systemOwnerServerForOrg(t, orgB, WithGuardrail(guard))
	srvA, tokA := systemOwnerServerForOrg(t, orgA, WithGuardrail(guard))

	a := accountA.String()

	// --- Cross-account GET from B → uniform 404, no disclosure. ----------------
	if rec := getJSON(t, srvB, tokB, "/guardrails?marketplaceAccountId="+a, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account GET /guardrails: status=%d, want 404 (uniform not-found); body=%s", rec.Code, rec.Body.String())
	}

	// --- Cross-account SET from B (money/policy write) → 404, NO mutation. -----
	auditsBefore := countGuardrailAudits(t, pool, accountA)
	writeBody := `{"marketplaceAccountId":"` + a + `","settings":{"contributionFloor":{"mantissa":"999","currency":"USD","exponent":-2},"movementCapBasisPoints":9999,"cooldownSeconds":60,"strategy":"undercut","strategyEnabled":true}}`
	if rec := postJSON(t, srvB, tokB, "/guardrails", writeBody); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account POST /guardrails: status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	// A's guardrails must be byte-for-byte what the seed wrote — the foreign write
	// never landed.
	after, err := guard.Get(context.Background(), accountA)
	if err != nil {
		t.Fatalf("reload A's guardrails: %v", err)
	}
	if after.Settings.MovementCapBp != 500 || after.Settings.Strategy != policy.StrategyMatch {
		t.Fatalf("cross-account write mutated A's guardrails: %+v (must be movementCap=500, strategy=match)", after.Settings)
	}
	if got := countGuardrailAudits(t, pool, accountA); got != auditsBefore {
		t.Fatalf("cross-account write appended an audit row for A (%d -> %d); a foreign account must leave NO audit trail", auditsBefore, got)
	}

	// --- Positive control: A's own operator reads (200) and writes (200). ------
	if rec := getJSON(t, srvA, tokA, "/guardrails?marketplaceAccountId="+a, nil); rec.Code != http.StatusOK {
		t.Fatalf("account A's own GET /guardrails: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	ownWrite := `{"marketplaceAccountId":"` + a + `","settings":{"contributionFloor":{"mantissa":"1000","currency":"USD","exponent":-2},"movementCapBasisPoints":300,"cooldownSeconds":3600,"strategy":"hold","strategyEnabled":false}}`
	if rec := postJSON(t, srvA, tokA, "/guardrails", ownWrite); rec.Code != http.StatusOK {
		t.Fatalf("account A's own POST /guardrails: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	own, err := guard.Get(context.Background(), accountA)
	if err != nil {
		t.Fatalf("reload A's guardrails after own write: %v", err)
	}
	if own.Settings.MovementCapBp != 300 {
		t.Fatalf("account A's own write did not land: movementCap=%d, want 300", own.Settings.MovementCapBp)
	}
}

// TestTenantScopingS37_CrossAccountWatchlistIsNotFound proves ListWatchlist and
// AddWatchlistEntry reject a cross-account request with a uniform 404, and that the
// rejected add inserts no entry and appends no audit row for the foreign account
// (issue #237). Positive control: A's own operator can list its (empty) watchlist.
func TestTenantScopingS37_CrossAccountWatchlistIsNotFound(t *testing.T) {
	pool, q := newSystemPool(t)

	orgA, accountA := seedOrgAndAccount(t, q, "A")
	orgB, _ := seedOrgAndAccount(t, q, "B")

	wl := watchlist.NewService(pool)
	srvB, tokB := systemOwnerServerForOrg(t, orgB, WithWatchlist(wl))
	srvA, tokA := systemOwnerServerForOrg(t, orgA, WithWatchlist(wl))

	a := accountA.String()

	// Cross-account GET from B → uniform 404.
	if rec := getJSON(t, srvB, tokB, "/watchlist?marketplaceAccountId="+a, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account GET /watchlist: status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	// Cross-account ADD from B (with A's account id in the body) → 404 with NO
	// insert. The ownership guard rejects BEFORE the confirmed-identity check, so an
	// arbitrary variant id is fine — the request never reaches the insert.
	addBody := `{"marketplaceAccountId":"` + a + `","variantId":"` + uuid.NewString() + `"}`
	if rec := postJSON(t, srvB, tokB, "/watchlist", addBody); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account POST /watchlist: status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	entries, err := wl.List(context.Background(), accountA)
	if err != nil {
		t.Fatalf("list A's watchlist: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("cross-account add inserted an entry for A (must be none): %d entries", len(entries))
	}

	// Positive control: A's own operator can list its own (empty) watchlist.
	if rec := getJSON(t, srvA, tokA, "/watchlist?marketplaceAccountId="+a, nil); rec.Code != http.StatusOK {
		t.Fatalf("account A's own GET /watchlist: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestTenantScopingS37_CrossAccountMarketConflictsAreNotFound proves
// ListMarketConflicts rejects a cross-account request with a uniform 404 rather than
// disclosing another tenant's Market conflict view (issue #237). Positive control:
// A's own operator reads its own (empty) conflict list.
func TestTenantScopingS37_CrossAccountMarketConflictsAreNotFound(t *testing.T) {
	pool, q := newSystemPool(t)

	orgA, accountA := seedOrgAndAccount(t, q, "A")
	orgB, _ := seedOrgAndAccount(t, q, "B")

	obs := observation.NewService(pool)
	srvB, tokB := systemOwnerServerForOrg(t, orgB, WithObservation(obs))
	srvA, tokA := systemOwnerServerForOrg(t, orgA, WithObservation(obs))

	a := accountA.String()

	if rec := getJSON(t, srvB, tokB, "/market/conflicts?marketplaceAccountId="+a, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account GET /market/conflicts: status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getJSON(t, srvA, tokA, "/market/conflicts?marketplaceAccountId="+a, nil); rec.Code != http.StatusOK {
		t.Fatalf("account A's own GET /market/conflicts: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
