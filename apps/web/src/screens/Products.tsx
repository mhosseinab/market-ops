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

  const targets = useMemo(() => targetsQuery.data?.items ?? [], [targetsQuery.data]);

  const readinessQueries = useQueries({
    queries: targets.map((tg) => ({
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

  const offerByTarget = useMemo(() => {
    const map = new Map<string, ObservedOffer>();
    for (const o of offersQuery.data?.items ?? []) {
      if (!map.has(o.targetId)) map.set(o.targetId, o);
    }
    return map;
  }, [offersQuery.data]);

  const rows: Row[] = useMemo(() => {
    const normalizedSearch = normalizeDigits(search.trim());
    return targets
      .map((target, i) => ({
        target,
        offer: offerByTarget.get(target.id),
        readiness: readinessQueries[i]?.data,
      }))
      .filter((row) => {
        if (normalizedSearch !== "") {
          const hay = `${row.target.nativeVariantId} ${row.target.nativeProductId}`;
          if (!hay.includes(normalizedSearch)) return false;
        }
        if (readinessFilter && row.readiness?.state !== readinessFilter) return false;
        return true;
      });
  }, [targets, offerByTarget, readinessQueries, search, readinessFilter]);

  const columns: readonly Column<Row>[] = [
    {
      id: "product",
      header: "products.col.product",
      render: (r) => <LtrToken text={formatCount(r.target.nativeProductId, "en")} />,
    },
    {
      id: "sku",
      header: "products.col.sku",
      render: (r) => <LtrToken text={formatCount(r.target.nativeVariantId, "en")} />,
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
        onToggle={(id) => setReadinessFilter(id === "all" ? null : (id as MarginReadinessState))}
      />

      <p className="muted">{t("products.count", { count: formatCount(rows.length, locale) })}</p>

      <ViewState
        pending={targetsQuery.isPending}
        error={targetsQuery.isError}
        isEmpty={targets.length === 0}
        onRetry={() => void targetsQuery.refetch()}
      >
        <DataTable columns={columns} rows={rows} rowKey={(r) => r.target.id} />
      </ViewState>
    </div>
  );
}
