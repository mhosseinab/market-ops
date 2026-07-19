import type { MessageKey } from "@market-ops/locale";
import { normalizeDigits } from "@market-ops/locale";
import { useQueries } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { gateway } from "../app/query";
import { AppLink } from "../components/AppLink";
import { QualityBadge, ReadinessBadge } from "../components/badges";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { FilterChips } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { formatCount } from "../data/format";
import { queryKeys, useObservationTargets, useObservedOffers } from "../data/hooks";
import type {
  MarginReadiness,
  MarginReadinessState,
  ObservationTarget,
  ObservedOffer,
} from "../data/types";

const TIER_LABEL: Record<ObservationTarget["tier"], MessageKey> = {
  priority: "tier.priority",
  standard: "tier.standard",
  background: "tier.background",
};

const READINESS_FILTERS: readonly { id: MarginReadinessState; labelKey: MessageKey }[] = [
  { id: "complete", labelKey: "readiness.complete" },
  { id: "partial", labelKey: "readiness.partial" },
  { id: "stale", labelKey: "readiness.stale" },
  { id: "missing", labelKey: "readiness.missing" },
];

// Readiness is fetched per variant (the P0 contract exposes no batch/paginated
// readiness endpoint). At the 5,000-SKU account envelope (PRD §17.1) a per-row
// fan-out would fire thousands of concurrent /cost/readiness calls, blowing the
// §17.2 P95<2s common-view target and amplifying transient failures. We therefore
// bound the fan-out to ONE PAGE: readiness is only requested for the current
// page's targets. 50 is a deliberate, bounded batch — large enough to fill the
// common viewport in one fetch, small enough to keep concurrency and P95 in check.
const PAGE_SIZE = 50;

interface Row {
  readonly target: ObservationTarget;
  readonly offer?: ObservedOffer;
  readonly readiness?: MarginReadiness;
}

// Cell renderers as named components so each table `render` arrow returns a
// single element (keeps copy-lint's JSX-text heuristic and biome's line-break
// style from fighting over inline ternaries). Em dash is a non-linguistic glyph.
function ReadinessCell({ value }: { value?: MarginReadiness }) {
  if (!value) return <LtrToken text="—" />;
  return <ReadinessBadge state={value.state} />;
}

function QualityCell({ offer }: { offer?: ObservedOffer }) {
  if (!offer) return <LtrToken text="—" />;
  return <QualityBadge state={offer.quality} />;
}

function PriceCell({ offer }: { offer?: ObservedOffer }) {
  if (!offer) return <LtrToken text="—" />;
  return <LtrToken text={offer.price.text} />;
}

// Products workspace (design screen 7). Rows are the account's observation
// targets — the observable products — joined with the current observed offer
// (market quality + raw price evidence) and per-variant margin readiness. Search
// matches the LTR technical identifiers (digits normalized at the input
// boundary); readiness filter chips narrow the list. Data only — no money or
// readiness is recomputed here.
export function Products() {
  const t = useT();
  const { locale } = useLocale();
  const targetsQuery = useObservationTargets();
  const offersQuery = useObservedOffers();
  const [search, setSearch] = useState("");
  const [readinessFilter, setReadinessFilter] = useState<MarginReadinessState | null>(null);
  const [page, setPage] = useState(0);

  const targets = useMemo(() => targetsQuery.data?.items ?? [], [targetsQuery.data]);

  // 1) Filter by the search box FIRST — it matches the LTR native identifiers on
  // the target itself (no readiness needed), so search narrows the full set before
  // any readiness fan-out. Digits normalize at the input boundary.
  const filteredTargets = useMemo(() => {
    const normalizedSearch = normalizeDigits(search.trim());
    if (normalizedSearch === "") return targets;
    return targets.filter((tg) =>
      `${tg.nativeVariantId} ${tg.nativeProductId}`.includes(normalizedSearch),
    );
  }, [targets, search]);

  const pageCount = Math.max(1, Math.ceil(filteredTargets.length / PAGE_SIZE));

  // 2) Paginate the filtered set. Clamp the page defensively (data can shrink
  // between renders) so the slice is always in range.
  const safePage = Math.min(page, pageCount - 1);
  const pageTargets = useMemo(
    () => filteredTargets.slice(safePage * PAGE_SIZE, safePage * PAGE_SIZE + PAGE_SIZE),
    [filteredTargets, safePage],
  );

  // 3) Fan out readiness for the CURRENT PAGE ONLY — this bounds concurrent
  // /cost/readiness calls to at most PAGE_SIZE per view. Already-fetched pages stay
  // cached by queryKey, so paging back is free and paging forward fetches only the
  // next visible batch. Do NOT widen the QueryClient `retry: 1` default here.
  const readinessQueries = useQueries({
    queries: pageTargets.map((tg) => ({
      queryKey: queryKeys.readiness(tg.variantId),
      queryFn: async (): Promise<MarginReadiness> => {
        const res = await gateway.GET("/cost/readiness", {
          params: { query: { variantId: tg.variantId } },
        });
        if (res.error || !res.data) throw new Error("readiness_failed");
        return res.data;
      },
    })),
  });

  // Aggregate the page's readiness error states for the "Partial batch failure"
  // section state (STATE_MATRIX). A single failed row must not throw away the
  // table; the successful rows still render and only the failed queries retry.
  const failedReadiness = readinessQueries.filter((q) => q.isError);

  const offerByTarget = useMemo(() => {
    const map = new Map<string, ObservedOffer>();
    for (const o of offersQuery.data?.items ?? []) {
      if (!map.has(o.targetId)) map.set(o.targetId, o);
    }
    return map;
  }, [offersQuery.data]);

  const rows: Row[] = useMemo(() => {
    return (
      pageTargets
        .map((target, i) => ({
          target,
          offer: offerByTarget.get(target.id),
          readiness: readinessQueries[i]?.data,
        }))
        // The readiness filter is CARRY-FORWARD: the P0 gateway exposes no
        // server-side readiness filter param, and readiness is only loaded for the
        // current page (bounded fan-out), so the chip narrows the LOADED page rows
        // only — it is not a whole-catalog filter (carry-forward for
        // api_data_contracts, mirroring BulkApproval.tsx's documented gaps).
        .filter((row) => !readinessFilter || row.readiness?.state === readinessFilter)
    );
  }, [pageTargets, offerByTarget, readinessQueries, readinessFilter]);

  const columns: readonly Column<Row>[] = [
    {
      id: "product",
      header: "products.col.product",
      // Native IDs are technical identifiers, not quantities: raw + LTR-isolated
      // (no grouping / digit conversion). Displayed value == the search haystack.
      render: (r) => <LtrToken text={String(r.target.nativeProductId)} />,
    },
    {
      id: "sku",
      header: "products.col.sku",
      render: (r) => <LtrToken text={String(r.target.nativeVariantId)} />,
    },
    {
      id: "tier",
      header: "products.col.tier",
      render: (r) => t(TIER_LABEL[r.target.tier]),
    },
    {
      id: "readiness",
      header: "products.col.readiness",
      render: (r) => <ReadinessCell value={r.readiness} />,
    },
    {
      id: "quality",
      header: "products.col.quality",
      render: (r) => <QualityCell offer={r.offer} />,
    },
    {
      id: "price",
      header: "products.col.marketPrice",
      render: (r) => <PriceCell offer={r.offer} />,
    },
    {
      id: "open",
      header: "products.col.open",
      align: "end",
      render: (r) => (
        <AppLink to="/product" search={{ variantId: r.target.variantId }} className="link">
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
        {t("products.count", { count: formatCount(filteredTargets.length, locale) })}
      </p>

      <ViewState
        pending={targetsQuery.isPending}
        error={targetsQuery.isError}
        isEmpty={targets.length === 0}
        onRetry={() => void targetsQuery.refetch()}
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
                // Retry ONLY the failed readiness queries — no whole-table refetch,
                // no retry storm (the QueryClient `retry: 1` default is unchanged).
                for (const q of failedReadiness) void q.refetch();
              }}
            >
              {t("action.retry")}
            </button>
          </div>
        ) : null}

        <DataTable columns={columns} rows={rows} rowKey={(r) => r.target.id} />

        <nav
          className="pagination"
          aria-label={t("products.pagination.page", {
            page: formatCount(safePage + 1, locale),
            total: formatCount(pageCount, locale),
          })}
        >
          <button
            type="button"
            className="btn btn--secondary"
            data-testid="products-prev-page"
            disabled={safePage <= 0}
            onClick={() => setPage((p) => Math.max(0, p - 1))}
          >
            {t("products.pagination.prev")}
          </button>
          <span className="muted" data-testid="products-page-indicator">
            {t("products.pagination.page", {
              page: formatCount(safePage + 1, locale),
              total: formatCount(pageCount, locale),
            })}
          </span>
          <button
            type="button"
            className="btn btn--secondary"
            data-testid="products-next-page"
            disabled={safePage >= pageCount - 1}
            onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
          >
            {t("products.pagination.next")}
          </button>
        </nav>
      </ViewState>
    </div>
  );
}
