package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
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
	u := db.User{ID: uuid.New(), OrganizationID: uuid.New(), Email: email, Role: string(role)}
	f.usersByEmail[email] = u
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
