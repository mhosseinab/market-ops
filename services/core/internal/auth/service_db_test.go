package auth_test

import (
	"context"
	"os"
	"strings"
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

	// The stored credential is an argon2id PHC-encoded hash, never the plaintext.
	cred, err := q.GetUserCredential(ctx, u.ID)
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if cred.PasswordHash == password {
		t.Fatal("plaintext password stored — must be an argon2id hash")
	}
	if !strings.HasPrefix(cred.PasswordHash, "$argon2id$") {
		t.Fatalf("stored credential is not an argon2id PHC hash: prefix = %.16q", cred.PasswordHash)
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

// TestGlobalEmailUniquenessRejectsCrossOrgDuplicate is the identity-isolation
// security regression for issue #12: under the globally-unique normalized-email
// model, the SAME email cannot exist in two organizations. Inserting the same
// address (even in a different case) into a second organization must be rejected
// by the schema, so login can never resolve an arbitrary matching tenant.
func TestGlobalEmailUniquenessRejectsCrossOrgDuplicate(t *testing.T) {
	q := newDBQueries(t)
	ctx := context.Background()

	orgA, err := q.CreateOrganization(ctx, "iso-A-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := q.CreateOrganization(ctx, "iso-B-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}

	email := "dup-" + uuid.NewString() + "@x.io"
	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		OrganizationID: orgA.ID, Email: email, Role: string(perm.RoleOwner),
	}); err != nil {
		t.Fatalf("create user in org A: %v", err)
	}

	// Same email, different organization, different case — must be rejected by the
	// global unique index on the normalized email.
	_, err = q.CreateUser(ctx, db.CreateUserParams{
		OrganizationID: orgB.ID, Email: strings.ToUpper(email), Role: string(perm.RoleOwner),
	})
	if err == nil {
		t.Fatal("cross-org duplicate email was accepted — global email uniqueness not enforced")
	}
}

// TestLoginResolvesExactOrganizationForNormalizedEmail proves login resolves
// exactly one principal, bound to the organization that owns the address, when
// the credentials are submitted with different case/whitespace than at write
// time (write/auth normalization parity, issue #12).
func TestLoginResolvesExactOrganizationForNormalizedEmail(t *testing.T) {
	q := newDBQueries(t)
	svc := auth.NewService(q)
	ctx := context.Background()

	org, err := q.CreateOrganization(ctx, "iso-C-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	email := "principal-" + uuid.NewString() + "@x.io"
	u, err := q.CreateUser(ctx, db.CreateUserParams{
		OrganizationID: org.ID, Email: email, Role: string(perm.RoleOwner),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	const password = "governOwner2026"
	if err := svc.SetPassword(ctx, u.ID, password); err != nil {
		t.Fatalf("set password: %v", err)
	}

	sess, err := svc.Login(ctx, "  "+strings.ToUpper(email)+" ", password)
	if err != nil {
		t.Fatalf("login with padded/upper email: %v", err)
	}
	if sess.Principal.UserID != u.ID {
		t.Fatalf("resolved user %s, want %s", sess.Principal.UserID, u.ID)
	}
	if sess.Principal.OrganizationID != org.ID {
		t.Fatalf("resolved org %s, want %s (token must carry the org from the same lookup)", sess.Principal.OrganizationID, org.ID)
	}
}

// TestWhitespaceAliasCannotCoexistCrossOrg is the issue #201 storage regression:
// a tab/newline-padded email and its trimmed twin must NOT be able to coexist in
// two organizations. Before the fix, the write used lower(btrim(email)) (1-arg
// btrim strips only spaces) and the index keyed lower(email), so a tab/newline
// alias stored a padded row the index treated as distinct — both inserts
// succeeded. After 0034, email_canonical() collapses the full whitespace set at
// write AND in the index, so the second insert is rejected. The user rows are
// created WITHOUT pre-normalizing (raw padded input) precisely to prove the
// schema — not the caller — is the enforcement authority.
func TestWhitespaceAliasCannotCoexistCrossOrg(t *testing.T) {
	q := newDBQueries(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		padded func(email string) string
	}{
		{"tab", func(e string) string { return "\t" + e + "\n" }},
		{"newline", func(e string) string { return "\n" + e + "\r" }},
		{"vertical-tab-formfeed", func(e string) string { return "\v" + e + "\f" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orgA, err := q.CreateOrganization(ctx, "iso201-A-"+uuid.NewString())
			if err != nil {
				t.Fatalf("create org A: %v", err)
			}
			orgB, err := q.CreateOrganization(ctx, "iso201-B-"+uuid.NewString())
			if err != nil {
				t.Fatalf("create org B: %v", err)
			}
			clean := "alias-" + uuid.NewString() + "@x.io"

			// Org A gets the RAW padded identifier (attacker-shaped input).
			a, err := q.CreateUser(ctx, db.CreateUserParams{
				OrganizationID: orgA.ID, Email: tc.padded(clean), Role: string(perm.RoleOwner),
			})
			if err != nil {
				t.Fatalf("create padded user in org A: %v", err)
			}
			// The stored email must already be canonical — the padding is gone, so
			// there is no distinct alias for the index to miss.
			if a.Email != clean {
				t.Fatalf("stored email = %q, want canonical %q (write did not canonicalize)", a.Email, clean)
			}

			// Org B's clean twin collides on the canonical form and MUST be rejected.
			if _, err := q.CreateUser(ctx, db.CreateUserParams{
				OrganizationID: orgB.ID, Email: clean, Role: string(perm.RoleOwner),
			}); err == nil {
				t.Fatal("whitespace alias coexisted across organizations — global canonical uniqueness not enforced")
			}
		})
	}
}

// TestPaddedIdentityCannotShadowOrIssueWrongOrgSession is the issue #201 auth
// regression, including the password-reuse case. A padded login id must resolve
// the EXACT principal that owns the canonical email — never another organization —
// and an attacker cannot pre-plant a same-canonical row in a second org to be
// shadowed, even with an identical password.
func TestPaddedIdentityCannotShadowOrIssueWrongOrgSession(t *testing.T) {
	q := newDBQueries(t)
	svc := auth.NewService(q)
	ctx := context.Background()
	const password = "governOwner2026" // reused across both orgs (password-reuse case)

	orgA, err := q.CreateOrganization(ctx, "iso201-owner-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := q.CreateOrganization(ctx, "iso201-shadow-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}
	clean := "principal-" + uuid.NewString() + "@x.io"

	// Org A owner created from a RAW tab/newline-padded identifier.
	a, err := q.CreateUser(ctx, db.CreateUserParams{
		OrganizationID: orgA.ID, Email: "\t" + clean + "\n", Role: string(perm.RoleOwner),
	})
	if err != nil {
		t.Fatalf("create padded owner: %v", err)
	}
	if err := svc.SetPassword(ctx, a.ID, password); err != nil {
		t.Fatalf("set password A: %v", err)
	}

	// The shadow attempt in org B with the SAME password must be rejected at write
	// time by canonical uniqueness — the wrong-org row can never be created.
	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		OrganizationID: orgB.ID, Email: clean, Role: string(perm.RoleOwner),
	}); err == nil {
		t.Fatal("shadow row with reused password was created in a second org — canonical uniqueness not enforced")
	}

	// Login with the padded identifier resolves EXACTLY org A's principal.
	sess, err := svc.Login(ctx, "\t"+clean+"\n", password)
	if err != nil {
		t.Fatalf("login with padded id: %v", err)
	}
	if sess.Principal.UserID != a.ID {
		t.Fatalf("resolved user %s, want %s", sess.Principal.UserID, a.ID)
	}
	if sess.Principal.OrganizationID != orgA.ID {
		t.Fatalf("resolved org %s, want %s — padded id must never issue a wrong-org session", sess.Principal.OrganizationID, orgA.ID)
	}

	// Login with the clean identifier resolves the SAME single principal — the
	// padded and trimmed forms are one identity, not two.
	sessClean, err := svc.Login(ctx, clean, password)
	if err != nil {
		t.Fatalf("login with clean id: %v", err)
	}
	if sessClean.Principal.UserID != a.ID || sessClean.Principal.OrganizationID != orgA.ID {
		t.Fatalf("clean-id principal mismatch: %+v", sessClean.Principal)
	}
}

// TestLoginUnknownNormalizedEmailFailsClosed confirms an email with no matching
// normalized row fails closed with ErrInvalidCredentials (no enumeration signal).
func TestLoginUnknownNormalizedEmailFailsClosed(t *testing.T) {
	q := newDBQueries(t)
	svc := auth.NewService(q)
	ctx := context.Background()
	if _, err := svc.Login(ctx, "no-such-"+uuid.NewString()+"@x.io", "whatever12"); err != auth.ErrInvalidCredentials {
		t.Fatalf("login err = %v, want ErrInvalidCredentials", err)
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
