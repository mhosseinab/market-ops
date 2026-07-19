package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/pairing"
)

// TestListOwnedTargetsCredentialScopedAndIsolated is the #145 credential-scoped
// owned-target READ integration test (EXT-004 / OBS-001). It proves:
//   - no capture credential (no Bearer) → 401 (fail closed);
//   - a revoked/unknown credential → 401;
//   - the response carries ONLY the credential-account's active targets and
//     NEVER another account's target (cross-account IDOR rejected);
//   - a target for a NeedsReview/deactivated identity (active=false) never
//     appears.
func TestListOwnedTargetsCredentialScopedAndIsolated(t *testing.T) {
	pool, q := newIntegrationPool(t)
	ctx := context.Background()

	obsSvc := observation.NewService(pool)
	pairSvc := pairing.NewService(q)

	// seedAccount seeds an org+account+product+variant+identity and syncs targets.
	// When active=false the identity is inserted NeedsReview (never Confirmed), so
	// SyncTargetsFromConfirmed creates no target for it.
	type seeded struct {
		acct         uuid.UUID
		activeNative int64
		credential   string
		org          uuid.UUID
	}
	seedAccount := func(label string) seeded {
		org, err := q.CreateOrganization(ctx, label+"-"+uuid.NewString())
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
			OrganizationID:  org.ID,
			NativeAccountID: "native-" + uuid.NewString(),
			DisplayName:     "Seller " + label,
		})
		if err != nil {
			t.Fatalf("create account: %v", err)
		}
		// A Confirmed active variant → gets a target.
		activeNativeProduct := int64(uuid.New().ID())
		activeNativeVariant := int64(uuid.New().ID())
		prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
			MarketplaceAccountID: acct.ID, NativeProductID: activeNativeProduct, Title: "Widget " + label,
		})
		if err != nil {
			t.Fatalf("upsert product: %v", err)
		}
		variant, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
			MarketplaceAccountID: acct.ID, ProductID: prod.ID,
			NativeVariantID: activeNativeVariant, NativeProductID: activeNativeProduct,
			SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "Widget - " + label,
		})
		if err != nil {
			t.Fatalf("upsert variant: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO market_product_identities
			    (marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active)
			VALUES ($1,$2,$3,$4,'confirmed',true)`,
			acct.ID, variant.ID, activeNativeVariant, activeNativeProduct); err != nil {
			t.Fatalf("insert confirmed identity: %v", err)
		}

		// A NeedsReview (inactive) variant → must NOT get a target.
		nrNativeProduct := int64(uuid.New().ID())
		nrNativeVariant := int64(uuid.New().ID())
		nrProd, err := q.UpsertProduct(ctx, db.UpsertProductParams{
			MarketplaceAccountID: acct.ID, NativeProductID: nrNativeProduct, Title: "NR " + label,
		})
		if err != nil {
			t.Fatalf("upsert NR product: %v", err)
		}
		nrVariant, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
			MarketplaceAccountID: acct.ID, ProductID: nrProd.ID,
			NativeVariantID: nrNativeVariant, NativeProductID: nrNativeProduct,
			SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "NR - " + label,
		})
		if err != nil {
			t.Fatalf("upsert NR variant: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO market_product_identities
			    (marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active)
			VALUES ($1,$2,$3,$4,'needs_review',false)`,
			acct.ID, nrVariant.ID, nrNativeVariant, nrNativeProduct); err != nil {
			t.Fatalf("insert needs_review identity: %v", err)
		}

		created, err := obsSvc.SyncTargetsFromConfirmed(ctx, acct.ID)
		if err != nil {
			t.Fatalf("sync targets: %v", err)
		}
		if len(created) != 1 {
			t.Fatalf("account %s: want 1 target (only the Confirmed active identity), got %d", label, len(created))
		}

		code, err := pairSvc.MintCode(ctx, org.ID)
		if err != nil {
			t.Fatalf("mint code: %v", err)
		}
		cred, err := pairSvc.Claim(ctx, code.Code)
		if err != nil {
			t.Fatalf("claim code: %v", err)
		}
		return seeded{acct: acct.ID, activeNative: activeNativeVariant, credential: cred.Credential, org: org.ID}
	}

	a := seedAccount("A")
	b := seedAccount("B")

	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(newFakeAuth()), WithCookieSecure(false),
		WithObservation(obsSvc), WithPairing(pairSvc))

	get := func(bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/ext/owned-targets", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec
	}

	decodeNative := func(t *testing.T, rec *httptest.ResponseRecorder) map[int64]uuid.UUID {
		t.Helper()
		var body struct {
			Items []struct {
				MarketplaceAccountId uuid.UUID `json:"marketplaceAccountId"`
				NativeVariantId      int64     `json:"nativeVariantId"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
		}
		out := map[int64]uuid.UUID{}
		for _, it := range body.Items {
			out[it.NativeVariantId] = it.MarketplaceAccountId
		}
		return out
	}

	// No credential → fail closed 401.
	if rec := get(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential = %d, want 401 (fail closed)", rec.Code)
	}

	// Unknown/garbage credential → 401.
	if rec := get("not-a-real-credential"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown credential = %d, want 401", rec.Code)
	}

	// Account A's credential returns ONLY A's active target — never B's, never the
	// NeedsReview identity.
	recA := get(a.credential)
	if recA.Code != http.StatusOK {
		t.Fatalf("account A owned-targets = %d, want 200; body=%s", recA.Code, recA.Body.String())
	}
	gotA := decodeNative(t, recA)
	if len(gotA) != 1 {
		t.Fatalf("account A returned %d targets, want exactly 1 (the Confirmed active one)", len(gotA))
	}
	if acct, ok := gotA[a.activeNative]; !ok || acct != a.acct {
		t.Fatalf("account A missing its own active target or wrong account: %v", gotA)
	}
	if _, leaked := gotA[b.activeNative]; leaked {
		t.Fatal("account A response leaked account B's target — cross-account IDOR")
	}

	// Revoke A's credential → next read fails closed with 401.
	if err := pairSvc.RevokeForOrganization(ctx, a.org); err != nil {
		t.Fatalf("revoke A: %v", err)
	}
	if rec := get(a.credential); rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-revocation owned-targets = %d, want 401", rec.Code)
	}

	// B still works and only sees its own target.
	recB := get(b.credential)
	if recB.Code != http.StatusOK {
		t.Fatalf("account B owned-targets = %d, want 200", recB.Code)
	}
	gotB := decodeNative(t, recB)
	if _, leaked := gotB[a.activeNative]; leaked {
		t.Fatal("account B response leaked account A's target — cross-account IDOR")
	}
}
