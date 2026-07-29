package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/pairing"
)

// Issue #149 headline acceptance test, end to end against the REAL pairing
// service + Postgres: after a CONFIRMED self-revoke, a REPLAY of the very same
// (copied) capture credential is refused with 401 on the capture route. Before
// the fix the extension only deleted its local copy, so a copied credential kept
// uploading until its own server-side expiry.
//
// It also proves the two properties that make the kill switch trustworthy:
//   - Self-limiting: a self-revoke kills EXACTLY the presenting credential. A
//     second device paired to the SAME account keeps working, and another
//     account is untouched — nobody can revoke somebody else's pairing.
//   - Idempotent: repeating the self-revoke never errors ambiguously; it is
//     refused with the same fail-closed 401 the capture route gives.
func TestSelfRevokeInvalidatesTheCredentialAtTheAuthority(t *testing.T) {
	_, q := newIntegrationPool(t)
	ctx := context.Background()

	newAccount := func(prefix string) (uuid.UUID, uuid.UUID) {
		t.Helper()
		org, err := q.CreateOrganization(ctx, prefix+"-"+uuid.NewString())
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
			OrganizationID:  org.ID,
			NativeAccountID: "native-" + uuid.NewString(),
			DisplayName:     "Ext Seller",
		})
		if err != nil {
			t.Fatalf("create account: %v", err)
		}
		return org.ID, acct.ID
	}

	pairSvc := pairing.NewService(q)
	pair := func(orgID uuid.UUID) pairing.Credential {
		t.Helper()
		code, err := pairSvc.MintCode(ctx, orgID)
		if err != nil {
			t.Fatalf("mint code: %v", err)
		}
		cred, err := pairSvc.Claim(ctx, code.Code)
		if err != nil {
			t.Fatalf("claim code: %v", err)
		}
		return cred
	}

	orgA, acctA := newAccount("ext-149-a")
	orgB, _ := newAccount("ext-149-b")

	// Device 1 and device 2 are two pairings of the SAME account A; device 3
	// belongs to a different account entirely.
	device1 := pair(orgA)
	device2 := pair(orgA)
	device3 := pair(orgB)
	if device1.MarketplaceAccountID != acctA || device2.MarketplaceAccountID != acctA {
		t.Fatal("both device credentials must be scoped to account A")
	}
	if device1.Credential == device2.Credential {
		t.Fatal("two pairings must yield distinct credentials")
	}

	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(newFakeAuth()), WithCookieSecure(false), WithPairing(pairSvc))

	// ownedTargets is a live credential-scoped READ: 200 while the credential is
	// valid, 401 once the authority has revoked it. It stands in for "the
	// credential still authenticates" without needing a full capture fixture.
	ownedTargets := func(credential string) int {
		req := httptest.NewRequest(http.MethodGet, "/ext/owned-targets", nil)
		req.Header.Set("Authorization", "Bearer "+credential)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	selfRevoke := func(credential string) int {
		req := httptest.NewRequest(http.MethodPost, selfRevokePath, nil)
		req.Header.Set("Authorization", "Bearer "+credential)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Baseline: every credential authenticates before any revocation.
	for name, cred := range map[string]string{
		"device1": device1.Credential, "device2": device2.Credential, "device3": device3.Credential,
	} {
		if got := ownedTargets(cred); got == http.StatusUnauthorized {
			t.Fatalf("%s credential did not authenticate before revocation: %d", name, got)
		}
	}

	// The extension revokes ITSELF using only its Bearer capture credential — no
	// human session cookie is involved anywhere in this call.
	if got := selfRevoke(device1.Credential); got != http.StatusNoContent {
		t.Fatalf("self-revoke = %d, want 204", got)
	}

	// HEADLINE: replaying the copied credential is now refused at the authority.
	if got := ownedTargets(device1.Credential); got != http.StatusUnauthorized {
		t.Fatalf("replay of the self-revoked credential = %d, want 401 — the credential is still live server-side (issue #149)", got)
	}

	// Self-limiting: the same account's OTHER device and the other account are
	// untouched. A self-revoke is never an account-wide (or cross-account) kill.
	if got := ownedTargets(device2.Credential); got == http.StatusUnauthorized {
		t.Fatalf("device2 (same account, different pairing) was revoked too: %d", got)
	}
	if got := ownedTargets(device3.Credential); got == http.StatusUnauthorized {
		t.Fatalf("device3 (different account) was revoked: %d — cross-account revocation", got)
	}

	// Idempotent: repeating the revoke is unambiguous (401 = already invalid at
	// the authority), never a 5xx and never a resurrection.
	for i := 0; i < 3; i++ {
		if got := selfRevoke(device1.Credential); got != http.StatusUnauthorized {
			t.Fatalf("repeat self-revoke #%d = %d, want 401 (already revoked)", i+1, got)
		}
	}
	if got := ownedTargets(device1.Credential); got != http.StatusUnauthorized {
		t.Fatalf("credential resurrected after repeated self-revoke: %d", got)
	}
}
