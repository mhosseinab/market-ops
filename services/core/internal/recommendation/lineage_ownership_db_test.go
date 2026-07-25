package recommendation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// Selection-set LINEAGE ownership (issue #90 blocker 1, PRD §4.6 identity
// quarantine / tenant isolation). A selection-set lineage belongs to EXACTLY ONE
// marketplace account, immutably, and that binding is enforced by the DATABASE —
// not merely checked in application code. These are the negative tests first: the
// cross-tenant takeover must fail closed at BOTH layers (the service, and the
// schema itself when the service guard is bypassed).

// TestPreviewBulkSelectionForOrg_CrossTenantLineageTakeoverRejected reproduces the
// takeover: tenant B, a fully-provisioned authorized caller, presents tenant A's
// selection-set LINEAGE on a preview refresh. Before the fix the lineage was used
// verbatim and B minted version N+1 INTO A's lineage under B's account, which (i)
// invalidated A's live confirmation bound to N and (ii) planted a B-owned row that
// satisfied B's confirm precheck. After the fix the refresh is rejected while the
// lineage lock is held, BEFORE any version is minted: A's version N stays current
// and A's bound confirmation still authorizes.
func TestPreviewBulkSelectionForOrg_CrossTenantLineageTakeoverRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	// Tenant A owns a lineage with one executable member, bound at version N.
	orgA, accountA, variantA := seedTenant(t, q)
	cardA := awaitingCard(t, svc, accountA, variantA)
	lineageA, versionA := previewExecutableSet(t, svc, accountA, variantA, cardA)

	// Tenant B is fully provisioned and supplies a member from its OWN account, so
	// member resolution cannot be what rejects the call — only lineage ownership can.
	orgB, accountB, variantB := seedTenant(t, q)
	cardB := awaitingCard(t, svc, accountB, variantB)

	_, err := svc.PreviewBulkSelectionForOrg(ctx, orgB, accountB, lineageA, "takeover", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variantB, RecommendationID: cardB.RecommendationID}})
	if !errors.Is(err, recommendation.ErrLineageNotOwned) {
		t.Fatalf("cross-tenant lineage refresh: err=%v; want ErrLineageNotOwned (fail closed)", err)
	}

	// No version was minted into A's lineage: A's version N is still the current one.
	current, err := db.New(pool).GetCurrentSelectionSet(ctx, lineageA)
	if err != nil {
		t.Fatalf("read current selection set: %v", err)
	}
	if current.Version != versionA {
		t.Fatalf("A's lineage advanced to version %d under B's refresh; want %d", current.Version, versionA)
	}
	if current.MarketplaceAccountID != accountA {
		t.Fatalf("A's lineage head is owned by %s; want %s", current.MarketplaceAccountID, accountA)
	}

	// A's confirmation bound to N is STILL valid and still authorizes its member.
	out, err := svc.ConfirmBulkSelectionForOrg(ctx, orgA, lineageA, versionA, time.Now().UTC(), testActor())
	if err != nil {
		t.Fatalf("owner confirm after attempted takeover: %v", err)
	}
	if !out.Valid || !out.ExecutionPending {
		t.Fatalf("owner confirm after takeover attempt: valid=%v pending=%v; want both true", out.Valid, out.ExecutionPending)
	}
	if st := itemFor(t, out.Items, cardA.RecommendationID).State; st != recommendation.BulkItemAuthorized {
		t.Fatalf("owner member state = %s; want authorized", st)
	}
}

// TestCreateSelectionSet_CrossTenantLineageRejected covers the SAME gap on the other
// minting path (the chat-side zero-member create): a foreign lineage is rejected
// before a version is minted.
func TestCreateSelectionSet_CrossTenantLineageRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)

	_, accountA, _ := seedTenant(t, q)
	_, accountB, _ := seedTenant(t, q)

	setA, err := svc.CreateSelectionSet(ctx, recommendation.SelectionSetInput{
		Account: accountA, Lineage: uuid.New(), Name: "a-set",
	})
	if err != nil {
		t.Fatalf("create A's selection set: %v", err)
	}

	if _, err := svc.CreateSelectionSet(ctx, recommendation.SelectionSetInput{
		Account: accountB, Lineage: setA.LineageID, Name: "takeover",
	}); !errors.Is(err, recommendation.ErrLineageNotOwned) {
		t.Fatalf("cross-tenant create: err=%v; want ErrLineageNotOwned", err)
	}

	current, err := db.New(pool).GetCurrentSelectionSet(ctx, setA.LineageID)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if current.Version != setA.Version || current.MarketplaceAccountID != accountA {
		t.Fatalf("A's lineage mutated: version=%d account=%s", current.Version, current.MarketplaceAccountID)
	}
}

// TestSelectionSetLineage_DBRejectsForgedCrossAccountRow is the mutation test that
// proves the ownership rule is DB-ENFORCED, not merely application-checked: with the
// service guard bypassed entirely (a raw INSERT, exactly what a compromised or future
// code path could attempt), PostgreSQL itself rejects a selection_sets row whose
// account differs from the lineage owner.
func TestSelectionSetLineage_DBRejectsForgedCrossAccountRow(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)

	_, accountA, _ := seedTenant(t, q)
	_, accountB, _ := seedTenant(t, q)

	setA, err := svc.CreateSelectionSet(ctx, recommendation.SelectionSetInput{
		Account: accountA, Lineage: uuid.New(), Name: "a-set",
	})
	if err != nil {
		t.Fatalf("create A's selection set: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO selection_sets (marketplace_account_id, lineage_id, version, name, criteria, member_count, membership_fingerprint)
		VALUES ($1, $2, 99, 'forged', '{}'::jsonb, 0, ''::bytea)`,
		accountB, setA.LineageID)
	if err == nil {
		t.Fatalf("forged cross-account selection_sets row was ACCEPTED by the database; ownership is not DB-enforced")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("forged row rejected with %v; want foreign_key_violation (SQLSTATE 23503)", err)
	}
	if pgErr.ConstraintName != "selection_sets_lineage_account_fkey" {
		t.Fatalf("forged row rejected by %q; want selection_sets_lineage_account_fkey", pgErr.ConstraintName)
	}

	// Ownership is INSERT-ONCE: the append-only posture forbids re-pointing a lineage
	// at another account (that would retro-actively transfer every sealed version).
	_, err = pool.Exec(ctx,
		`UPDATE selection_set_lineages SET marketplace_account_id = $1 WHERE lineage_id = $2`,
		accountB, setA.LineageID)
	if err == nil {
		t.Fatalf("selection_set_lineages ownership was UPDATEable; want immutable (append-only, §4.6)")
	}
}

// TestConfirmBulkSelection_ForeignAccountReadsNothing proves the confirmation's
// authoritative current-version read is ACCOUNT-SCOPED: presenting a foreign account
// with a valid lineage + version matches no row (uniform not-found, no existence
// oracle) and authorizes nothing.
func TestConfirmBulkSelection_ForeignAccountReadsNothing(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	_, accountA, variantA := seedTenant(t, q)
	cardA := awaitingCard(t, svc, accountA, variantA)
	lineageA, versionA := previewExecutableSet(t, svc, accountA, variantA, cardA)
	_, accountB, _ := seedTenant(t, q)

	out, err := svc.ConfirmBulkSelection(ctx, accountB, lineageA, versionA, time.Now().UTC(), testActor())
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign-account confirm: err=%v; want pgx.ErrNoRows", err)
	}
	if out.Valid || len(out.Items) != 0 {
		t.Fatalf("foreign-account confirm leaked an outcome: %+v", out)
	}
	if got := reloadState(t, svc, cardA.ID); got != approval.StateAwaitingConfirmation {
		t.Fatalf("A's member advanced to %s; want awaiting_confirmation", got)
	}
}

// TestConfirmBulkSelection_CurrentReadHeldUnderLineageLock proves the account-scoped
// current-version read happens INSIDE the confirmation transaction while the lineage
// lock is held: a concurrent refresh that already holds the lock BLOCKS the
// confirmation, and once that refresh commits version N+1 the confirmation observes
// it and fails closed as stale. Were the read taken outside the lock (the pre-fix
// behaviour) the confirmation would have read the pre-refresh head and authorized
// against a version that was already superseded.
func TestConfirmBulkSelection_CurrentReadHeldUnderLineageLock(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	_, account, variant := seedTenant(t, q)
	card := awaitingCard(t, svc, account, variant)
	lineage, v1 := previewExecutableSet(t, svc, account, variant, card)

	// A concurrent refresh takes the lineage lock and holds it in its own transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin refresh tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := db.New(tx).LockApprovalLineage(ctx, lineage); err != nil {
		t.Fatalf("lock lineage: %v", err)
	}

	type confirmResult struct {
		out recommendation.BulkConfirmOutcome
		err error
	}
	done := make(chan confirmResult, 1)
	go func() {
		out, err := svc.ConfirmBulkSelection(ctx, account, lineage, v1, time.Now().UTC(), testActor())
		done <- confirmResult{out: out, err: err}
	}()

	// The confirmation must BLOCK on the lock rather than read a head outside it.
	select {
	case r := <-done:
		t.Fatalf("confirmation completed while the lineage lock was held elsewhere (read is outside the lock): %+v err=%v", r.out, r.err)
	case <-time.After(500 * time.Millisecond):
	}

	// The refresh mints version N+1 and commits, releasing the lock.
	if _, err := db.New(tx).InsertSelectionSet(ctx, db.InsertSelectionSetParams{
		MarketplaceAccountID:  account,
		LineageID:             lineage,
		Name:                  "refreshed",
		Criteria:              []byte(`{}`),
		MemberCount:           0,
		MembershipFingerprint: []byte{},
	}); err != nil {
		t.Fatalf("mint v2 in refresh tx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit refresh: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("confirmation after refresh: %v", r.err)
		}
		if r.out.Valid {
			t.Fatalf("confirmation bound to v%d reported VALID after a concurrent refresh minted v%d", v1, r.out.CurrentVersion)
		}
		if len(r.out.Items) != 0 {
			t.Fatalf("stale confirmation authorized %d items; want 0", len(r.out.Items))
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("confirmation did not complete after the lock was released")
	}

	if got := reloadState(t, svc, card.ID); got != approval.StateAwaitingConfirmation {
		t.Fatalf("member card advanced to %s on a stale bulk confirm; want awaiting_confirmation", got)
	}
}
