package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// fakeReadQuerier is an in-memory productReadQuerier so the Products read-model
// logic (cross-account gating, capability gating, no-inference, deterministic
// market snapshot, pagination) is unit-tested WITHOUT a database.
type fakeReadQuerier struct {
	owned        map[uuid.UUID]uuid.UUID // accountID -> orgID that owns it
	capabilities []db.ConnectorCapability
	rows         []db.ListCatalogProductsRow
	single       map[uuid.UUID]db.GetCatalogProductForVariantRow
	offers       []db.ObservedOffer

	listProductsCalls int
}

func (f *fakeReadQuerier) GetOrgMarketplaceAccountID(_ context.Context, arg db.GetOrgMarketplaceAccountIDParams) (uuid.UUID, error) {
	if org, ok := f.owned[arg.ID]; ok && org == arg.OrganizationID {
		return arg.ID, nil
	}
	return uuid.Nil, pgx.ErrNoRows
}

func (f *fakeReadQuerier) ListConnectorCapabilities(_ context.Context, _ db.ListConnectorCapabilitiesParams) ([]db.ConnectorCapability, error) {
	return f.capabilities, nil
}

func (f *fakeReadQuerier) ListCatalogProducts(_ context.Context, arg db.ListCatalogProductsParams) ([]db.ListCatalogProductsRow, error) {
	f.listProductsCalls++
	out := make([]db.ListCatalogProductsRow, 0, len(f.rows))
	for _, r := range f.rows {
		if r.NativeVariantID > arg.NativeVariantID {
			out = append(out, r)
		}
	}
	if int32(len(out)) > arg.Limit {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakeReadQuerier) GetCatalogProductForVariant(_ context.Context, arg db.GetCatalogProductForVariantParams) (db.GetCatalogProductForVariantRow, error) {
	if row, ok := f.single[arg.ID]; ok {
		return row, nil
	}
	return db.GetCatalogProductForVariantRow{}, pgx.ErrNoRows
}

func (f *fakeReadQuerier) ListObservedOffers(_ context.Context, _ uuid.UUID) ([]db.ObservedOffer, error) {
	return f.offers, nil
}

func supportedOwnedOffer() []db.ConnectorCapability {
	return []db.ConnectorCapability{
		{Capability: string(connector.CatalogRead), Status: string(connector.Supported)},
		{Capability: string(connector.OwnedOfferRead), Status: string(connector.Supported)},
	}
}

// Negative test (never-cut, cross-account): an account the org does not own yields
// ErrAccountNotFound and NEVER reads a catalog row, so a row from account B can
// never be returned for account A's request.
func TestListProducts_CrossAccountFailsClosed(t *testing.T) {
	org := uuid.New()
	ownedAccount := uuid.New()
	foreignAccount := uuid.New()
	f := &fakeReadQuerier{
		owned: map[uuid.UUID]uuid.UUID{ownedAccount: org},
		rows:  []db.ListCatalogProductsRow{{VariantID: uuid.New(), NativeVariantID: 1}},
	}
	svc := newReadServiceWith(f)

	_, err := svc.ListProducts(context.Background(), org, foreignAccount, 0, 50)
	if err == nil {
		t.Fatal("expected cross-account request to fail closed, got nil error")
	}
	if err != ErrAccountNotFound {
		t.Fatalf("want ErrAccountNotFound, got %v", err)
	}
	if f.listProductsCalls != 0 {
		t.Fatalf("cross-account request must not read catalog rows; ListCatalogProducts called %d times", f.listProductsCalls)
	}
}

// Negative test (never-cut, capability gating): owned_offer_read Unknown must yield
// the UI-reason path and NEVER fabricated owned data — even when an owned offer row
// physically exists. Unknown never enables dependent UI.
func TestListProducts_OwnedOfferCapabilityUnknownGated(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	f := &fakeReadQuerier{
		owned: map[uuid.UUID]uuid.UUID{account: org},
		// No owned_offer_read capability row at all ⇒ fail closed to Unknown.
		capabilities: []db.ConnectorCapability{{Capability: string(connector.CatalogRead), Status: string(connector.Supported)}},
		rows: []db.ListCatalogProductsRow{{
			VariantID: uuid.New(), NativeVariantID: 10, OwnedPresent: true,
			OwnedPriceText: "1,000", OwnedPriceValue: "1000", OwnedPriceUnit: "T",
		}},
	}
	svc := newReadServiceWith(f)

	page, err := svc.ListProducts(context.Background(), org, account, 0, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	owned := page.Rows[0].Owned
	if owned.Capability != string(connector.Unknown) {
		t.Fatalf("want capability unknown, got %q", owned.Capability)
	}
	if owned.Present {
		t.Fatal("Unknown capability must never present owned data")
	}
	if owned.HasPrice || !owned.Price.IsEmpty() {
		t.Fatalf("Unknown capability must not fabricate price, got %+v", owned.Price)
	}
	if owned.UnavailableReason != ReasonCapabilityNotSupported {
		t.Fatalf("want reason %q, got %q", ReasonCapabilityNotSupported, owned.UnavailableReason)
	}
}

// Supported owned-offer capability + a present owned offer renders the raw price
// evidence (money quarantine: verbatim tokens, never a Money).
func TestListProducts_OwnedOfferSupportedRenders(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	seller := int64(7)
	f := &fakeReadQuerier{
		owned:        map[uuid.UUID]uuid.UUID{account: org},
		capabilities: supportedOwnedOffer(),
		rows: []db.ListCatalogProductsRow{{
			VariantID: uuid.New(), NativeVariantID: 10, OwnedPresent: true,
			OwnedPriceText: "1,000", OwnedPriceValue: "1000", OwnedPriceUnit: "T",
			OwnedSellerStock: pgtype.Int8{Int64: seller, Valid: true},
		}},
	}
	svc := newReadServiceWith(f)

	page, err := svc.ListProducts(context.Background(), org, account, 0, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	owned := page.Rows[0].Owned
	if !owned.Present || !owned.HasPrice {
		t.Fatalf("supported owned offer must render, got %+v", owned)
	}
	if owned.Price.Value != "1000" || owned.Price.Unit != "T" {
		t.Fatalf("raw price evidence not preserved: %+v", owned.Price)
	}
	if owned.UnavailableReason != "" {
		t.Fatalf("present owned offer must carry no reason, got %q", owned.UnavailableReason)
	}
	if owned.SellerStock == nil || *owned.SellerStock != seller {
		t.Fatalf("seller stock not surfaced: %v", owned.SellerStock)
	}
}

// Supported capability but NO owned offer ⇒ reason no_owned_offer (not fabricated).
func TestListProducts_SupportedButNoOwnedOffer(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	f := &fakeReadQuerier{
		owned:        map[uuid.UUID]uuid.UUID{account: org},
		capabilities: supportedOwnedOffer(),
		rows:         []db.ListCatalogProductsRow{{VariantID: uuid.New(), NativeVariantID: 10, OwnedPresent: false}},
	}
	svc := newReadServiceWith(f)

	page, err := svc.ListProducts(context.Background(), org, account, 0, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	owned := page.Rows[0].Owned
	if owned.Present {
		t.Fatal("absent owned offer must not present")
	}
	if owned.UnavailableReason != ReasonNoOwnedOffer {
		t.Fatalf("want reason %q, got %q", ReasonNoOwnedOffer, owned.UnavailableReason)
	}
}

// No-inference (never-cut): a synced variant with no identity and no target still
// appears as a canonical row built from VARIANT fields — never synthesized from an
// observation target, and no market offer is inferred for an unwatched variant.
func TestListProducts_NoInferenceFromObservationTarget(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	variant := uuid.New()
	f := &fakeReadQuerier{
		owned:        map[uuid.UUID]uuid.UUID{account: org},
		capabilities: supportedOwnedOffer(),
		rows: []db.ListCatalogProductsRow{{
			VariantID: variant, NativeVariantID: 42, NativeProductID: 99,
			VariantTitle: "V", ProductTitle: "P", SupplierCode: "SKU-1",
			MappingState: "", // no identity row
			Watched:      false,
			TargetID:     pgtype.UUID{}, // no active target
		}},
		// An observed offer exists but for a DIFFERENT target — must not attach.
		offers: []db.ObservedOffer{{TargetID: uuid.New(), OfferIdentity: "x"}},
	}
	svc := newReadServiceWith(f)

	page, err := svc.ListProducts(context.Background(), org, account, 0, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	row := page.Rows[0]
	if row.VariantID != variant || row.NativeVariantID != 42 || row.SupplierCode != "SKU-1" {
		t.Fatalf("row must be built from canonical variant fields, got %+v", row)
	}
	if row.MappingState != MappingUnmapped {
		t.Fatalf("no identity ⇒ unmapped, got %q", row.MappingState)
	}
	if len(row.MarketOffers) != 0 {
		t.Fatalf("unwatched variant must have no inferred market offers, got %d", len(row.MarketOffers))
	}
}

// Deterministic market snapshot: multiple competitor offers for one target are
// ordered by OfferIdentity ascending (a stable, non-money key) — never by a mutable
// updated_at "most recent".
func TestListProducts_DeterministicMarketSnapshot(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	target := uuid.New()
	f := &fakeReadQuerier{
		owned:        map[uuid.UUID]uuid.UUID{account: org},
		capabilities: supportedOwnedOffer(),
		rows: []db.ListCatalogProductsRow{{
			VariantID: uuid.New(), NativeVariantID: 10,
			TargetID: pgtype.UUID{Bytes: target, Valid: true},
			Watched:  true, MappingState: string(MappingConfirmed),
		}},
		// Deliberately UNSORTED input.
		offers: []db.ObservedOffer{
			{TargetID: target, OfferIdentity: "z-seller"},
			{TargetID: target, OfferIdentity: "a-seller"},
			{TargetID: target, OfferIdentity: "m-seller"},
		},
	}
	svc := newReadServiceWith(f)

	page, err := svc.ListProducts(context.Background(), org, account, 0, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := page.Rows[0].MarketOffers
	want := []string{"a-seller", "m-seller", "z-seller"}
	if len(got) != len(want) {
		t.Fatalf("want %d offers, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i].OfferIdentity != want[i] {
			t.Fatalf("offer %d: want identity %q, got %q (non-deterministic order)", i, want[i], got[i].OfferIdentity)
		}
	}
}

// Mapping states surface verbatim for every synced variant (confirmed / needs_review
// / rejected / obsolete / unmapped), plus the watched flag.
func TestListProducts_MappingStatesExplicit(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	f := &fakeReadQuerier{
		owned:        map[uuid.UUID]uuid.UUID{account: org},
		capabilities: supportedOwnedOffer(),
		rows: []db.ListCatalogProductsRow{
			{VariantID: uuid.New(), NativeVariantID: 1, MappingState: string(MappingConfirmed), Watched: true},
			{VariantID: uuid.New(), NativeVariantID: 2, MappingState: string(MappingConfirmed), Watched: false},
			{VariantID: uuid.New(), NativeVariantID: 3, MappingState: string(MappingNeedsReview)},
			{VariantID: uuid.New(), NativeVariantID: 4, MappingState: string(MappingRejected)},
			{VariantID: uuid.New(), NativeVariantID: 5, MappingState: string(MappingObsolete)},
			{VariantID: uuid.New(), NativeVariantID: 6, MappingState: ""},
		},
	}
	svc := newReadServiceWith(f)

	page, err := svc.ListProducts(context.Background(), org, account, 0, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []MappingState{
		MappingConfirmed, MappingConfirmed, MappingNeedsReview,
		MappingRejected, MappingObsolete, MappingUnmapped,
	}
	for i, w := range want {
		if page.Rows[i].MappingState != w {
			t.Fatalf("row %d: want %q, got %q", i, w, page.Rows[i].MappingState)
		}
	}
	// Confirmed-but-inactive-target row is watched=false (the inactive-target case).
	if page.Rows[1].Watched {
		t.Fatal("confirmed row with inactive target must be watched=false")
	}
	if !page.Rows[0].Watched {
		t.Fatal("confirmed row with active target must be watched=true")
	}
}

// Pagination cursor: a FULL page reports the last row's native_variant_id as the
// next cursor; a short (final) page reports nil.
func TestListProducts_PaginationCursor(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	rows := make([]db.ListCatalogProductsRow, 0, 5)
	for i := int64(1); i <= 5; i++ {
		rows = append(rows, db.ListCatalogProductsRow{VariantID: uuid.New(), NativeVariantID: i})
	}
	f := &fakeReadQuerier{
		owned:        map[uuid.UUID]uuid.UUID{account: org},
		capabilities: supportedOwnedOffer(),
		rows:         rows,
	}
	svc := newReadServiceWith(f)

	// Full page (limit 2) ⇒ next cursor = 2.
	page, err := svc.ListProducts(context.Background(), org, account, 0, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.NextCursor == nil || *page.NextCursor != 2 {
		t.Fatalf("full page must carry next cursor 2, got %v", page.NextCursor)
	}
	// Continue from cursor 2 with a large limit ⇒ 3 rows (3,4,5), short page, nil cursor.
	page2, err := svc.ListProducts(context.Background(), org, account, *page.NextCursor, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page2.Rows) != 3 || page2.Rows[0].NativeVariantID != 3 {
		t.Fatalf("cursor continuation wrong: %+v", page2.Rows)
	}
	if page2.NextCursor != nil {
		t.Fatalf("final short page must report nil cursor, got %v", *page2.NextCursor)
	}
}

// GetProduct fails closed cross-account and for an unknown variant.
func TestGetProduct_FailClosed(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	variant := uuid.New()
	f := &fakeReadQuerier{
		owned:        map[uuid.UUID]uuid.UUID{account: org},
		capabilities: supportedOwnedOffer(),
		single: map[uuid.UUID]db.GetCatalogProductForVariantRow{
			variant: {VariantID: variant, NativeVariantID: 1, MappingState: string(MappingConfirmed)},
		},
	}
	svc := newReadServiceWith(f)

	// Cross-account.
	if _, err := svc.GetProduct(context.Background(), org, uuid.New(), variant); err != ErrAccountNotFound {
		t.Fatalf("cross-account GetProduct: want ErrAccountNotFound, got %v", err)
	}
	// Unknown variant within the owned account.
	if _, err := svc.GetProduct(context.Background(), org, account, uuid.New()); err != ErrVariantNotFound {
		t.Fatalf("unknown variant: want ErrVariantNotFound, got %v", err)
	}
	// Known variant resolves.
	row, err := svc.GetProduct(context.Background(), org, account, variant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.VariantID != variant {
		t.Fatalf("want variant %s, got %s", variant, row.VariantID)
	}
}
