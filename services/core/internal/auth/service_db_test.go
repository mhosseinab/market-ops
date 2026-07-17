package auth_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// newDBQueries connects to DATABASE_URL and returns sqlc queries, skipping when
// unset (mirrors the connector DB test). The auth migration (0003) must be
// applied via `task db:reset`.
func newDBQueries(t *testing.T) *db.Queries {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping auth DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return db.New(pool)
}

func seedOwner(t *testing.T, q *db.Queries) db.User {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "auth-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	u, err := q.CreateUser(ctx, db.CreateUserParams{
		OrganizationID: org.ID,
		Email:          "owner-" + uuid.NewString() + "@x.io",
		Role:           string(perm.RoleOwner),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

// TestAuthLifecycleDBBacked proves the full DB-backed path: set password, login,
// resolve the session, and log out — against the real 0003 schema. It also
// asserts the plaintext password is never stored (only the argon2id hash).
func TestAuthLifecycleDBBacked(t *testing.T) {
	q := newDBQueries(t)
	svc := auth.NewService(q)
	ctx := context.Background()
	u := seedOwner(t, q)

	const password = "governOwner2026"
	if err := svc.SetPassword(ctx, u.ID, password); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// The stored credential is an argon2id hash, never the plaintext.
	cred, err := q.GetUserCredential(ctx, u.ID)
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if cred.PasswordHash == password {
		t.Fatal("plaintext password stored — must be an argon2id hash")
	}

	sess, err := svc.Login(ctx, u.Email, password)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.Principal.Role != perm.RoleOwner {
		t.Fatalf("role = %s", sess.Principal.Role)
	}

	p, err := svc.Resolve(ctx, sess.Token)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p.UserID != u.ID || p.OrganizationID != u.OrganizationID {
		t.Fatalf("principal mismatch: %+v", p)
	}

	if err := svc.Logout(ctx, sess.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.Resolve(ctx, sess.Token); err != auth.ErrNoSession {
		t.Fatalf("resolve after logout = %v, want ErrNoSession", err)
	}
}

// TestLoginWrongPasswordDBBacked confirms the fail-closed path against real
// storage.
func TestLoginWrongPasswordDBBacked(t *testing.T) {
	q := newDBQueries(t)
	svc := auth.NewService(q)
	ctx := context.Background()
	u := seedOwner(t, q)
	if err := svc.SetPassword(ctx, u.ID, "rightPassword1"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, err := svc.Login(ctx, u.Email, "wrongPassword1"); err != auth.ErrInvalidCredentials {
		t.Fatalf("login err = %v, want ErrInvalidCredentials", err)
	}
}
