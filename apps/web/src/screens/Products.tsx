import type { MessageKey } from "@market-ops/locale";
import { normalizeDigits } from "@market-ops/locale";
import { useQueries } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { gateway } from "../app/query";
import { AppLink } from "../components/AppLink";
import { MappingBadge, QualityBadge, ReadinessBadge } from "../components/badges";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { FilterChips } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { formatCount } from "../data/format";
import { queryKeys, useCatalogProducts } from "../data/hooks";
import type {
  CatalogProductRow,
  MarginReadiness,
  MarginReadinessState,
  ObservedOffer,
} from "../data/types";

const READINESS_FILTERS: readonly { id: MarginReadinessState; labelKey: MessageKey }[] = [
  { id: "complete", labelKey: "readiness.complete" },
  { id: "partial", labelKey: "readiness.partial" },
  { id: "stale", labelKey: "readiness.stale" },
  { id: "missing", labelKey: "readiness.missing" },
];

// Readiness is fetched per variant (the P0 contract exposes no batch/paginated
// readiness endpoint). The Products read model is SERVER-paginated, so the fan-out
// is naturally bounded to the current page's rows — never the whole catalog.
interface Row {
  readonly product: CatalogProductRow;
  readonly readiness?: MarginReadiness;
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

function ReadinessCell({ value }: { value?: MarginReadiness }) {
  if (!value) return <LtrToken text="—" />;
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

// Products workspace (design screen 7). Rows are the account's CANONICAL products
// (Product/Variant/Owned Offer), NOT observation targets — every synced variant
// appears with its explicit identity mapping state and whether it is watched. The
// market snapshot uses the contract-defined deterministic offer ordering, and
// margin readiness is fetched per row. Data only — no money is recomputed here.
export function Products() {
  const t = useT();
  const { locale } = useLocale();
  // Cursor stack: each entry is the cursor used to fetch a page; [] is the first
  // page. `push` on Next (server's nextCursor), `pop` on Previous.
  const [cursorStack, setCursorStack] = useState<string[]>([]);
  const currentCursor =
    cursorStack.length > 0 ? (cursorStack[cursorStack.length - 1] ?? null) : null;
  const productsQuery = useCatalogProducts(currentCursor);
  const [search, setSearch] = useState("");
  const [readinessFilter, setReadinessFilter] = useState<MarginReadinessState | null>(null);

  const products = useMemo(() => productsQuery.data?.items ?? [], [productsQuery.data]);
  const nextCursor = productsQuery.data?.nextCursor ?? null;

  // Search matches the LTR native identifiers on the current page only. The P0
  // read model has no server-side search param, and the page is already bounded
  // by the cursor, so this narrows the LOADED page (carry-forward, documented).
  const filtered = useMemo(() => {
    const normalizedSearch = normalizeDigits(search.trim());
    if (normalizedSearch === "") return products;
    return products.filter((p) =>
      `${p.nativeVariantId} ${p.nativeProductId}`.includes(normalizedSearch),
    );
  }, [products, search]);

  // Fan out readiness for the current page's variants only (server pagination
  // already bounds the page). Already-fetched variants stay cached by queryKey.
  const readinessQueries = useQueries({
    queries: filtered.map((p) => ({
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

  const rows: Row[] = useMemo(() => {
    return filtered
      .map((product, i) => ({ product, readiness: readinessQueries[i]?.data }))
      .filter((row) => !readinessFilter || row.readiness?.state === readinessFilter);
  }, [filtered, readinessQueries, readinessFilter]);

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
      render: (r) => <ReadinessCell value={r.readiness} />,
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

  const pageNumber = cursorStack.length + 1;

  return (
    <div className="screen">
      <div className="toolbar">
        <input
          className="toolbar__search"
          placeholder={t("products.search.placeholder")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
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
        }}
      />

      <p className="muted">
        {t("products.count", { count: formatCount(filtered.length, locale) })}
      </p>

      <ViewState
        pending={productsQuery.isPending}
        error={productsQuery.isError}
        isEmpty={products.length === 0}
        onRetry={() => void productsQuery.refetch()}
      >
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
            disabled={cursorStack.length === 0}
            onClick={() => setCursorStack((s) => s.slice(0, -1))}
          >
            {t("products.pagination.prev")}
          </button>
          <span className="muted" data-testid="products-page-indicator">
            {formatCount(pageNumber, locale)}
          </span>
          <button
            type="button"
            className="btn btn--secondary"
            data-testid="products-next-page"
            disabled={nextCursor === null}
            onClick={() => {
              if (nextCursor !== null) setCursorStack((s) => [...s, nextCursor]);
            }}
          >
            {t("products.pagination.next")}
          </button>
        </nav>
      </ViewState>
    </div>
  );
}
