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
-- connector-sync failure streak (issue #146, bounded per issue #211). Newest-first
-- per account; the telemetry seam (catalog.deriveStreaks) counts the leading
-- consecutive non-success runs since the last completed run. The run id is carried so
-- the seam can re-seed its per-run idempotency guard: a run still being retried after
-- a restart is never double-counted on the live path.
--
-- Only a BOUNDED newest suffix per account is needed to re-derive the streak: the
-- walk stops at the first 'completed' run, so rows older than the last success are
-- discarded anyway. Driving the read from marketplace_accounts with a per-account
-- CROSS JOIN LATERAL + LIMIT makes the work proportional to (#accounts x bound), not
-- to lifetime catalog_sync_runs history — served by
-- idx_catalog_sync_runs_account_started (marketplace_account_id, started_at DESC)
-- from migration 0004. When an account's suffix is exhausted without hitting a
-- 'completed' run the seam fails closed (seeds a lower bound that still trips), so
-- truncation is never presented as an authoritative resolved streak.
-- Pure SELECT: never mutates a run row.
SELECT o.id, o.marketplace_account_id, o.status, o.error
FROM marketplace_accounts a
CROSS JOIN LATERAL (
    SELECT r.id, r.marketplace_account_id, r.status, r.error, r.started_at
    FROM catalog_sync_runs r
    WHERE r.marketplace_account_id = a.id
    ORDER BY r.started_at DESC, r.id DESC
    LIMIT sqlc.arg(bound)::int
) o
ORDER BY o.marketplace_account_id, o.started_at DESC, o.id DESC;

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

-- name: ListCatalogProducts :many
-- Account-scoped, cursor-paginated Products READ MODEL (S26, CAT UI / PRD §6.1).
-- The row SOURCE is the canonical `variants` table (JOINed to its `products`), so
-- every synced variant appears exactly once — a Product/Owned Offer row is NEVER
-- synthesized from an observation target (a target is a dependent projection, not
-- inventory). Identity mapping state, the observation target, and the owned offer
-- are LEFT-joined as ATTRIBUTES of the canonical variant:
--   * ident: the single most-relevant Market Product Identity per variant (active
--     first, else the newest inactive), so rejected/obsolete variants still surface
--     their explicit state; a variant with no identity row reports mapping_state
--     NULL (the handler maps NULL -> 'unmapped').
--   * tgt: an ACTIVE observation target (OBS-001) — watched = (tgt.id IS NOT NULL).
--   * oo: the canonical owned offer (money-quarantined raw price evidence only).
-- CROSS-ACCOUNT FAIL-CLOSED: the driving filter is v.marketplace_account_id = $1,
-- and every LEFT JOIN additionally pins marketplace_account_id to the same account,
-- so a row belonging to another account can never attach or leak. Pagination is by
-- the STABLE native_variant_id key ($2 exclusive lower bound), never a mutable
-- updated_at.
SELECT
    v.id                              AS variant_id,
    v.product_id                      AS product_id,
    v.native_variant_id               AS native_variant_id,
    v.native_product_id               AS native_product_id,
    v.title                           AS variant_title,
    v.supplier_code                   AS supplier_code,
    p.title                           AS product_title,
    COALESCE(ident.state, '')            AS mapping_state,
    tgt.id                            AS target_id,
    (tgt.id IS NOT NULL)::boolean     AS watched,
    (oo.id IS NOT NULL)::boolean      AS owned_present,
    COALESCE(oo.price_raw_text, '')   AS owned_price_text,
    COALESCE(oo.price_raw_value, '')  AS owned_price_value,
    COALESCE(oo.price_raw_unit, '')   AS owned_price_unit,
    oo.seller_stock                   AS owned_seller_stock,
    oo.warehouse_stock                AS owned_warehouse_stock
FROM variants v
JOIN products p
    ON p.id = v.product_id
    AND p.marketplace_account_id = v.marketplace_account_id
LEFT JOIN LATERAL (
    SELECT mpi.state
    FROM market_product_identities mpi
    WHERE mpi.variant_id = v.id
      AND mpi.marketplace_account_id = v.marketplace_account_id
    ORDER BY mpi.active DESC, mpi.updated_at DESC, mpi.id DESC
    LIMIT 1
) ident ON true
LEFT JOIN observation_targets tgt
    ON tgt.variant_id = v.id
    AND tgt.marketplace_account_id = v.marketplace_account_id
    AND tgt.active = true
LEFT JOIN owned_offers oo
    ON oo.variant_id = v.id
    AND oo.marketplace_account_id = v.marketplace_account_id
WHERE v.marketplace_account_id = $1
  AND v.native_variant_id > $2
ORDER BY v.native_variant_id
LIMIT $3;

-- name: GetCatalogProductForVariant :one
-- Single-variant canonical Product row backing Product detail (S26, PRD §6.1).
-- Same canonical projection as ListCatalogProducts, scoped to ONE variant. Both
-- the account AND the variant id must match (cross-account fail-closed): a foreign
-- or unknown variant returns no row (pgx.ErrNoRows -> 404), never another account's
-- data.
SELECT
    v.id                              AS variant_id,
    v.product_id                      AS product_id,
    v.native_variant_id               AS native_variant_id,
    v.native_product_id               AS native_product_id,
    v.title                           AS variant_title,
    v.supplier_code                   AS supplier_code,
    p.title                           AS product_title,
    COALESCE(ident.state, '')            AS mapping_state,
    tgt.id                            AS target_id,
    (tgt.id IS NOT NULL)::boolean     AS watched,
    (oo.id IS NOT NULL)::boolean      AS owned_present,
    COALESCE(oo.price_raw_text, '')   AS owned_price_text,
    COALESCE(oo.price_raw_value, '')  AS owned_price_value,
    COALESCE(oo.price_raw_unit, '')   AS owned_price_unit,
    oo.seller_stock                   AS owned_seller_stock,
    oo.warehouse_stock                AS owned_warehouse_stock
FROM variants v
JOIN products p
    ON p.id = v.product_id
    AND p.marketplace_account_id = v.marketplace_account_id
LEFT JOIN LATERAL (
    SELECT mpi.state
    FROM market_product_identities mpi
    WHERE mpi.variant_id = v.id
      AND mpi.marketplace_account_id = v.marketplace_account_id
    ORDER BY mpi.active DESC, mpi.updated_at DESC, mpi.id DESC
    LIMIT 1
) ident ON true
LEFT JOIN observation_targets tgt
    ON tgt.variant_id = v.id
    AND tgt.marketplace_account_id = v.marketplace_account_id
    AND tgt.active = true
LEFT JOIN owned_offers oo
    ON oo.variant_id = v.id
    AND oo.marketplace_account_id = v.marketplace_account_id
WHERE v.marketplace_account_id = $1
  AND v.id = $2;

-- name: GetVariantListingForDiagnostics :one
-- The READ-ONLY listing/image diagnostics source projection for one variant (S26,
-- LST-001). It reads ONLY already-captured canonical catalog data — the variant +
-- product titles, whether a Listing presence row exists, and the variant's capture
-- time (updated_at) — so a diagnostic can NAME the observed field and its capture
-- moment without any write, generation, or inference. Description/image are NOT
-- projected: the DK Seller connector does not surface that content yet, so those
-- diagnostics are reported not_observed by the read model rather than guessed.
-- CROSS-ACCOUNT FAIL-CLOSED: both the account ($1) and the variant id ($2) must
-- match; a foreign or unknown variant returns no row (pgx.ErrNoRows -> 404).
SELECT
    v.id                              AS variant_id,
    v.native_variant_id               AS native_variant_id,
    v.native_product_id               AS native_product_id,
    v.title                           AS variant_title,
    p.title                           AS product_title,
    (lst.id IS NOT NULL)::boolean     AS listing_present,
    COALESCE(lst.native_listing_id, 0) AS native_listing_id,
    v.updated_at                      AS variant_updated_at
FROM variants v
JOIN products p
    ON p.id = v.product_id
    AND p.marketplace_account_id = v.marketplace_account_id
LEFT JOIN LATERAL (
    SELECT l.id, l.native_listing_id
    FROM listings l
    WHERE l.variant_id = v.id
      AND l.marketplace_account_id = v.marketplace_account_id
    ORDER BY l.updated_at DESC, l.id DESC
    LIMIT 1
) lst ON true
WHERE v.marketplace_account_id = $1
  AND v.id = $2;
