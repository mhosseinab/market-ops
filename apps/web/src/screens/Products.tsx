import type { MessageKey } from "@market-ops/locale";
import { normalizeDigits } from "@market-ops/locale";
import { useQueries } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { reportCatalogTruncation } from "../app/catalogTruncationTelemetry";
import { useLocale, useT } from "../app/i18n";
import { gateway } from "../app/query";
import { AppLink } from "../components/AppLink";
import { MappingBadge, QualityBadge, ReadinessBadge } from "../components/badges";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { FilterChips } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { formatCount } from "../data/format";
import { CATALOG_MAX_PAGES, queryKeys, useAllCatalogProducts } from "../data/hooks";
import type {
  CatalogProductRow,
  MarginReadiness,
  MarginReadinessState,
  ObservedOffer,
} from "../data/types";

// Client-side page size for the READINESS-FILTERED authoritative set. Pagination,
// counts, and page membership are all derived from that set (issue #256), never
// from a single server page.
const PAGE_SIZE = 20;

const READINESS_FILTERS: readonly { id: MarginReadinessState; labelKey: MessageKey }[] = [
  { id: "complete", labelKey: "readiness.complete" },
  { id: "partial", labelKey: "readiness.partial" },
  { id: "stale", labelKey: "readiness.stale" },
  { id: "missing", labelKey: "readiness.missing" },
];

// Readiness is fetched per variant (the P0 contract exposes no batch/paginated
// readiness endpoint), always through a DOCUMENTED bound:
//   - no readiness filter → only the current display page (≤ PAGE_SIZE requests);
//   - readiness filter active → the whole search-filtered candidate set so the
//     filter is applied BEFORE pagination. That set is the accumulated bounded walk,
//     whose worst case is CATALOG_PAGE_LIMIT × CATALOG_MAX_PAGES = 200 × 4 = 800 rows
//     (the §4.5 target ceiling is 200, so production returns the whole catalog in the
//     first page; the 4× is defensive headroom). It is never an unbounded
//     whole-catalog-per-page crawl. Beyond the cap the set is TRUNCATED and fails
//     closed (see `catalogTruncated`), never silently presented as complete.
// A failed or not-yet-loaded readiness value is DEGRADED/unknown — it is never
// treated as a definitive mismatch and so is never silently filtered out.
interface Row {
  readonly product: CatalogProductRow;
  readonly readiness?: MarginReadiness;
  readonly degraded?: boolean;
}

// The contract-defined market snapshot: marketOffers are the variant's current
// competitor Observed Offers, surfaced INDIVIDUALLY and ordered deterministically
// by offerIdentity ascending (money quarantine forbids numeric price ranking).
// The cell shows the first offer WITH its identity and a count when more exist —
// never an anonymous "most recently updated" price.
function primaryOffer(product: CatalogProductRow): ObservedOffer | undefined {
  return product.marketOffers[0];
}

function QualityCell({ product }: { product: CatalogProductRow }) {
  const offer = primaryOffer(product);
  if (!offer) return <LtrToken text="—" />;
  return <QualityBadge state={offer.quality} />;
}

function MarketPriceCell({ product }: { product: CatalogProductRow }) {
  const t = useT();
  const { locale } = useLocale();
  const offer = primaryOffer(product);
  if (!offer) return <LtrToken text="—" />;
  return (
    <span className="inline-badges">
      <LtrToken text={offer.offerIdentity} />
      <LtrToken text={offer.price.text} />
      {product.marketOffers.length > 1 ? (
        <span className="muted">
          {t("products.market.multiple", {
            count: formatCount(product.marketOffers.length, locale),
          })}
        </span>
      ) : null}
    </span>
  );
}

function MappingCell({ product }: { product: CatalogProductRow }) {
  const t = useT();
  return (
    <span className="inline-badges">
      <MappingBadge state={product.mappingState} />
      <span className="muted">{t(product.watched ? "mapping.watched" : "mapping.unwatched")}</span>
    </span>
  );
}

function ReadinessCell({ value, degraded }: { value?: MarginReadiness; degraded?: boolean }) {
  const t = useT();
  if (value) {
    // `data-state` exposes the SERVER-DERIVED readiness verdict (CST-003) as a
    // stable, locale-independent hook so the journey-1 real-core smoke can assert
    // a genuine server-backed value per row (not localized copy). It mirrors the
    // badge's own state — presentation is unchanged.
    return (
      <span data-testid="product-row-readiness" data-state={value.state}>
        <ReadinessBadge state={value.state} />
      </span>
    );
  }
  // A FAILED readiness lookup is an explicit degraded/unknown state (STATE_MATRIX),
  // never a silent "—" that could be mistaken for a definitive verdict.
  if (degraded) {
    return (
      <span data-testid="product-row-readiness" data-state="unknown" className="muted">
        {t("products.readiness.unknown")}
      </span>
    );
  }
  return <LtrToken text="—" />;
}

// Products workspace (design screen 7). Rows are the account's CANONICAL products
// (Product/Variant/Owned Offer), NOT observation targets — every synced variant
// appears with its explicit identity mapping state and whether it is watched. The
// market snapshot uses the contract-defined deterministic offer ordering, and
// margin readiness is fetched per row. Data only — no money is recomputed here.
export function Products() {
  const t = useT();
  const { locale } = useLocale();
  const [search, setSearch] = useState("");
  const [readinessFilter, setReadinessFilter] = useState<MarginReadinessState | null>(null);
  // Zero-based client page index over the AUTHORITATIVE (readiness-filtered) set.
  const [page, setPage] = useState(0);

  // The full account catalog, fetched as a bounded cursor walk (see the hook).
  const productsQuery = useAllCatalogProducts();
  const pagesFetched = productsQuery.data?.pages.length ?? 0;
  const products = useMemo(
    () => productsQuery.data?.pages.flatMap((p) => p.items) ?? [],
    [productsQuery.data],
  );

  // Walk remaining pages until the server has no more OR the documented cap is
  // reached — an explicitly bounded crawl, never open-ended. `pagesFetched` drives
  // the effect: each landed page reliably reruns it to fetch the next.
  const walkComplete = !productsQuery.hasNextPage || pagesFetched >= CATALOG_MAX_PAGES;
  const { hasNextPage, isFetching, fetchNextPage } = productsQuery;
  useEffect(() => {
    if (hasNextPage && !isFetching && pagesFetched < CATALOG_MAX_PAGES) {
      void fetchNextPage();
    }
  }, [hasNextPage, isFetching, pagesFetched, fetchNextPage]);

  // FAIL CLOSED at the cap boundary (issue #256): the walk stopped at CATALOG_MAX_PAGES
  // while the server STILL reports another page. The accumulated set is therefore an
  // INCOMPLETE, non-authoritative slice — count/pageCount below describe only what
  // loaded, never the full catalog. This is a distinct STATE_MATRIX state, never a
  // silent table, and it is emitted once (per transition) so the cap hit is observable.
  const catalogTruncated = hasNextPage === true && pagesFetched >= CATALOG_MAX_PAGES;
  useEffect(() => {
    if (catalogTruncated) {
      reportCatalogTruncation({ pagesFetched, pageCap: CATALOG_MAX_PAGES });
    }
  }, [catalogTruncated, pagesFetched]);

  // Search matches the LTR native identifiers across the WHOLE fetched set (the
  // authoritative candidate set), not one server page. No server-side search param
  // exists in P0; the set is bounded by the §4.5 ceiling, so this is bounded too.
  const candidates = useMemo(() => {
    const normalizedSearch = normalizeDigits(search.trim());
    if (normalizedSearch === "") return products;
    return products.filter((p) =>
      `${p.nativeVariantId} ${p.nativeProductId}`.includes(normalizedSearch),
    );
  }, [products, search]);

  // Readiness fan-out target (see the DOCUMENTED bound above): the whole candidate
  // set when a readiness filter is active (so filtering precedes pagination), else
  // only the current display page. Both are bounded; already-fetched variants stay
  // cached by queryKey so navigating pages never refetches.
  const pageStart = page * PAGE_SIZE;
  const readinessTargets = useMemo(
    () => (readinessFilter ? candidates : candidates.slice(pageStart, pageStart + PAGE_SIZE)),
    [readinessFilter, candidates, pageStart],
  );

  const readinessQueries = useQueries({
    queries: readinessTargets.map((p) => ({
      queryKey: queryKeys.readiness(p.variantId),
      queryFn: async (): Promise<MarginReadiness> => {
        const res = await gateway.GET("/cost/readiness", {
          params: { query: { variantId: p.variantId } },
        });
        if (res.error || !res.data) throw new Error("readiness_failed");
        return res.data;
      },
    })),
  });

  const failedReadiness = readinessQueries.filter((q) => q.isError);

  // variantId → { readiness, degraded } for every fetched variant.
  const readinessByVariant = useMemo(() => {
    const map = new Map<string, { readiness?: MarginReadiness; degraded: boolean }>();
    readinessTargets.forEach((p, i) => {
      const q = readinessQueries[i];
      map.set(p.variantId, { readiness: q?.data, degraded: Boolean(q?.isError) });
    });
    return map;
  }, [readinessTargets, readinessQueries]);

  // The AUTHORITATIVE set: the search-filtered candidates narrowed by readiness
  // BEFORE pagination. A definitive verdict that differs from the filter is
  // excluded; a failed OR not-yet-loaded lookup is NEVER a definitive mismatch, so
  // it is kept (and rendered degraded) rather than silently dropped (issue #256).
  const authoritative = useMemo(() => {
    if (!readinessFilter) return candidates;
    return candidates.filter((p) => {
      const r = readinessByVariant.get(p.variantId);
      if (!r?.readiness) return true;
      return r.readiness.state === readinessFilter;
    });
  }, [candidates, readinessFilter, readinessByVariant]);

  const pageCount = Math.max(1, Math.ceil(authoritative.length / PAGE_SIZE));
  // Clamp the page whenever the authoritative set shrinks (search/filter change or
  // readiness resolving) so navigation never skips or duplicates a row.
  useEffect(() => {
    if (page > pageCount - 1) setPage(pageCount - 1);
  }, [page, pageCount]);
  const currentPage = Math.min(page, pageCount - 1);

  const rows: Row[] = useMemo(() => {
    const start = currentPage * PAGE_SIZE;
    return authoritative.slice(start, start + PAGE_SIZE).map((product) => {
      const r = readinessByVariant.get(product.variantId);
      return { product, readiness: r?.readiness, degraded: r?.degraded };
    });
  }, [authoritative, currentPage, readinessByVariant]);

  const columns: readonly Column<Row>[] = [
    {
      id: "product",
      header: "products.col.product",
      render: (r) => <LtrToken text={String(r.product.nativeProductId)} />,
    },
    {
      id: "sku",
      header: "products.col.sku",
      render: (r) => <LtrToken text={String(r.product.nativeVariantId)} />,
    },
    {
      id: "mapping",
      header: "products.col.mapping",
      render: (r) => <MappingCell product={r.product} />,
    },
    {
      id: "readiness",
      header: "products.col.readiness",
      render: (r) => <ReadinessCell value={r.readiness} degraded={r.degraded} />,
    },
    {
      id: "quality",
      header: "products.col.quality",
      render: (r) => <QualityCell product={r.product} />,
    },
    {
      id: "price",
      header: "products.col.marketPrice",
      render: (r) => <MarketPriceCell product={r.product} />,
    },
    {
      id: "open",
      header: "products.col.open",
      align: "end",
      render: (r) => (
        <AppLink to="/product" search={{ variantId: r.product.variantId }} className="link">
          {t("products.open")}
        </AppLink>
      ),
    },
  ];

  return (
    <div className="screen">
      <div className="toolbar">
        <input
          className="toolbar__search"
          placeholder={t("products.search.placeholder")}
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setPage(0);
          }}
          aria-label={t("products.search.placeholder")}
        />
        <AppLink to="/bulk" className="btn btn--secondary" testId="bulk-entry">
          {t("products.bulkEntry")}
        </AppLink>
      </div>

      <FilterChips
        chips={[
          { id: "all", labelKey: "filter.all", active: readinessFilter === null },
          ...READINESS_FILTERS.map((f) => ({
            id: f.id,
            labelKey: f.labelKey,
            active: readinessFilter === f.id,
          })),
        ]}
        onToggle={(id) => {
          setReadinessFilter(id === "all" ? null : (id as MarginReadinessState));
          setPage(0);
        }}
      />

      <p className="muted">
        {catalogTruncated
          ? t("products.truncated.count", { count: formatCount(authoritative.length, locale) })
          : t("products.count", { count: formatCount(authoritative.length, locale) })}
      </p>

      <ViewState
        pending={productsQuery.isPending || !walkComplete}
        error={productsQuery.isError}
        isEmpty={products.length === 0}
        onRetry={() => void productsQuery.refetch()}
      >
        {catalogTruncated ? (
          <div className="view-error" role="alert" data-testid="products-truncated">
            <p className="view-error__title">{t("products.truncated.title")}</p>
            <p className="view-error__body">{t("products.truncated.body")}</p>
          </div>
        ) : null}

        {failedReadiness.length > 0 ? (
          <div className="view-error" role="alert" data-testid="products-readiness-error">
            <p className="view-error__title">{t("products.readiness.error.title")}</p>
            <p className="view-error__body">
              {t("products.readiness.error.body", {
                count: formatCount(failedReadiness.length, locale),
              })}
            </p>
            <button
              type="button"
              className="btn btn--secondary"
              onClick={() => {
                for (const q of failedReadiness) void q.refetch();
              }}
            >
              {t("action.retry")}
            </button>
          </div>
        ) : null}

        <DataTable columns={columns} rows={rows} rowKey={(r) => r.product.variantId} />

        <nav className="pagination" aria-label={t("products.pagination.next")}>
          <button
            type="button"
            className="btn btn--secondary"
            data-testid="products-prev-page"
            disabled={currentPage === 0}
            onClick={() => setPage((p) => Math.max(0, p - 1))}
          >
            {t("products.pagination.prev")}
          </button>
          <span className="muted" data-testid="products-page-indicator">
            {t("products.pagination.page", {
              page: formatCount(currentPage + 1, locale),
              total: formatCount(pageCount, locale),
            })}
          </span>
          <button
            type="button"
            className="btn btn--secondary"
            data-testid="products-next-page"
            disabled={currentPage >= pageCount - 1}
            onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
          >
            {t("products.pagination.next")}
          </button>
        </nav>
      </ViewState>
    </div>
  );
}
