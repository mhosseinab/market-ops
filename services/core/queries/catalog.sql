-- Catalog + owned-offer sync queries (S10, CAT-001, ACC-004/ACC-005).
-- Every canonical upsert conflicts on the stable DK native identifier so a
-- repeated or REORDERED payload replay updates in place and never inserts a
-- duplicate. The `(xmax = 0) AS inserted` flag distinguishes an INSERT from an
-- UPDATE (Postgres system-column idiom) so the sync run can count new vs changed
-- records without a second round trip.

-- name: UpsertProduct :one
INSERT INTO products (
    marketplace_account_id, native_product_id, title, brand_title, product_url, updated_at
) VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (marketplace_account_id, native_product_id) DO UPDATE SET
    title       = EXCLUDED.title,
    brand_title = EXCLUDED.brand_title,
    product_url = EXCLUDED.product_url,
    updated_at  = now()
RETURNING id, (xmax = 0) AS inserted;

-- name: UpsertVariant :one
INSERT INTO variants (
    marketplace_account_id, product_id, native_variant_id, native_product_id, supplier_code, title, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (marketplace_account_id, native_variant_id) DO UPDATE SET
    product_id        = EXCLUDED.product_id,
    native_product_id = EXCLUDED.native_product_id,
    supplier_code     = EXCLUDED.supplier_code,
    title             = EXCLUDED.title,
    updated_at        = now()
RETURNING id, (xmax = 0) AS inserted;

-- name: UpsertListing :one
INSERT INTO listings (
    marketplace_account_id, variant_id, native_listing_id, selling_channel, product_url, updated_at
) VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (marketplace_account_id, native_listing_id) DO UPDATE SET
    variant_id      = EXCLUDED.variant_id,
    selling_channel = EXCLUDED.selling_channel,
    product_url     = EXCLUDED.product_url,
    updated_at      = now()
RETURNING id, (xmax = 0) AS inserted;

-- name: UpsertOwnedOffer :one
-- Money quarantine (§9.1): price is stored ONLY as raw evidence text; there is no
-- Money/currency column and no conversion path. last_seen_run_id stamps the run
-- that observed this offer for the reconciliation drift pass.
INSERT INTO owned_offers (
    marketplace_account_id, variant_id, native_variant_id,
    price_raw_text, price_raw_value, price_raw_unit,
    seller_stock, warehouse_stock,
    last_seen_run_id, last_seen_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
ON CONFLICT (marketplace_account_id, native_variant_id) DO UPDATE SET
    variant_id       = EXCLUDED.variant_id,
    price_raw_text   = EXCLUDED.price_raw_text,
    price_raw_value  = EXCLUDED.price_raw_value,
    price_raw_unit   = EXCLUDED.price_raw_unit,
    seller_stock     = EXCLUDED.seller_stock,
    warehouse_stock  = EXCLUDED.warehouse_stock,
    last_seen_run_id = EXCLUDED.last_seen_run_id,
    last_seen_at     = now(),
    updated_at       = now()
RETURNING id, (xmax = 0) AS inserted;

-- name: InsertCatalogPayloadSnapshot :exec
-- APPEND-ONLY: the only write path to this table is INSERT. Raw item JSON kept
-- verbatim as evidence (plan §4.7). No UPDATE/DELETE query exists by design.
INSERT INTO catalog_payload_snapshots (
    marketplace_account_id, sync_run_id, native_variant_id, page, payload
) VALUES ($1, $2, $3, $4, $5);

-- name: CreateCatalogSyncRun :one
-- Idempotent in-flight claim (issue #76, PRD §9.1 never-cut). ON CONFLICT DO NOTHING
-- against uq_catalog_sync_runs_inflight (the partial unique index on
-- marketplace_account_id WHERE status IN ('running','queued')) makes this INSERT the
-- atomic serialization point: when a non-terminal run already exists this returns NO
-- row (pgx.ErrNoRows), which the enqueue path treats as "already in-flight" and
-- enqueues nothing. The row is inserted with status='running'; 'queued' is a RESERVED
-- forward state covered by the index predicate but not yet emitted here.
INSERT INTO catalog_sync_runs (marketplace_account_id, kind, status, next_page)
VALUES ($1, $2, 'running', 1)
ON CONFLICT (marketplace_account_id) WHERE status IN ('running', 'queued')
DO NOTHING
RETURNING *;

-- name: GetCatalogSyncRun :one
SELECT * FROM catalog_sync_runs WHERE id = $1;

-- name: GetLatestCatalogSyncRun :one
-- Backs the sync-status view the UI reads (data persisted for a later UI step).
SELECT * FROM catalog_sync_runs
WHERE marketplace_account_id = $1
ORDER BY started_at DESC
LIMIT 1;

-- name: ListRecentCatalogSyncOutcomes :many
-- READ-ONLY durable ordered sync-run state for restart re-derivation of the §20.1
-- connector-sync failure streak (issue #146). Newest-first per account; the
-- telemetry seam (catalog.deriveStreaks) counts the leading consecutive non-success
-- runs since the last completed run. The run id is carried so the seam can re-seed
-- its per-run idempotency guard: a run still being retried after a restart is never
-- double-counted on the live path. Pure SELECT: never mutates a run row.
SELECT id, marketplace_account_id, status, error
FROM catalog_sync_runs
ORDER BY marketplace_account_id, started_at DESC;

-- name: AdvanceCatalogSyncRun :one
-- Persist page progress after a page is fully applied. next_page is the resume
-- cursor an interrupted import continues from; counters accumulate.
UPDATE catalog_sync_runs SET
    next_page        = $2,
    pages_done       = $3,
    total_pages      = $4,
    total_rows       = $5,
    items_seen       = items_seen + $6,
    records_inserted = records_inserted + $7,
    records_updated  = records_updated + $8,
    updated_at       = now()
WHERE id = $1
RETURNING *;

-- name: CompleteCatalogSyncRun :one
UPDATE catalog_sync_runs SET
    status       = 'completed',
    drift_count  = $2,
    completed_at = now(),
    updated_at   = now()
WHERE id = $1
RETURNING *;

-- name: SetCatalogSyncRunError :one
-- Record a retryable error WITHOUT marking the run failed: status stays 'running'
-- and next_page is untouched, so a retry (River backoff) resumes from the same
-- page. Used for transient fetch/apply faults (pagination fault, parser drift).
UPDATE catalog_sync_runs SET
    error      = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: FailCatalogSyncRun :one
UPDATE catalog_sync_runs SET
    status     = 'failed',
    error      = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CountDriftedOwnedOffers :one
-- Reconciliation: owned offers not observed by the given run are drift (missing
-- from the latest full fetch). Reported, never silently deleted.
SELECT count(*) FROM owned_offers
WHERE marketplace_account_id = $1
  AND (last_seen_run_id IS DISTINCT FROM $2);

-- name: CountProducts :one
SELECT count(*) FROM products WHERE marketplace_account_id = $1;

-- name: CountVariants :one
SELECT count(*) FROM variants WHERE marketplace_account_id = $1;

-- name: CountListings :one
SELECT count(*) FROM listings WHERE marketplace_account_id = $1;

-- name: CountOwnedOffers :one
SELECT count(*) FROM owned_offers WHERE marketplace_account_id = $1;

-- name: CountCatalogSnapshots :one
SELECT count(*) FROM catalog_payload_snapshots WHERE marketplace_account_id = $1;
