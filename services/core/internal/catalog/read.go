package catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// ErrAccountNotFound is returned when the requested marketplace account is not
// owned by the authenticated organization (cross-account fail-closed). It is
// returned identically for a genuinely-absent account and one owned by a DIFFERENT
// organization, so the response never reveals whether a foreign account exists.
var ErrAccountNotFound = errors.New("catalog: account not found")

// ErrVariantNotFound is returned by GetProduct when the variant does not exist
// within the account (including a foreign variant). Fail closed → the transport
// maps it to 404, never another account's row.
var ErrVariantNotFound = errors.New("catalog: variant not found")

// MappingState is the identity-mapping state of a synced variant surfaced on the
// Products read model (CAT-002). `unmapped` means the variant has NO Market
// Product Identity row at all. Only a Confirmed (and watched) row can ever drive
// an executable recommendation; the read model merely reports the state.
type MappingState string

const (
	MappingConfirmed   MappingState = "confirmed"
	MappingNeedsReview MappingState = "needs_review"
	MappingRejected    MappingState = "rejected"
	MappingObsolete    MappingState = "obsolete"
	MappingUnmapped    MappingState = "unmapped"
)

// Owned-offer unavailability reasons (§15.2 capability gating). A machine-readable
// reason the UI maps to localized copy — never free text.
const (
	ReasonCapabilityNotSupported = "capability_not_supported"
	ReasonNoOwnedOffer           = "no_owned_offer"
)

// OwnedOfferView is the CAPABILITY-GATED owned offer for one variant (PRD §6.1,
// §15.2). Price is raw evidence only (money quarantine §9.1) and is populated ONLY
// when owned_offer_read is Supported AND an owned offer exists; otherwise the view
// carries a reason and no fabricated price/stock. Unknown never enables the price.
type OwnedOfferView struct {
	// Capability is the owned_offer_read capability state (unknown/supported/
	// unsupported/degraded). It is what the UI gates on; only "supported" enables.
	Capability string
	// Present is true only when Capability is Supported AND an owned offer exists.
	Present bool
	// UnavailableReason explains a not-present view (empty when Present).
	UnavailableReason string
	// Price is the raw owned price evidence, populated only when Present.
	Price money.RawAmount
	// HasPrice reports whether Price carries any captured evidence.
	HasPrice bool
	// SellerStock / WarehouseStock are owned stock counts, populated only when
	// Present (nil when DK omitted them or the view is gated).
	SellerStock    *int64
	WarehouseStock *int64
}

// ProductRow is one canonical Products-workspace row (PRD §6.1). It is built from
// the canonical Variant/Product (+ Owned Offer) entities and joined with identity
// mapping state and observation evidence — NEVER synthesized from an observation
// target.
type ProductRow struct {
	VariantID       uuid.UUID
	ProductID       uuid.UUID
	NativeVariantID int64
	NativeProductID int64
	VariantTitle    string
	ProductTitle    string
	SupplierCode    string
	MappingState    MappingState
	Watched         bool
	Owned           OwnedOfferView
	// MarketOffers are the variant's current competitor Observed Offers, surfaced
	// individually WITH identity and ordered deterministically by OfferIdentity
	// ascending (money quarantine forbids numeric price ranking). Empty when the
	// variant is not watched or has no current offer.
	MarketOffers []db.ObservedOffer
}

// ProductPage is one page of canonical Product rows plus the forward cursor.
type ProductPage struct {
	Rows []ProductRow
	// NextCursor is the native_variant_id of the last row when the page was full
	// (more may follow); nil at the end of the account's catalog.
	NextCursor *int64
}

// productReadQuerier is the exact query surface the Products read model needs.
// *db.Queries satisfies it; tests substitute a fake so the gating / no-inference /
// deterministic-ordering / cross-account logic is unit-tested WITHOUT a database.
type productReadQuerier interface {
	GetOrgMarketplaceAccountID(ctx context.Context, arg db.GetOrgMarketplaceAccountIDParams) (uuid.UUID, error)
	ListConnectorCapabilities(ctx context.Context, arg db.ListConnectorCapabilitiesParams) ([]db.ConnectorCapability, error)
	ListCatalogProducts(ctx context.Context, arg db.ListCatalogProductsParams) ([]db.ListCatalogProductsRow, error)
	GetCatalogProductForVariant(ctx context.Context, arg db.GetCatalogProductForVariantParams) (db.GetCatalogProductForVariantRow, error)
	ListObservedOffers(ctx context.Context, marketplaceAccountID uuid.UUID) ([]db.ObservedOffer, error)
}

// ReadService is the account-scoped Products read model (S26, PRD §6.1). It owns
// NO money logic — owned/competitor prices stay raw evidence (money quarantine) —
// and it gates owned-offer data on the owned_offer_read capability (§15.2).
type ReadService struct {
	q productReadQuerier
}

// NewReadService builds a ReadService bound to the pool.
func NewReadService(pool *pgxpool.Pool) *ReadService {
	return &ReadService{q: db.New(pool)}
}

// newReadServiceWith injects a querier (tests only).
func newReadServiceWith(q productReadQuerier) *ReadService {
	return &ReadService{q: q}
}

// DefaultPageLimit / MaxPageLimit bound a page. The default fills a common viewport
// in one fetch; the ceiling matches the §4.5 priority-target envelope.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// ListProducts returns one account-scoped, cursor-paginated page of canonical
// Product rows. It fails CLOSED cross-account: an account not owned by the
// organization yields ErrAccountNotFound BEFORE any catalog row is read, so no
// foreign row can leak. cursor is an exclusive native_variant_id lower bound (0
// for the first page).
func (s *ReadService) ListProducts(ctx context.Context, organizationID, accountID uuid.UUID, cursor int64, limit int32) (ProductPage, error) {
	if err := s.assertOwned(ctx, organizationID, accountID); err != nil {
		return ProductPage{}, err
	}
	limit = clampLimit(limit)

	capState, err := s.ownedOfferCapability(ctx, organizationID, accountID)
	if err != nil {
		return ProductPage{}, err
	}

	rows, err := s.q.ListCatalogProducts(ctx, db.ListCatalogProductsParams{
		MarketplaceAccountID: accountID,
		NativeVariantID:      cursor,
		Limit:                limit,
	})
	if err != nil {
		return ProductPage{}, fmt.Errorf("catalog: list products: %w", err)
	}

	offersByTarget, err := s.marketOffersByTarget(ctx, accountID)
	if err != nil {
		return ProductPage{}, err
	}

	page := ProductPage{Rows: make([]ProductRow, 0, len(rows))}
	for _, r := range rows {
		page.Rows = append(page.Rows, listRowToProductRow(r, capState, offersByTarget))
	}
	// The cursor advances only when the page was full (more rows may follow); a
	// short page is the end of the catalog and reports no next cursor.
	if int32(len(rows)) == limit && len(rows) > 0 {
		last := rows[len(rows)-1].NativeVariantID
		page.NextCursor = &last
	}
	return page, nil
}

// GetProduct returns the single canonical Product row for a variant, scoped to the
// account. It fails CLOSED cross-account (ErrAccountNotFound) and returns
// ErrVariantNotFound for an unknown/foreign variant.
func (s *ReadService) GetProduct(ctx context.Context, organizationID, accountID, variantID uuid.UUID) (ProductRow, error) {
	if err := s.assertOwned(ctx, organizationID, accountID); err != nil {
		return ProductRow{}, err
	}
	capState, err := s.ownedOfferCapability(ctx, organizationID, accountID)
	if err != nil {
		return ProductRow{}, err
	}
	row, err := s.q.GetCatalogProductForVariant(ctx, db.GetCatalogProductForVariantParams{
		MarketplaceAccountID: accountID,
		ID:                   variantID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductRow{}, ErrVariantNotFound
	}
	if err != nil {
		return ProductRow{}, fmt.Errorf("catalog: get product: %w", err)
	}
	offersByTarget, err := s.marketOffersByTarget(ctx, accountID)
	if err != nil {
		return ProductRow{}, err
	}
	return getRowToProductRow(row, capState, offersByTarget), nil
}

// assertOwned resolves the account ONLY when it belongs to organizationID; a
// foreign or unknown account returns ErrAccountNotFound with no side effect and no
// catalog read (cross-account fail-closed).
func (s *ReadService) assertOwned(ctx context.Context, organizationID, accountID uuid.UUID) error {
	_, err := s.q.GetOrgMarketplaceAccountID(ctx, db.GetOrgMarketplaceAccountIDParams{
		ID:             accountID,
		OrganizationID: organizationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("catalog: resolve account owner: %w", err)
	}
	return nil
}

// ownedOfferCapability loads the account's owned_offer_read capability state,
// fail-closed to Unknown when the row is absent (§15.2). Only "supported" enables
// owned-offer data downstream.
func (s *ReadService) ownedOfferCapability(ctx context.Context, organizationID, accountID uuid.UUID) (string, error) {
	rows, err := s.q.ListConnectorCapabilities(ctx, db.ListConnectorCapabilitiesParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	})
	if err != nil {
		return "", fmt.Errorf("catalog: list capabilities: %w", err)
	}
	state := string(connector.Unknown)
	for _, c := range rows {
		if c.Capability == string(connector.OwnedOfferRead) {
			state = c.Status
			break
		}
	}
	return state, nil
}

// marketOffersByTarget loads the account's current Observed Offers and groups them
// by target, each group ordered deterministically by OfferIdentity ascending. This
// is the contract-defined market snapshot: offers surfaced individually WITH
// identity, never collapsed by a mutable updated_at into an anonymous price.
func (s *ReadService) marketOffersByTarget(ctx context.Context, accountID uuid.UUID) (map[uuid.UUID][]db.ObservedOffer, error) {
	offers, err := s.q.ListObservedOffers(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list observed offers: %w", err)
	}
	byTarget := make(map[uuid.UUID][]db.ObservedOffer, len(offers))
	for _, o := range offers {
		byTarget[o.TargetID] = append(byTarget[o.TargetID], o)
	}
	for id := range byTarget {
		grp := byTarget[id]
		sort.SliceStable(grp, func(i, j int) bool { return grp[i].OfferIdentity < grp[j].OfferIdentity })
		byTarget[id] = grp
	}
	return byTarget, nil
}

func clampLimit(limit int32) int32 {
	if limit <= 0 || limit > MaxPageLimit {
		return DefaultPageLimit
	}
	return limit
}

// mappingStateOf maps the raw identity state (empty ⇒ no identity row) onto a
// MappingState. An unrecognized non-empty state is reported verbatim rather than
// silently coerced, so a drift is visible.
func mappingStateOf(raw string) MappingState {
	if raw == "" {
		return MappingUnmapped
	}
	return MappingState(raw)
}

// ownedViewOf builds the capability-gated owned-offer view. Owned price/stock is
// carried ONLY when the capability is Supported AND an owned offer exists; every
// other case fails closed with a reason and NO fabricated data (§15.2).
func ownedViewOf(capState string, present bool, priceText, priceValue, priceUnit string, sellerStock, warehouseStock *int64) OwnedOfferView {
	view := OwnedOfferView{Capability: capState}
	if capState != string(connector.Supported) {
		// Unknown/Unsupported/Degraded never enable dependent data.
		view.UnavailableReason = ReasonCapabilityNotSupported
		return view
	}
	if !present {
		view.UnavailableReason = ReasonNoOwnedOffer
		return view
	}
	view.Present = true
	view.Price = money.NewRawAmount(priceText, priceValue, priceUnit)
	view.HasPrice = !view.Price.IsEmpty()
	view.SellerStock = sellerStock
	view.WarehouseStock = warehouseStock
	return view
}

func listRowToProductRow(r db.ListCatalogProductsRow, capState string, offersByTarget map[uuid.UUID][]db.ObservedOffer) ProductRow {
	return ProductRow{
		VariantID:       r.VariantID,
		ProductID:       r.ProductID,
		NativeVariantID: r.NativeVariantID,
		NativeProductID: r.NativeProductID,
		VariantTitle:    r.VariantTitle,
		ProductTitle:    r.ProductTitle,
		SupplierCode:    r.SupplierCode,
		MappingState:    mappingStateOf(r.MappingState),
		Watched:         r.Watched,
		Owned: ownedViewOf(capState, r.OwnedPresent, r.OwnedPriceText, r.OwnedPriceValue, r.OwnedPriceUnit,
			int8PtrOf(r.OwnedSellerStock), int8PtrOf(r.OwnedWarehouseStock)),
		MarketOffers: marketOffersFor(r.TargetID, offersByTarget),
	}
}

func getRowToProductRow(r db.GetCatalogProductForVariantRow, capState string, offersByTarget map[uuid.UUID][]db.ObservedOffer) ProductRow {
	return ProductRow{
		VariantID:       r.VariantID,
		ProductID:       r.ProductID,
		NativeVariantID: r.NativeVariantID,
		NativeProductID: r.NativeProductID,
		VariantTitle:    r.VariantTitle,
		ProductTitle:    r.ProductTitle,
		SupplierCode:    r.SupplierCode,
		MappingState:    mappingStateOf(r.MappingState),
		Watched:         r.Watched,
		Owned: ownedViewOf(capState, r.OwnedPresent, r.OwnedPriceText, r.OwnedPriceValue, r.OwnedPriceUnit,
			int8PtrOf(r.OwnedSellerStock), int8PtrOf(r.OwnedWarehouseStock)),
		MarketOffers: marketOffersFor(r.TargetID, offersByTarget),
	}
}

// marketOffersFor returns the deterministically-ordered current offers for a
// variant's target. An unwatched variant (no active target) has no target id and
// therefore no market offers — never an inferred one.
func marketOffersFor(targetID pgtype.UUID, offersByTarget map[uuid.UUID][]db.ObservedOffer) []db.ObservedOffer {
	if !targetID.Valid {
		return nil
	}
	return offersByTarget[uuid.UUID(targetID.Bytes)]
}

func int8PtrOf(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	out := v.Int64
	return &out
}
