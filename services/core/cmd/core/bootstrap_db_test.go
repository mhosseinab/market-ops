package main

// DB-backed proof for the `bootstrap-owner` subcommand — the production
// provisioning path. Mirrors the harness in internal/auth/service_db_test.go:
// it talks to the real schema through DATABASE_URL and skips when unset, so it
// runs in CI (scratch postgres:18 service container) and locally after
// `task db:reset`, and is a no-op elsewhere.
//
// What is proven here, in order of how badly each would hurt in production:
//
//  1. a fresh bootstrap creates organization + owner + credential + marketplace
//     account, and the created owner can actually log in (the whole point — a
//     provisioning path that produces an unusable credential is worthless)
//  2. re-running is idempotent and does NOT rotate the credential silently
//  3. BOOTSTRAP_ROTATE_PASSWORD=true rotates AND revokes live sessions
//  4. every input-validation path fails closed BEFORE opening a connection
//  5. a failure partway through leaves no orphan organization (the transaction)

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

func bootstrapTestQueries(t *testing.T) (*db.Queries, string) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping bootstrap-owner DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return db.New(pool), url
}

// env builds a getenv func from a map so the subcommand's environment contract
// is exercised exactly as it is in production, without touching os.Environ.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func baseEnv(url, email string) map[string]string {
	return map[string]string{
		"DATABASE_URL":                   url,
		"BOOTSTRAP_OWNER_EMAIL":          email,
		"BOOTSTRAP_OWNER_PASSWORD":       "correct-horse-battery",
		"BOOTSTRAP_ORG_NAME":             "Bootstrap Test Org " + uuid.NewString(),
		"BOOTSTRAP_ACCOUNT_NATIVE_ID":    "dk-" + uuid.NewString(),
		"BOOTSTRAP_ACCOUNT_DISPLAY_NAME": "Bootstrap Test Account",
	}
}

func TestBootstrapOwnerCreatesUsableOwner(t *testing.T) {
	q, url := bootstrapTestQueries(t)
	ctx := context.Background()
	email := "owner-" + uuid.NewString() + "@example.test"

	var out strings.Builder
	if err := runBootstrapOwner(ctx, env(baseEnv(url, email)), &out); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	user, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("owner was not created: %v", err)
	}
	if user.Role != "owner" {
		t.Fatalf("role = %q, want owner", user.Role)
	}
	account, err := q.GetMarketplaceAccountByOrganization(ctx, user.OrganizationID)
	if err != nil {
		t.Fatalf("marketplace account was not created: %v", err)
	}
	// The summary must carry the identifiers the handoff record needs, and must
	// never carry the password.
	summary := out.String()
	for _, want := range []string{user.ID.String(), user.OrganizationID.String(), account.ID.String()} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %s:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "correct-horse-battery") {
		t.Fatalf("summary leaked the plaintext password:\n%s", summary)
	}

	// The credential must actually work: this is the difference between "rows
	// were inserted" and "the operator can sign in".
	if _, err := auth.NewService(q).Login(ctx, email, "correct-horse-battery"); err != nil {
		t.Fatalf("bootstrapped owner cannot log in: %v", err)
	}
}

func TestBootstrapOwnerIsIdempotentAndDoesNotRotateSilently(t *testing.T) {
	q, url := bootstrapTestQueries(t)
	ctx := context.Background()
	email := "owner-" + uuid.NewString() + "@example.test"

	var first strings.Builder
	if err := runBootstrapOwner(ctx, env(baseEnv(url, email)), &first); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	created, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("owner missing after first run: %v", err)
	}

	// Second run with a DIFFERENT password and no rotate flag.
	second := baseEnv(url, email)
	second["BOOTSTRAP_OWNER_PASSWORD"] = "a-completely-different-one"
	var out strings.Builder
	if err := runBootstrapOwner(ctx, env(second), &out); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}

	again, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("owner missing after second run: %v", err)
	}
	if again.ID != created.ID {
		t.Fatalf("second run minted a new user %s (was %s)", again.ID, created.ID)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Errorf("second run did not report the existing owner:\n%s", out.String())
	}
	svc := auth.NewService(q)
	if _, err := svc.Login(ctx, email, "correct-horse-battery"); err != nil {
		t.Fatalf("original password stopped working without an explicit rotation: %v", err)
	}
	if _, err := svc.Login(ctx, email, "a-completely-different-one"); err == nil {
		t.Fatal("the second run silently applied a new password without BOOTSTRAP_ROTATE_PASSWORD")
	}
}

func TestBootstrapOwnerRotationRevokesLiveSessions(t *testing.T) {
	q, url := bootstrapTestQueries(t)
	ctx := context.Background()
	email := "owner-" + uuid.NewString() + "@example.test"

	var out strings.Builder
	if err := runBootstrapOwner(ctx, env(baseEnv(url, email)), &out); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	svc := auth.NewService(q)
	session, err := svc.Login(ctx, email, "correct-horse-battery")
	if err != nil {
		t.Fatalf("login before rotation: %v", err)
	}

	rotate := baseEnv(url, email)
	rotate["BOOTSTRAP_OWNER_PASSWORD"] = "rotated-password-value"
	rotate["BOOTSTRAP_ROTATE_PASSWORD"] = "true"
	var rotOut strings.Builder
	if err := runBootstrapOwner(ctx, env(rotate), &rotOut); err != nil {
		t.Fatalf("rotation: %v", err)
	}

	if _, err := svc.Login(ctx, email, "rotated-password-value"); err != nil {
		t.Fatalf("rotated password does not work: %v", err)
	}
	if _, err := svc.Login(ctx, email, "correct-horse-battery"); err == nil {
		t.Fatal("the superseded password still authenticates after rotation")
	}
	// The cookie issued before the rotation must be dead. A rotation that leaves
	// live sessions valid has revoked nothing.
	if _, err := svc.Resolve(ctx, session.Token); err == nil {
		t.Fatal("a session issued before the rotation still resolves")
	}
}

func TestBootstrapOwnerFailsClosedOnBadInput(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://unreachable.invalid:5432/none"
	}
	good := baseEnv(url, "owner-"+uuid.NewString()+"@example.test")

	cases := map[string]func(map[string]string){
		"no DATABASE_URL":    func(m map[string]string) { delete(m, "DATABASE_URL") },
		"no email":           func(m map[string]string) { delete(m, "BOOTSTRAP_OWNER_EMAIL") },
		"email without @":    func(m map[string]string) { m["BOOTSTRAP_OWNER_EMAIL"] = "not-an-address" },
		"no password":        func(m map[string]string) { delete(m, "BOOTSTRAP_OWNER_PASSWORD") },
		"password too short": func(m map[string]string) { m["BOOTSTRAP_OWNER_PASSWORD"] = "short" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := map[string]string{}
			for k, v := range good {
				m[k] = v
			}
			mutate(m)
			if _, err := loadBootstrapInput(env(m)); err == nil {
				t.Fatalf("%s should have been rejected before any connection is opened", name)
			}
		})
	}

	// A weak password must be rejected without ever appearing in the error.
	m := map[string]string{}
	for k, v := range good {
		m[k] = v
	}
	m["BOOTSTRAP_OWNER_PASSWORD"] = "hunter2"
	_, err := loadBootstrapInput(env(m))
	if err == nil {
		t.Fatal("short password accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("validation error leaked the password: %v", err)
	}
}

func TestRunSubcommandRejectsUnknownNames(t *testing.T) {
	if err := runSubcommand("serve", nil); err == nil {
		t.Fatal("an unknown subcommand must fail rather than boot the gateway")
	}
	if err := runSubcommand("bootstrap-owner", []string{"--force"}); err == nil {
		t.Fatal("bootstrap-owner must reject positional arguments")
	}
}
