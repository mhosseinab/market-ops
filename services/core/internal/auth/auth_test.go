package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/normalize"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// fakeStore is an in-memory Store for the auth service unit tests.
type fakeStore struct {
	usersByEmail map[string]db.User
	creds        map[uuid.UUID]string  // userID -> password hash
	sessions     map[string]db.Session // token hash -> session
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByEmail: map[string]db.User{},
		creds:        map[uuid.UUID]string{},
		sessions:     map[string]db.Session{},
	}
}

// GetUserByEmail mirrors the SQL query `WHERE email_canonical(email) =
// email_canonical($1)` (issue #201): rows are stored under their canonical
// (normalized) email key, and the lookup argument — pre-normalized by auth.Login
// with normalize.Email, which matches email_canonical — is matched verbatim. This
// fake keeps the caller-side normalization load-bearing rather than papering over
// it.
func (f *fakeStore) GetUserByEmail(_ context.Context, email string) (db.User, error) {
	u, ok := f.usersByEmail[email]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeStore) GetUserCredential(_ context.Context, userID uuid.UUID) (db.UserCredential, error) {
	h, ok := f.creds[userID]
	if !ok {
		return db.UserCredential{}, pgx.ErrNoRows
	}
	return db.UserCredential{UserID: userID, PasswordHash: h}, nil
}

func (f *fakeStore) UpsertUserCredential(_ context.Context, arg db.UpsertUserCredentialParams) error {
	f.creds[arg.UserID] = arg.PasswordHash
	return nil
}

func (f *fakeStore) CreateSession(_ context.Context, arg db.CreateSessionParams) (db.Session, error) {
	s := db.Session{TokenHash: arg.TokenHash, UserID: arg.UserID, ExpiresAt: arg.ExpiresAt}
	f.sessions[arg.TokenHash] = s
	return s, nil
}

func (f *fakeStore) GetSessionUser(_ context.Context, tokenHash string) (db.GetSessionUserRow, error) {
	s, ok := f.sessions[tokenHash]
	if !ok || !s.ExpiresAt.After(time.Now()) { // mirror the SQL "expires_at > now()"
		return db.GetSessionUserRow{}, pgx.ErrNoRows
	}
	u := usersByID(f, s.UserID)
	return db.GetSessionUserRow{
		TokenHash:      s.TokenHash,
		ExpiresAt:      s.ExpiresAt,
		UserID:         u.ID,
		OrganizationID: u.OrganizationID,
		Email:          u.Email,
		Role:           u.Role,
	}, nil
}

func (f *fakeStore) DeleteSession(_ context.Context, tokenHash string) error {
	delete(f.sessions, tokenHash)
	return nil
}

func (f *fakeStore) ListUsersByOrganization(_ context.Context, organizationID uuid.UUID) ([]db.User, error) {
	out := make([]db.User, 0)
	for _, u := range f.usersByEmail {
		if u.OrganizationID == organizationID {
			out = append(out, u)
		}
	}
	return out, nil
}

func usersByID(f *fakeStore, id uuid.UUID) db.User {
	for _, u := range f.usersByEmail {
		if u.ID == id {
			return u
		}
	}
	return db.User{}
}

// seedUser adds a user with a password to the fake store, returning the user.
func seedUser(t *testing.T, f *fakeStore, svc *Service, email, password string, role perm.Role) db.User {
	t.Helper()
	// The write path stores the canonical (normalized) email, exactly as the SQL
	// CreateUser stores email_canonical(email) (issue #201).
	canonical := normalize.Email(email)
	u := db.User{ID: uuid.New(), OrganizationID: uuid.New(), Email: canonical, Role: string(role)}
	f.usersByEmail[canonical] = u
	if err := svc.SetPassword(context.Background(), u.ID, password); err != nil {
		t.Fatalf("set password: %v", err)
	}
	return u
}

func TestLoginSuccessIssuesSession(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f)
	u := seedUser(t, f, svc, "owner@x.io", "hunter2hunter2", perm.RoleOwner)

	sess, err := svc.Login(context.Background(), "owner@x.io", "hunter2hunter2")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("no token issued")
	}
	if sess.Principal.UserID != u.ID || sess.Principal.Role != perm.RoleOwner {
		t.Fatalf("principal = %+v", sess.Principal)
	}
	// The raw token is never what is stored; the store holds only its hash.
	if _, rawStored := f.sessions[sess.Token]; rawStored {
		t.Fatal("raw token used as storage key — only its hash may be stored")
	}
	if _, hashStored := f.sessions[hashToken(sess.Token)]; !hashStored {
		t.Fatal("session not stored under token hash")
	}
}

func TestLoginWrongPasswordFailsClosed(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f)
	seedUser(t, f, svc, "op@x.io", "correctpass1", perm.RoleOperator)

	_, err := svc.Login(context.Background(), "op@x.io", "wrongpass1")
	if err != ErrInvalidCredentials {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginUnknownUserFailsClosed(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f)
	_, err := svc.Login(context.Background(), "ghost@x.io", "whatever12")
	if err != ErrInvalidCredentials {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

// TestLoginNormalizesEmailToSamePrincipal is the write/auth normalization-parity
// guard for issue #12: a user provisioned under "owner@x.io" must authenticate
// when the login form supplies the same address with different case and
// surrounding whitespace. Login normalizes identically to the write path, so the
// lookup resolves exactly one intended principal.
func TestLoginNormalizesEmailToSamePrincipal(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f)
	u := seedUser(t, f, svc, "owner@x.io", "hunter2hunter2", perm.RoleOwner)

	for _, variant := range []string{"owner@x.io", "Owner@X.IO", "  OWNER@x.io ", "\towner@X.IO\n"} {
		sess, err := svc.Login(context.Background(), variant, "hunter2hunter2")
		if err != nil {
			t.Fatalf("login with %q: %v", variant, err)
		}
		if sess.Principal.UserID != u.ID {
			t.Fatalf("login with %q resolved user %s, want %s", variant, sess.Principal.UserID, u.ID)
		}
		if sess.Principal.OrganizationID != u.OrganizationID {
			t.Fatalf("login with %q resolved org %s, want %s", variant, sess.Principal.OrganizationID, u.OrganizationID)
		}
	}
}

func TestResolveAndLogout(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f)
	seedUser(t, f, svc, "internal@x.io", "diagnose99", perm.RoleInternal)

	sess, err := svc.Login(context.Background(), "internal@x.io", "diagnose99")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	p, err := svc.Resolve(context.Background(), sess.Token)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p.Role != perm.RoleInternal {
		t.Fatalf("resolved role = %s", p.Role)
	}

	if err := svc.Logout(context.Background(), sess.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.Resolve(context.Background(), sess.Token); err != ErrNoSession {
		t.Fatalf("resolve after logout = %v, want ErrNoSession", err)
	}
	// Logout is idempotent.
	if err := svc.Logout(context.Background(), sess.Token); err != nil {
		t.Fatalf("second logout: %v", err)
	}
}

func TestResolveEmptyAndExpired(t *testing.T) {
	f := newFakeStore()
	// TTL in the past so every issued session is already expired.
	svc := NewService(f).WithClock(func() time.Time { return time.Now().Add(-2 * time.Hour) }).WithTTL(time.Hour)
	seedUser(t, f, svc, "owner@x.io", "password12", perm.RoleOwner)
	sess, err := svc.Login(context.Background(), "owner@x.io", "password12")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if _, err := svc.Resolve(context.Background(), ""); err != ErrNoSession {
		t.Fatalf("empty token resolve = %v, want ErrNoSession", err)
	}
	if _, err := svc.Resolve(context.Background(), sess.Token); err != ErrNoSession {
		t.Fatalf("expired session resolve = %v, want ErrNoSession", err)
	}
}

// TestListUsersScopesToOrganization is PD-3 item 7 (S37): ListUsers returns
// every user in the named organization, in a stable order, and never leaks a
// user from a DIFFERENT organization (cross-org containment for the roster
// read).
func TestListUsersScopesToOrganization(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f)
	org := uuid.New()
	other := uuid.New()

	a := db.User{ID: uuid.New(), OrganizationID: org, Email: "a@x.io", Role: string(perm.RoleOwner)}
	b := db.User{ID: uuid.New(), OrganizationID: org, Email: "b@x.io", Role: string(perm.RoleOperator)}
	c := db.User{ID: uuid.New(), OrganizationID: other, Email: "c@y.io", Role: string(perm.RoleOwner)}
	f.usersByEmail[a.Email] = a
	f.usersByEmail[b.Email] = b
	f.usersByEmail[c.Email] = c

	got, err := svc.ListUsers(context.Background(), org)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListUsers returned %d users, want 2 (org-scoped)", len(got))
	}
	for _, u := range got {
		if u.OrganizationID != org {
			t.Fatalf("ListUsers leaked a foreign-organization user: %+v", u)
		}
		if u.ID == c.ID {
			t.Fatal("ListUsers leaked the OTHER organization's user")
		}
	}
}

// TestListUsersEmptyOrganization proves an organization with no users returns
// an empty, non-error result — never a fabricated roster.
func TestListUsersEmptyOrganization(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f)
	got, err := svc.ListUsers(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListUsers on an unknown org = %d users, want 0", len(got))
	}
}
