package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/pairing"
)

// newIntegrationPool connects to DATABASE_URL (schema applied via `task db:reset`).
// Skips when unset so the suite still runs where no Postgres is provisioned.
func newIntegrationPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping pairing/capture integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// TestExtensionCaptureEndToEnd is the S30 integration test (EXT-001/002/004 +
// OBS-008): a paired capture credential uploads a passive capture to the real
// core, an OFFLINE-QUEUE REPLAY (identical body) is DEDUPED so the core holds
// exactly ONE current offer, and after revocation the next upload FAILS CLOSED
// with 401. It exercises the full transport → middleware credential auth →
// observation ingestion → DB seam.
func TestExtensionCaptureEndToEnd(t *testing.T) {
	pool, q := newIntegrationPool(t)
	ctx := context.Background()

	// Seed org + account + product + variant + a Confirmed identity, then sync the
	// observation target (only Confirmed owned identities get a target — EXT-004).
	org, err := q.CreateOrganization(ctx, "ext-s30-"+uuid.NewString())
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
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID, NativeProductID: nativeProduct, Title: "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	variant, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID, ProductID: prod.ID,
		NativeVariantID: nativeVariant, NativeProductID: nativeProduct,
		SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO market_product_identities
		    (marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active)
		VALUES ($1,$2,$3,$4,'confirmed',true)`,
		acct.ID, variant.ID, nativeVariant, nativeProduct); err != nil {
		t.Fatalf("insert confirmed identity: %v", err)
	}

	obsSvc := observation.NewService(pool)
	created, err := obsSvc.SyncTargetsFromConfirmed(ctx, acct.ID)
	if err != nil {
		t.Fatalf("sync targets: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("want 1 target, got %d", len(created))
	}
	targetID := created[0].ID

	// Pair: mint a short-lived code and claim the scoped capture credential — the
	// EXT-001 flow. The extension holds ONLY this capture credential.
	pairSvc := pairing.NewService(q)
	code, err := pairSvc.MintCode(ctx, org.ID)
	if err != nil {
		t.Fatalf("mint code: %v", err)
	}
	cred, err := pairSvc.Claim(ctx, code.Code)
	if err != nil {
		t.Fatalf("claim code: %v", err)
	}
	if cred.MarketplaceAccountID != acct.ID {
		t.Fatalf("credential scoped to %v, want %v", cred.MarketplaceAccountID, acct.ID)
	}

	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(newFakeAuth()), WithCookieSecure(false),
		WithObservation(obsSvc), WithPairing(pairSvc))

	// A fixed body — the offline queue replays a byte-identical payload, which is
	// exactly what makes the dedup key stable (OBS-008).
	body, _ := json.Marshal(map[string]any{
		"marketplaceAccountId": acct.ID,
		"targetId":             targetID,
		"nativeVariantId":      nativeVariant,
		"subRoute":             "passive",
		"sourceType":           "public-web-endpoint",
		"parserVersion":        "dk-product@1.0.0",
		// The real extension always sends its connector version (apps/extension
		// build-capture.ts → CONNECTOR_VERSION). The default parser registry pins
		// dk-product@1.0.0 to exactly this connector (#154), so omitting it makes the
		// capture a registry MISS → quarantined to Unverified. Before the #307
		// quarantine write-guard an Unverified capture still fell through to
		// UpsertObservedOffer and became the offer of record (the overwrite hole #307
		// closes), which is the ONLY reason this end-to-end test used to see a current
		// offer. Send the production connector version so the capture is a registered,
		// schema-valid, identity-valid, verified-confidence extension upload that
		// LEGITIMATELY becomes the one current offer — exercising the real trusted path.
		"connectorVersion":   "market-ops-ext@0.1.0",
		"evidenceRef":        "fixture://ext/product.json",
		"availabilityStatus": "in_stock",
		"capturedAt":         "2026-07-18T10:00:00Z",
		"confidence":         "verified",
		"price": map[string]any{
			"text": "1٬200٬000 ریال", "value": "1200000", "unit": "IRR-rial",
		},
	})

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/observation/capture", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+cred.Credential)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec
	}

	// First upload accepted.
	if rec := post(); rec.Code != http.StatusAccepted {
		t.Fatalf("first capture = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	// Offline-queue replay: identical body → accepted AND deduped.
	rec := post()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("replay capture = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var accepted struct {
		Deduped bool `json:"deduped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode replay body: %v", err)
	}
	if !accepted.Deduped {
		t.Fatal("replayed capture was NOT deduped (OBS-008): the offline queue would create duplicates")
	}

	// Zero duplicates in core: exactly one current observed offer for the target.
	offers, err := obsSvc.ListObservedOffers(ctx, acct.ID)
	if err != nil {
		t.Fatalf("list observed offers: %v", err)
	}
	current := 0
	for _, o := range offers {
		if o.TargetID == targetID && !o.EndedAt.Valid {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("current offers for target = %d, want exactly 1 (replay must not duplicate)", current)
	}

	// Revoke the capture credential (EXT-001/EXT-009 kill switch): the next upload
	// fails closed with 401 — the extension renders a visibly disabled state.
	if err := pairSvc.RevokeForOrganization(ctx, org.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if rec := post(); rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-revocation capture = %d, want 401", rec.Code)
	}
}
