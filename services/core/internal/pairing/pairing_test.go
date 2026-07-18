package pairing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// fakeStore is an in-memory pairing Store. It models the single-use claim and the
// revoke/expiry exclusions with plain maps so the service logic is tested without
// a database.
type fakeStore struct {
	account uuid.UUID
	noAcct  bool
	rows    map[uuid.UUID]*db.ExtensionPairing // by id
	byCode  map[string]uuid.UUID               // code_hash -> id
	byCred  map[string]uuid.UUID               // credential_hash -> id
	now     time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		account: uuid.New(),
		rows:    map[uuid.UUID]*db.ExtensionPairing{},
		byCode:  map[string]uuid.UUID{},
		byCred:  map[string]uuid.UUID{},
		now:     time.Now(),
	}
}

func (f *fakeStore) GetMarketplaceAccountByOrganization(_ context.Context, _ uuid.UUID) (db.MarketplaceAccount, error) {
	if f.noAcct {
		return db.MarketplaceAccount{}, pgx.ErrNoRows
	}
	return db.MarketplaceAccount{ID: f.account}, nil
}

func (f *fakeStore) CreatePairingCode(_ context.Context, arg db.CreatePairingCodeParams) (db.ExtensionPairing, error) {
	row := &db.ExtensionPairing{
		ID:                   uuid.New(),
		MarketplaceAccountID: arg.MarketplaceAccountID,
		CodeHash:             arg.CodeHash,
		CodeExpiresAt:        arg.CodeExpiresAt,
	}
	f.rows[row.ID] = row
	f.byCode[arg.CodeHash.String] = row.ID
	return *row, nil
}

func (f *fakeStore) ClaimPairingCode(_ context.Context, arg db.ClaimPairingCodeParams) (db.ExtensionPairing, error) {
	id, ok := f.byCode[arg.CodeHash.String]
	if !ok {
		return db.ExtensionPairing{}, pgx.ErrNoRows
	}
	row := f.rows[id]
	// Model the query's WHERE: unclaimed, unrevoked, code unexpired.
	if row.ClaimedAt.Valid || row.RevokedAt.Valid || !row.CodeExpiresAt.After(f.now) {
		return db.ExtensionPairing{}, pgx.ErrNoRows
	}
	row.CredentialHash = arg.CredentialHash
	row.CredentialExpiresAt = arg.CredentialExpiresAt
	row.ClaimedAt = pgtype.Timestamptz{Time: f.now, Valid: true}
	// Single-use: clear the code hash and its lookup.
	delete(f.byCode, arg.CodeHash.String)
	row.CodeHash = pgtype.Text{}
	f.byCred[arg.CredentialHash.String] = row.ID
	return *row, nil
}

func (f *fakeStore) ResolveCaptureCredential(_ context.Context, credentialHash pgtype.Text) (db.ResolveCaptureCredentialRow, error) {
	id, ok := f.byCred[credentialHash.String]
	if !ok {
		return db.ResolveCaptureCredentialRow{}, pgx.ErrNoRows
	}
	row := f.rows[id]
	if row.RevokedAt.Valid || !row.CredentialExpiresAt.Time.After(f.now) {
		return db.ResolveCaptureCredentialRow{}, pgx.ErrNoRows
	}
	return db.ResolveCaptureCredentialRow{
		ID:                   row.ID,
		MarketplaceAccountID: row.MarketplaceAccountID,
		CredentialExpiresAt:  row.CredentialExpiresAt,
	}, nil
}

func (f *fakeStore) RevokePairingsForAccount(_ context.Context, accountID uuid.UUID) error {
	for _, row := range f.rows {
		if row.MarketplaceAccountID == accountID && !row.RevokedAt.Valid {
			row.RevokedAt = pgtype.Timestamptz{Time: f.now, Valid: true}
		}
	}
	return nil
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestMintClaimResolve is the happy path: a minted code claims into a credential
// that resolves to the scoped account.
func TestMintClaimResolve(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(fs)
	ctx := context.Background()

	code, err := svc.MintCode(ctx, uuid.New())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if code.Code == "" || code.MarketplaceAccountID != fs.account {
		t.Fatalf("bad code: %+v", code)
	}
	// The raw code is never persisted — only its hash is a lookup key.
	if _, ok := fs.byCode[code.Code]; ok {
		t.Fatal("raw code used as storage key; only the hash must be stored")
	}
	if _, ok := fs.byCode[hashHex(code.Code)]; !ok {
		t.Fatal("code hash not stored")
	}

	cred, err := svc.Claim(ctx, code.Code)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if cred.Credential == "" || cred.MarketplaceAccountID != fs.account {
		t.Fatalf("bad credential: %+v", cred)
	}
	if cred.Credential == code.Code {
		t.Fatal("credential must not equal the pairing code")
	}

	res, err := svc.ResolveCredential(ctx, cred.Credential)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.MarketplaceAccountID != fs.account {
		t.Fatalf("resolved account = %v, want %v", res.MarketplaceAccountID, fs.account)
	}
}

// TestClaimIsSingleUse proves a code cannot be claimed twice (a replayed claim
// mints no second credential).
func TestClaimIsSingleUse(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(fs)
	ctx := context.Background()

	code, _ := svc.MintCode(ctx, uuid.New())
	if _, err := svc.Claim(ctx, code.Code); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := svc.Claim(ctx, code.Code); err != ErrInvalidCode {
		t.Fatalf("second claim err = %v, want ErrInvalidCode", err)
	}
}

// TestRevokeBlocksResolution proves a revoked credential no longer resolves —
// the server-side half of EXT-001 revocation (uploads then fail closed with 401).
func TestRevokeBlocksResolution(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(fs)
	ctx := context.Background()

	code, _ := svc.MintCode(ctx, uuid.New())
	cred, _ := svc.Claim(ctx, code.Code)
	if _, err := svc.ResolveCredential(ctx, cred.Credential); err != nil {
		t.Fatalf("pre-revoke resolve: %v", err)
	}
	if err := svc.RevokeForOrganization(ctx, uuid.New()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.ResolveCredential(ctx, cred.Credential); err != ErrInvalidCredential {
		t.Fatalf("post-revoke resolve err = %v, want ErrInvalidCredential", err)
	}
}

// TestExpiredCodeFailsClosed proves an expired pairing code cannot be claimed.
func TestExpiredCodeFailsClosed(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(fs).WithTTLs(time.Minute, time.Hour)
	ctx := context.Background()

	code, _ := svc.MintCode(ctx, uuid.New())
	// Advance the store clock past the code TTL.
	fs.now = fs.now.Add(2 * time.Minute)
	if _, err := svc.Claim(ctx, code.Code); err != ErrInvalidCode {
		t.Fatalf("expired claim err = %v, want ErrInvalidCode", err)
	}
}

// TestUnknownCredentialFailsClosed proves a fabricated credential never resolves.
func TestUnknownCredentialFailsClosed(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(fs)
	if _, err := svc.ResolveCredential(context.Background(), "not-a-real-credential"); err != ErrInvalidCredential {
		t.Fatalf("unknown credential err = %v, want ErrInvalidCredential", err)
	}
}

// TestMintNoAccountFailsClosed proves an organization with no marketplace account
// cannot mint a code (no dangling credential is ever created).
func TestMintNoAccountFailsClosed(t *testing.T) {
	fs := newFakeStore()
	fs.noAcct = true
	svc := NewService(fs)
	if _, err := svc.MintCode(context.Background(), uuid.New()); err != ErrNoAccount {
		t.Fatalf("mint err = %v, want ErrNoAccount", err)
	}
}
