package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// Kind identifies the two sync modes.
type Kind string

const (
	// KindInitial is the first full import of the catalog (ACC-004).
	KindInitial Kind = "initial"
	// KindIncremental is a reconciled refresh detecting drift (ACC-005).
	KindIncremental Kind = "incremental"
)

// DefaultPageSize is the DK variants page size used when none is configured.
const DefaultPageSize = 50

// Syncer runs the resumable, idempotent catalog sync. It owns the transaction
// boundary: each page is applied atomically (all upserts + snapshots + progress
// advance in one tx) so an interruption between pages leaves a consistent,
// resumable state and never a partially-applied page.
type Syncer struct {
	pool      *pgxpool.Pool
	source    Source
	pageSize  int
	telemetry *SyncTelemetry
}

// NewSyncer builds a Syncer. A pageSize <= 0 uses DefaultPageSize.
func NewSyncer(pool *pgxpool.Pool, source Source, pageSize int) *Syncer {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	return &Syncer{pool: pool, source: source, pageSize: pageSize}
}

// WithTelemetry attaches the process-wide sync-streak tracker so this Syncer
// records its terminal outcome at the authoritative sync lifecycle boundary
// (feeding the §20.1 ConnectorSyncFailureStreak trip wire). A nil tracker leaves
// recording off — the sync path never depends on telemetry being wired.
func (s *Syncer) WithTelemetry(t *SyncTelemetry) *Syncer {
	s.telemetry = t
	return s
}

// recordResult forwards one terminal sync disposition to the shared tracker when
// telemetry is attached. Nil-safe: the sync path is unaffected when it is not.
func (s *Syncer) recordResult(ctx context.Context, account uuid.UUID, d SyncDisposition) {
	if s.telemetry != nil {
		s.telemetry.recordSyncResult(ctx, account, d)
	}
}

// Start creates a sync run and returns its id. The run begins in 'running' with
// next_page=1; the actual work is driven by Resume (directly or from a worker).
func (s *Syncer) Start(ctx context.Context, account uuid.UUID, kind Kind) (uuid.UUID, error) {
	q := db.New(s.pool)
	run, err := q.CreateCatalogSyncRun(ctx, db.CreateCatalogSyncRunParams{
		MarketplaceAccountID: account,
		Kind:                 string(kind),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("catalog: create sync run: %w", err)
	}
	return run.ID, nil
}

// Resume drives a run to completion from its persisted next_page cursor. It is
// safe to call repeatedly: an interrupted import (process crash, or a bounded
// ResumeN in tests) resumes from exactly where it stopped, and a completed run
// is a no-op. A transient fetch/apply fault records the error on the run WITHOUT
// marking it failed and returns the error so the caller (River) can back off and
// retry from the same page — no page is skipped and no duplicate is created.
func (s *Syncer) Resume(ctx context.Context, account, runID uuid.UUID) error {
	return s.resume(ctx, account, runID, 0)
}

// ResumeN processes at most maxPages pages then returns, leaving the run
// 'running'. It exists to deterministically simulate an interrupted import in
// tests; maxPages <= 0 means unbounded (== Resume).
func (s *Syncer) ResumeN(ctx context.Context, account, runID uuid.UUID, maxPages int) error {
	return s.resume(ctx, account, runID, maxPages)
}

func (s *Syncer) resume(ctx context.Context, account, runID uuid.UUID, maxPages int) error {
	q := db.New(s.pool)
	processed := 0
	for {
		run, err := q.GetCatalogSyncRun(ctx, runID)
		if err != nil {
			return fmt.Errorf("catalog: load sync run: %w", err)
		}
		if run.Status == "completed" {
			return nil
		}
		if run.MarketplaceAccountID != account {
			return fmt.Errorf("catalog: sync run %s does not belong to account %s", runID, account)
		}
		if maxPages > 0 && processed >= maxPages {
			return nil
		}

		page := int(run.NextPage)
		pageData, err := s.applyPage(ctx, account, run, page)
		if err != nil {
			// Retryable: persist the error, keep status 'running' and next_page
			// unchanged so a retry resumes from this page. Zero duplicates.
			if _, serr := q.SetCatalogSyncRunError(ctx, db.SetCatalogSyncRunErrorParams{
				ID:    runID,
				Error: err.Error(),
			}); serr != nil {
				return errors.Join(err, fmt.Errorf("catalog: record run error: %w", serr))
			}
			// Authoritative sync-failure boundary: advance the §20.1 consecutive-
			// failure streak by this attempt's disposition (issue #146). Recorded
			// after the durable error is persisted so telemetry mirrors durable state.
			s.recordResult(ctx, account, classifySyncFailure(err))
			return err
		}
		processed++

		total := pageData.TotalPages
		if total < 1 {
			total = page
		}
		if page >= total {
			return s.finish(ctx, q, account, runID)
		}
	}
}

// applyPage fetches one page and applies it in a single transaction: upsert each
// item's Product/Variant/Listing/Owned Offer (idempotent, keyed by native id),
// append the raw payload snapshot, then advance the run's progress cursor. All
// or nothing per page.
func (s *Syncer) applyPage(ctx context.Context, account uuid.UUID, run db.CatalogSyncRun, page int) (connector.VariantPage, error) {
	pageData, err := s.source.FetchVariantsPage(ctx, page, s.pageSize)
	if err != nil {
		return connector.VariantPage{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return connector.VariantPage{}, fmt.Errorf("catalog: begin page tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	var inserted, updated int
	for _, item := range pageData.Items {
		ins, upd, err := applyItem(ctx, q, account, run.ID, page, item)
		if err != nil {
			return connector.VariantPage{}, err
		}
		inserted += ins
		updated += upd
	}

	totalPages := pageData.TotalPages
	if totalPages < 1 {
		totalPages = page
	}
	if _, err := q.AdvanceCatalogSyncRun(ctx, db.AdvanceCatalogSyncRunParams{
		ID:              run.ID,
		NextPage:        int32(page + 1),
		PagesDone:       int32(page),
		TotalPages:      int32(totalPages),
		TotalRows:       int32(pageData.TotalRows),
		ItemsSeen:       int32(len(pageData.Items)),
		RecordsInserted: int32(inserted),
		RecordsUpdated:  int32(updated),
	}); err != nil {
		return connector.VariantPage{}, fmt.Errorf("catalog: advance run: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return connector.VariantPage{}, fmt.Errorf("catalog: commit page tx: %w", err)
	}
	return pageData, nil
}

// applyItem upserts the four canonical entities for one variant item and appends
// its raw payload snapshot. Returns how many of the entity upserts were inserts
// vs updates. Identity is keyed entirely by DK native ids, so order does not
// matter and a replay never duplicates.
func applyItem(ctx context.Context, q *db.Queries, account, runID uuid.UUID, page int, item connector.VariantItem) (inserted, updated int, err error) {
	count := func(ins bool) {
		if ins {
			inserted++
		} else {
			updated++
		}
	}

	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: account,
		NativeProductID:      item.NativeProductID,
		Title:                item.ProductTitle,
		ProductUrl:           item.ProductURL,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert product %d: %w", item.NativeProductID, err)
	}
	count(prod.Inserted)

	variant, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: account,
		ProductID:            prod.ID,
		NativeVariantID:      item.NativeVariantID,
		NativeProductID:      item.NativeProductID,
		SupplierCode:         item.SupplierCode,
		Title:                item.VariantTitle,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert variant %d: %w", item.NativeVariantID, err)
	}
	count(variant.Inserted)

	listing, err := q.UpsertListing(ctx, db.UpsertListingParams{
		MarketplaceAccountID: account,
		VariantID:            variant.ID,
		NativeListingID:      item.NativeListingID,
		SellingChannel:       item.SellingChannel,
		ProductUrl:           item.ProductURL,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert listing %d: %w", item.NativeListingID, err)
	}
	count(listing.Inserted)

	// Money quarantine: build raw evidence, never a Money.
	ev := priceEvidence(item)
	offer, err := q.UpsertOwnedOffer(ctx, db.UpsertOwnedOfferParams{
		MarketplaceAccountID: account,
		VariantID:            variant.ID,
		NativeVariantID:      item.NativeVariantID,
		PriceRawText:         ev.Text,
		PriceRawValue:        ev.Value,
		PriceRawUnit:         ev.Unit,
		SellerStock:          nullableInt8(item.SellerStock),
		WarehouseStock:       nullableInt8(item.WarehouseStock),
		LastSeenRunID:        pgtype.UUID{Bytes: runID, Valid: true},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert owned offer %d: %w", item.NativeVariantID, err)
	}
	count(offer.Inserted)

	if err := q.InsertCatalogPayloadSnapshot(ctx, db.InsertCatalogPayloadSnapshotParams{
		MarketplaceAccountID: account,
		SyncRunID:            runID,
		NativeVariantID:      item.NativeVariantID,
		Page:                 int32(page),
		Payload:              []byte(item.Raw),
	}); err != nil {
		return 0, 0, fmt.Errorf("catalog: snapshot variant %d: %w", item.NativeVariantID, err)
	}
	return inserted, updated, nil
}

// finish runs the reconciliation pass and marks the run completed. Reconciliation
// counts owned offers not observed by this run (drift: missing from the latest
// full fetch); it is reported on the run, never silently deleted.
func (s *Syncer) finish(ctx context.Context, q *db.Queries, account, runID uuid.UUID) error {
	drift, err := q.CountDriftedOwnedOffers(ctx, db.CountDriftedOwnedOffersParams{
		MarketplaceAccountID: account,
		LastSeenRunID:        pgtype.UUID{Bytes: runID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("catalog: reconcile drift: %w", err)
	}
	if _, err := q.CompleteCatalogSyncRun(ctx, db.CompleteCatalogSyncRunParams{
		ID:         runID,
		DriftCount: int32(drift),
	}); err != nil {
		return fmt.Errorf("catalog: complete run: %w", err)
	}
	// Authoritative sync-success boundary: a completed run RESETS the §20.1
	// consecutive-failure streak to zero (issue #146).
	s.recordResult(ctx, account, SyncSuccess)
	return nil
}

// LatestStatus returns the most recent sync run for an account — the data the
// sync-status UI reads (persisted for a later UI/endpoint step). Returns a zero
// run and false when no sync has ever run.
func (s *Syncer) LatestStatus(ctx context.Context, account uuid.UUID) (db.CatalogSyncRun, bool, error) {
	run, err := db.New(s.pool).GetLatestCatalogSyncRun(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CatalogSyncRun{}, false, nil
	}
	if err != nil {
		return db.CatalogSyncRun{}, false, fmt.Errorf("catalog: latest status: %w", err)
	}
	return run, true, nil
}

func nullableInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}
