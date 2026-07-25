package conversation_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// Issue #412 — a conversation may never reference a marketplace account owned by
// another organization (PRD §4.6 tenant integrity / identity quarantine).
//
// The ownership invariant is enforced at the DATABASE, not only in Go: migration
// 0048 replaces the single-column FK to marketplace_accounts(id) — which proved
// only that the account EXISTS — with a COMPOSITE foreign key on
// (marketplace_account_id, organization_id) into the composite UNIQUE target
// marketplace_accounts (id, organization_id) added by migration 0036, plus an
// ownership-pair immutability trigger. The tests below therefore BYPASS the Go
// layer entirely and attack the table with raw SQL, mirroring the #90
// (selection_set_lineages) attack-variant bar. A test that only exercises the Go
// path proves the Go path, not the invariant.

// assertRejectedByOwnershipInvariant proves a variant was stopped by the OWNERSHIP
// INVARIANT itself and not incidentally — by a typo, an undefined column, a
// PL/pgSQL placement restriction, or any other error that would make the fixture
// pass while testing nothing. Only two SQLSTATEs qualify:
//
//	23503 foreign_key_violation — migration 0048's composite FK
//	P0001 raise_exception       — the ownership-pair immutability trigger
//
// A variant that starts failing for a different reason is a fixture that has gone
// vacuous, which is exactly as dangerous as a variant that starts passing.
func assertRejectedByOwnershipInvariant(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("rejection is not a PostgreSQL error (%T: %v); the attack never reached the invariant", err, err)
	}
	switch pgErr.Code {
	case "23503", "P0001":
		if !strings.Contains(pgErr.Message, "412") && pgErr.ConstraintName != "conversations_account_org_fkey" {
			t.Fatalf("rejected by an unrelated %s (constraint %q: %s); the attack never reached the ownership invariant",
				pgErr.Code, pgErr.ConstraintName, pgErr.Message)
		}
	default:
		t.Fatalf("rejected by SQLSTATE %s (%s), not the ownership invariant (23503 composite FK / P0001 trigger); this fixture is vacuous",
			pgErr.Code, pgErr.Message)
	}
}

// countConversations counts rows for an explicit (org, account) pair. Used to prove
// a rejected attempt wrote NOTHING.
func countConversations(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, account *uuid.UUID) int {
	t.Helper()
	ctx := context.Background()
	var n int
	var err error
	if account == nil {
		err = pool.QueryRow(ctx,
			`SELECT count(*) FROM conversations WHERE organization_id = $1 AND marketplace_account_id IS NULL`,
			org).Scan(&n)
	} else {
		err = pool.QueryRow(ctx,
			`SELECT count(*) FROM conversations WHERE organization_id = $1 AND marketplace_account_id = $2`,
			org, *account).Scan(&n)
	}
	if err != nil {
		t.Fatalf("count conversations: %v", err)
	}
	return n
}

// TestRawSQLCrossOrgConversationAccountRejected is the #90-style attack suite: the
// Go guard is bypassed and the table is attacked directly. EVERY variant must be
// rejected by PostgreSQL itself.
func TestRawSQLCrossOrgConversationAccountRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	orgA, userA := seedOrgUser(t, q)
	orgB, userB := seedOrgUser(t, q)
	accountA := seedAccount(t, q, orgA)
	accountB := seedAccount(t, q, orgB)

	// A legitimate, same-org conversation in each org to mutate in the UPDATE variants.
	var convB uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
		 VALUES ($1, $2, $3) RETURNING id`, orgB, userB, accountB).Scan(&convB); err != nil {
		t.Fatalf("seed same-org conversation in org B: %v", err)
	}
	var convBNull uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
		 VALUES ($1, $2, NULL) RETURNING id`, orgB, userB).Scan(&convBNull); err != nil {
		t.Fatalf("seed no-account conversation in org B: %v", err)
	}
	var convA uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
		 VALUES ($1, $2, $3) RETURNING id`, orgA, userA, accountA).Scan(&convA); err != nil {
		t.Fatalf("seed same-org conversation in org A: %v", err)
	}

	variants := []struct {
		name string
		sql  string
		args []any
	}{
		{
			// V1 — the direct forgery: org B claims org A's account by literal value.
			name: "direct INSERT with a foreign account",
			sql: `INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
			      VALUES ($1, $2, $3)`,
			args: []any{orgB, userB, accountA},
		},
		{
			// V2 — the same forgery laundered through a SELECT, so the account id never
			// appears as a literal the application could have screened.
			name: "INSERT ... SELECT sourcing the foreign account from the accounts table",
			sql: `INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
			      SELECT $1, $2, ma.id FROM marketplace_accounts ma WHERE ma.id = $3`,
			args: []any{orgB, userB, accountA},
		},
		{
			// V3 — explicit id + explicit defaults, in case the invariant were (wrongly)
			// tied to the DEFAULT-generating insert shape.
			name: "INSERT with explicit id, title, pinned and retention",
			sql: `INSERT INTO conversations
			        (id, organization_id, opened_by_user_id, marketplace_account_id, title, pinned, retention_expires_at)
			      VALUES (gen_random_uuid(), $1, $2, $3, 'forged', true, now() + interval '900 days')`,
			args: []any{orgB, userB, accountA},
		},
		{
			// V4 — multi-row INSERT smuggling one cross-tenant row beside a valid one.
			// The whole statement must fail; neither row may land.
			name: "multi-row INSERT smuggling one cross-tenant row",
			sql: `INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
			      VALUES ($1, $2, $3), ($1, $2, $4)`,
			args: []any{orgB, userB, accountB, accountA},
		},
		{
			// V5 — re-point an existing org B conversation's account at org A's account.
			name: "UPDATE re-pointing marketplace_account_id to a foreign account",
			sql:  `UPDATE conversations SET marketplace_account_id = $2 WHERE id = $1`,
			args: []any{convB, accountA},
		},
		{
			// V6 — trap 3: re-point the ORGANIZATION instead. Moving org A's
			// account-bound conversation into org B would SATISFY a composite FK
			// evaluated only against the new pair if the account moved too; here it
			// breaks the pair, and the ownership-pair trigger rejects it regardless.
			name: "UPDATE re-pointing organization_id to another org",
			sql:  `UPDATE conversations SET organization_id = $2 WHERE id = $1`,
			args: []any{convA, orgB},
		},
		{
			// V7 — trap 3, the dangerous half: re-point BOTH columns to a COHERENT
			// foreign pair. This SATISFIES the composite foreign key, so only the
			// ownership-pair immutability trigger can reject it. Without the trigger
			// an attacker with raw SQL could migrate a conversation (and its whole
			// message history) into a victim tenant.
			name: "UPDATE re-pointing BOTH columns to a coherent foreign pair",
			sql:  `UPDATE conversations SET organization_id = $2, marketplace_account_id = $3 WHERE id = $1`,
			args: []any{convA, orgB, accountB},
		},
		{
			// V8 — bind an account onto a no-account conversation: the foreign case.
			name: "UPDATE binding a foreign account onto a no-account conversation",
			sql:  `UPDATE conversations SET marketplace_account_id = $2 WHERE id = $1`,
			args: []any{convBNull, accountA},
		},
		{
			// V9 — bind an account onto a no-account conversation: even the OWN-org
			// case is rejected. The ownership pair is claimed once at INSERT and is
			// never re-bound; no legitimate writer in this repo re-points it (the only
			// UPDATE on conversations is TouchConversation, which advances updated_at).
			// Rejecting the own-org re-bind too keeps the rule "the pair is immutable"
			// rather than "the pair is immutable unless it happens to type-check".
			name: "UPDATE binding an own-org account onto a no-account conversation",
			sql:  `UPDATE conversations SET marketplace_account_id = $2 WHERE id = $1`,
			args: []any{convBNull, accountB},
		},
		{
			// V10 — a fabricated account id under the caller's own org. The composite
			// FK rejects it exactly as the old single-column FK did (no regression in
			// the existence half of the constraint).
			name: "INSERT with a nonexistent account id",
			sql: `INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
			      VALUES ($1, $2, $3)`,
			args: []any{orgB, userB, uuid.New()},
		},
		{
			// V12 — upsert laundering: an INSERT that DELIBERATELY collides on the
			// primary key so the write lands as an UPDATE the statement never names.
			// A guard reasoning about statement KEYWORDS ("this is an INSERT, the FK
			// covers it") misses this; the ownership-pair TRIGGER is what rejects it,
			// because ON CONFLICT DO UPDATE fires BEFORE UPDATE, not BEFORE INSERT.
			name: "INSERT ... ON CONFLICT DO UPDATE re-pointing to a foreign account",
			sql: `INSERT INTO conversations (id, organization_id, opened_by_user_id, marketplace_account_id)
			      VALUES ($1, $2, $3, $4)
			      ON CONFLICT (id) DO UPDATE SET marketplace_account_id = $5`,
			args: []any{convB, orgB, userB, accountB, accountA},
		},
		{
			// V13 — MERGE (PostgreSQL 15+) reaches the same re-point through a THIRD
			// statement form with its own executor path. Like V12 it satisfies the
			// composite FK on the way in and is stopped by the trigger.
			name: "MERGE ... WHEN MATCHED THEN UPDATE to a foreign account",
			sql: `MERGE INTO conversations c
			      USING (SELECT $1::uuid AS id, $2::uuid AS account) s
			         ON c.id = s.id
			      WHEN MATCHED THEN UPDATE SET marketplace_account_id = s.account`,
			args: []any{convB, accountA},
		},
		{
			// V14 — a data-modifying CTE: the UPDATE is nested inside a statement that
			// PRESENTS as a read-only SELECT. Anything screening on the leading verb
			// sees "SELECT"; the trigger sees the UPDATE.
			name: "data-modifying CTE (WITH u AS (UPDATE ... RETURNING) SELECT)",
			sql: `WITH u AS (
			        UPDATE conversations SET marketplace_account_id = $2 WHERE id = $1 RETURNING id
			      ) SELECT count(*) FROM u`,
			args: []any{convB, accountA},
		},
		{
			// V15 — UPDATE ... FROM: the foreign account id is JOINED in from
			// marketplace_accounts rather than bound as a parameter, so it never
			// appears as a literal any application-layer screen could match on.
			name: "UPDATE ... FROM marketplace_accounts sourcing the foreign account by join",
			sql: `UPDATE conversations c
			         SET marketplace_account_id = ma.id
			        FROM marketplace_accounts ma
			       WHERE c.id = $1 AND ma.id = $2`,
			args: []any{convB, accountA},
		},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, v.sql, v.args...)
			if err == nil {
				t.Fatalf("raw SQL attack accepted by the database: %s", v.name)
			}
			assertRejectedByOwnershipInvariant(t, err)
		})
	}

	// V11 — the same forgery inside an explicit transaction, to prove the rejection
	// is at statement time and not something a COMMIT could smuggle past.
	t.Run("cross-tenant INSERT inside an explicit transaction", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx,
			`INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
			 VALUES ($1, $2, $3)`, orgB, userB, accountA)
		if err == nil {
			t.Fatal("cross-tenant INSERT accepted inside a transaction")
		}
		assertRejectedByOwnershipInvariant(t, err)
	})

	// V16 — COPY ... FROM STDIN. Bulk load is a genuinely different code path from
	// INSERT (binary protocol, no per-row plan), and it is the classic way to get
	// rows into a table while side-stepping application logic entirely. Constraints
	// and triggers still apply, and the composite FK must reject the cross-tenant
	// row. pgx's CopyFrom issues exactly this COPY.
	t.Run("COPY ... FROM STDIN smuggling a cross-tenant row", func(t *testing.T) {
		_, err := pool.CopyFrom(ctx,
			pgx.Identifier{"conversations"},
			[]string{"organization_id", "opened_by_user_id", "marketplace_account_id"},
			pgx.CopyFromRows([][]any{{orgB, userB, accountA}}),
		)
		if err == nil {
			t.Fatal("COPY accepted a cross-tenant row: bulk load bypasses the ownership invariant")
		}
		assertRejectedByOwnershipInvariant(t, err)
	})

	// Nothing from any variant survived: org B holds exactly the two conversations it
	// legitimately created, and none of them references org A's account.
	if got := countConversations(t, pool, orgB, &accountA); got != 0 {
		t.Fatalf("org B rows referencing org A's account = %d, want 0", got)
	}
	if got := countConversations(t, pool, orgB, &accountB); got != 1 {
		t.Fatalf("org B rows referencing its own account = %d, want 1 (the legitimate seed)", got)
	}
	// The org A conversation was neither moved nor re-pointed.
	var gotOrg uuid.UUID
	var gotAccount uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT organization_id, marketplace_account_id FROM conversations WHERE id = $1`, convA,
	).Scan(&gotOrg, &gotAccount); err != nil {
		t.Fatalf("re-read org A conversation: %v", err)
	}
	if gotOrg != orgA || gotAccount != accountA {
		t.Fatalf("org A conversation moved to (%s, %s), want (%s, %s)", gotOrg, gotAccount, orgA, accountA)
	}
}

// TestNullTransitLaunderingRejected closes the two-step route the single-statement
// variants cannot express: the trigger DELIBERATELY permits clearing the account to
// NULL (the ON DELETE SET NULL referential action performs exactly that UPDATE), so
// an attacker's natural next move is to use NULL as a WAYPOINT — clear the account,
// then re-bind a foreign one, arriving at a cross-tenant binding via two individually
// plausible statements.
//
// This is the test that pins NULL as a TERMINAL state for the account half rather
// than a reset: step one is allowed and step two is rejected, so the de-scoping
// exception can only ever REMOVE reach. If a future "fix" relaxed the trigger to
// permit re-binding from NULL (a tempting simplification, since it looks like a
// fresh claim), the composite FK alone would still let the OWN-org re-bind through
// and this test is what fails.
func TestNullTransitLaunderingRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	orgA, _ := seedOrgUser(t, q)
	orgB, userB := seedOrgUser(t, q)
	accountA := seedAccount(t, q, orgA)
	accountB := seedAccount(t, q, orgB)

	var conv uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
		 VALUES ($1, $2, $3) RETURNING id`, orgB, userB, accountB).Scan(&conv); err != nil {
		t.Fatalf("seed same-org conversation: %v", err)
	}

	// Step 1 — the de-scoping transition. ALLOWED BY DESIGN: it only removes reach.
	if _, err := pool.Exec(ctx,
		`UPDATE conversations SET marketplace_account_id = NULL WHERE id = $1`, conv); err != nil {
		t.Fatalf("clearing the account must remain allowed (ON DELETE SET NULL performs it): %v", err)
	}

	// Step 2 — the laundering payload. REJECTED: NULL is terminal, not a reset.
	_, err := pool.Exec(ctx,
		`UPDATE conversations SET marketplace_account_id = $2 WHERE id = $1`, conv, accountA)
	if err == nil {
		t.Fatal("re-bound a FOREIGN account after transiting through NULL: the account half is not terminal")
	}
	assertRejectedByOwnershipInvariant(t, err)
	// Even the organization's OWN account may not be re-bound afterwards — the rule
	// is "claim once, never re-point", not "never re-point across tenants".
	_, err = pool.Exec(ctx,
		`UPDATE conversations SET marketplace_account_id = $2 WHERE id = $1`, conv, accountB)
	if err == nil {
		t.Fatal("re-bound an OWN-ORG account after transiting through NULL: NULL must be terminal for the account half")
	}
	assertRejectedByOwnershipInvariant(t, err)

	// The row ended where step 1 left it, and no cross-tenant binding exists.
	var account *uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT marketplace_account_id FROM conversations WHERE id = $1`, conv).Scan(&account); err != nil {
		t.Fatalf("re-read conversation: %v", err)
	}
	if account != nil {
		t.Fatalf("marketplace_account_id = %s, want NULL (both re-binds must have rolled back)", account)
	}
	if got := countConversations(t, pool, orgB, &accountA); got != 0 {
		t.Fatalf("org B rows referencing org A's account = %d, want 0", got)
	}
}

// TestBeginTurnForeignAccountDenied proves the Go boundary fails closed on a NEW
// conversation naming another organization's account, writes NOTHING (the whole
// transaction rolls back), and produces an error INDISTINGUISHABLE from the one a
// nonexistent account produces — so the denial is no existence oracle for another
// tenant's account ids.
func TestBeginTurnForeignAccountDenied(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	orgA, _ := seedOrgUser(t, q)
	orgB, userB := seedOrgUser(t, q)
	accountA := seedAccount(t, q, orgA)

	foreignErr := beginTurnErr(t, store, orgB, userB, accountA, "forged turn")
	if !errors.Is(foreignErr, conversation.ErrAccountDenied) {
		t.Fatalf("foreign account error = %v, want ErrAccountDenied", foreignErr)
	}

	unknown := uuid.New()
	unknownErr := beginTurnErr(t, store, orgB, userB, unknown, "unknown account turn")
	if !errors.Is(unknownErr, conversation.ErrAccountDenied) {
		t.Fatalf("unknown account error = %v, want ErrAccountDenied", unknownErr)
	}

	// No existence oracle: the two rejections must be textually identical once the
	// caller-supplied account id (which the caller already knows) is removed. A
	// difference in wording, wrapping, or detail would tell org B whether org A's
	// account id exists.
	if got, want := scrub(foreignErr.Error(), accountA), scrub(unknownErr.Error(), unknown); got != want {
		t.Fatalf("foreign and unknown account rejections are distinguishable:\n foreign = %q\n unknown = %q", got, want)
	}

	// NOTHING was written: no conversation row, and therefore no message row.
	if got := countConversations(t, pool, orgB, &accountA); got != 0 {
		t.Fatalf("conversations written for the denied foreign account = %d, want 0", got)
	}
	var msgs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM conversation_messages m
		   JOIN conversations c ON c.id = m.conversation_id
		  WHERE c.organization_id = $1`, orgB).Scan(&msgs); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgs != 0 {
		t.Fatalf("messages appended under a denied turn = %d, want 0 (the transaction must roll back)", msgs)
	}
}

// beginTurnErr runs a NEW-conversation BeginTurn bound to account and returns the
// error it must have produced.
func beginTurnErr(t *testing.T, store *conversation.Store, org, user, account uuid.UUID, body string) error {
	t.Helper()
	_, err := store.BeginTurn(context.Background(), conversation.OpenParams{
		OrganizationID: org, UserID: user, MarketplaceAccountID: &account,
	}, body)
	if err == nil {
		t.Fatalf("BeginTurn with account %s succeeded, want denial", account)
	}
	return err
}

// scrub removes the caller-supplied account id from an error string so two
// rejections can be compared for shape rather than for the id the caller already
// supplied.
func scrub(msg string, account uuid.UUID) string {
	return strings.ReplaceAll(msg, account.String(), "<account>")
}

// TestSameOrgAndNoAccountConversationsStillOpen is the positive half: the
// ownership invariant must not break the two legitimate creation shapes — an
// own-org account, and NO account at all.
//
// The no-account case is why the composite foreign key relies on the DEFAULT
// MATCH SIMPLE semantics: with a NULL marketplace_account_id the constraint is
// satisfied, preserving migration 0005's "NULL means no account context was
// resolved yet". MATCH FULL would reject every account-less conversation.
func TestSameOrgAndNoAccountConversationsStillOpen(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)
	account := seedAccount(t, q, org)

	bound, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, MarketplaceAccountID: &account,
	}, "same-org bound turn")
	if err != nil {
		t.Fatalf("same-org BeginTurn: %v", err)
	}
	if bound.MarketplaceAccountID == nil || *bound.MarketplaceAccountID != account {
		t.Fatalf("bound account = %v, want %s", bound.MarketplaceAccountID, account)
	}

	free, err := store.BeginTurn(ctx, conversation.OpenParams{OrganizationID: org, UserID: user}, "no-account turn")
	if err != nil {
		t.Fatalf("no-account BeginTurn: %v", err)
	}
	if free.MarketplaceAccountID != nil {
		t.Fatalf("no-account conversation bound %v, want nil", free.MarketplaceAccountID)
	}

	// A raw-SQL no-account INSERT stays legal too (the MATCH SIMPLE proof at the DB
	// boundary, independent of the Go layer).
	if _, err := pool.Exec(ctx,
		`INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
		 VALUES ($1, $2, NULL)`, org, user); err != nil {
		t.Fatalf("raw no-account INSERT rejected: %v", err)
	}
}

// TestTouchConversationSurvivesOwnershipTrigger is trap 2: the ownership-pair
// immutability trigger must fire ONLY when the (organization_id,
// marketplace_account_id) pair actually changes. conversations.updated_at is a
// legitimate, documented UPDATE (TouchConversation, the only UPDATE in
// queries/conversation.sql) and must keep working — on an account-bound
// conversation as well as an account-less one.
func TestTouchConversationSurvivesOwnershipTrigger(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)
	account := seedAccount(t, q, org)

	bound, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, MarketplaceAccountID: &account,
	}, "first turn")
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}

	// A continuation runs TouchConversation inside BeginTurn.
	again, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &bound.ID,
	}, "second turn")
	if err != nil {
		t.Fatalf("continuation BeginTurn (TouchConversation) rejected: %v", err)
	}
	if !again.UpdatedAt.After(bound.UpdatedAt) && !again.UpdatedAt.Equal(bound.UpdatedAt) {
		t.Fatalf("updated_at went backwards: %v then %v", bound.UpdatedAt, again.UpdatedAt)
	}

	// The query itself, directly, on an account-bound row.
	if _, err := q.TouchConversation(ctx, db.TouchConversationParams{
		ID: bound.ID, UpdatedAt: again.UpdatedAt.Add(1), OrganizationID: org,
	}); err != nil {
		t.Fatalf("TouchConversation on an account-bound conversation: %v", err)
	}

	// A same-value re-write of the ownership pair is not a change and must pass
	// (IS DISTINCT FROM, not a blanket UPDATE reject).
	if _, err := pool.Exec(ctx,
		`UPDATE conversations SET organization_id = $2, marketplace_account_id = $3, title = 'renamed'
		  WHERE id = $1`, bound.ID, org, account); err != nil {
		t.Fatalf("same-value ownership rewrite rejected: %v", err)
	}
}

// TestAccountDeletionDescopesConversation pins the ON DELETE semantics. Migration
// 0005 documents ON DELETE SET NULL: deleting the account drops the conversation's
// account CONTEXT but keeps the retained interaction record (CHAT-008). Migration
// 0048 preserves that with the PostgreSQL 15+ column-list form
// `ON DELETE SET NULL (marketplace_account_id)` — the composite FK must NOT null
// organization_id, which is NOT NULL.
//
// The referential action issues an UPDATE on conversations, so it also proves the
// ownership trigger permits the de-scoping transition to NULL.
func TestAccountDeletionDescopesConversation(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)
	account := seedAccount(t, q, org)

	conv, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, MarketplaceAccountID: &account,
	}, "bound turn")
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM marketplace_accounts WHERE id = $1`, account); err != nil {
		t.Fatalf("delete marketplace account: %v", err)
	}

	var gotOrg uuid.UUID
	var gotAccount *uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT organization_id, marketplace_account_id FROM conversations WHERE id = $1`, conv.ID,
	).Scan(&gotOrg, &gotAccount); err != nil {
		t.Fatalf("re-read conversation after account deletion: %v", err)
	}
	if gotAccount != nil {
		t.Fatalf("account context after deletion = %v, want NULL", gotAccount)
	}
	if gotOrg != org {
		t.Fatalf("organization after deletion = %s, want %s (organization_id must never be nulled)", gotOrg, org)
	}
}

// TestAccountContextNeverResolvesForeignAccount covers acceptance criterion 4: the
// REACHABLE-path shape that 108c introduces. 108c wires GatewayReadPort and typed
// read adapters that scope authoritative reads by the conversation's stored
// account, so the property that must hold is not merely "CreateConversation
// rejects" but "AccountContext can never hand a caller an account its organization
// does not own" — for any conversation reachable under that organization, however
// it was created.
func TestAccountContextNeverResolvesForeignAccount(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	orgA, userA := seedOrgUser(t, q)
	orgB, userB := seedOrgUser(t, q)
	accountA := seedAccount(t, q, orgA)

	// Every construction path org B could try to plant a foreign-account conversation.
	_, goErr := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: orgB, UserID: userB, MarketplaceAccountID: &accountA,
	}, "forged turn")
	if !errors.Is(goErr, conversation.ErrAccountDenied) {
		t.Fatalf("Go path: %v, want ErrAccountDenied", goErr)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
		 VALUES ($1, $2, $3)`, orgB, userB, accountA); err == nil {
		t.Fatal("raw SQL path: cross-tenant conversation accepted")
	}

	// Whatever org B DOES hold, no reachable conversation resolves a foreign account.
	rows, err := pool.Query(ctx, `SELECT id FROM conversations WHERE organization_id = $1`, orgB)
	if err != nil {
		t.Fatalf("list org B conversations: %v", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate org B conversations: %v", err)
	}
	for _, id := range ids {
		acc, err := store.AccountContext(ctx, orgB, id)
		if err != nil {
			t.Fatalf("AccountContext(%s): %v", id, err)
		}
		if acc == nil {
			continue
		}
		owner, err := q.GetMarketplaceAccount(ctx, *acc)
		if err != nil {
			t.Fatalf("resolve owner of resolved account %s: %v", *acc, err)
		}
		if owner.OrganizationID != orgB {
			t.Fatalf("AccountContext handed org B account %s owned by org %s", *acc, owner.OrganizationID)
		}
	}

	// And org A's own conversation still resolves normally (the invariant closes the
	// cross-tenant path without breaking the legitimate one).
	convA, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: orgA, UserID: userA, MarketplaceAccountID: &accountA,
	}, "legitimate turn")
	if err != nil {
		t.Fatalf("org A BeginTurn: %v", err)
	}
	got, err := store.AccountContext(ctx, orgA, convA.ID)
	if err != nil || got == nil || *got != accountA {
		t.Fatalf("AccountContext for org A = (%v, %v), want %s", got, err, accountA)
	}
}

// TestForeignAccountDenialIsNotPgxErrNoRows guards the error identity itself: the
// denial must be the typed domain sentinel, never a leaked driver error that a
// caller could mistake for "no conversation" (which maps to a different, existing
// response shape) or swallow as a benign empty result.
func TestForeignAccountDenialIsNotPgxErrNoRows(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	orgA, _ := seedOrgUser(t, q)
	orgB, userB := seedOrgUser(t, q)
	accountA := seedAccount(t, q, orgA)

	err := beginTurnErr(t, store, orgB, userB, accountA, "forged turn")
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("the account denial leaks pgx.ErrNoRows; it must be the typed ErrAccountDenied")
	}
	if errors.Is(err, conversation.ErrConversationDenied) {
		t.Fatal("the account denial must not be conflated with ErrConversationDenied (different cause, different response)")
	}
}
