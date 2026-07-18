import { FRESHNESS_AGING_MAX_MINUTES, FRESHNESS_FRESH_MAX_MINUTES } from "@market-ops/locale";
import { useMemo } from "react";
import { useLocale, useT } from "../app/i18n";
import { AppLink } from "../components/AppLink";
import { Banner } from "../components/Banner";
import { FreshnessPill, QualityBadge } from "../components/badges";
import { CoverageBars, type CoverageSegment } from "../components/CoverageBars";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { Section } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { ageMinutes, formatCount, formatInstant } from "../data/format";
import { useConnectorAction, useObservationTargets, useObservedOffers } from "../data/hooks";
import type { ObservationTarget, ObservedOffer, QualityState } from "../data/types";

// Market (design screen 9 / OBS-004): watch targets, observed offers, freshness
// coverage, quality distribution, and the conflicted-observation banner that
// routes to Operations. Every value is rendered from the core's surfaced
// observation state — freshness bands and quality counts bucket ONLY the counts
// the API returns; no price is recomputed and an offer's raw price is kept as
// evidence, LTR-isolated. A budgeted refresh is a request to the connector; the
// budget/scheduling is server-owned.

const QUALITY_ORDER: readonly QualityState[] = [
  "verified",
  "supported",
  "unverified",
  "conflicted",
  "stale",
  "unavailable",
];

interface WatchRow {
  readonly target: ObservationTarget;
  readonly offer?: ObservedOffer;
}

// Named cell (Products.tsx pattern): single-element render so copy-lint and biome
// don't fight over an inline ternary. Freshness pill, or an em-dash placeholder.
function FreshnessCell({ offer, now }: { offer?: ObservedOffer; now: number }) {
  if (!offer) return <LtrToken text="—" />;
  return <FreshnessPill ageMinutes={ageMinutes(offer.capturedAt, now)} />;
}

// Thresholds are the SHARED constants (packages/locale) — the SAME ones
// FreshnessPill and the extension overlay's freshnessBucketOf compare
// against, so this bucketing can never silently drift from either.
function freshnessSegments(offers: readonly ObservedOffer[], now: number): CoverageSegment[] {
  let fresh = 0;
  let aging = 0;
  let stale = 0;
  for (const o of offers) {
    const age = ageMinutes(o.capturedAt, now);
    if (age <= FRESHNESS_FRESH_MAX_MINUTES) fresh += 1;
    else if (age <= FRESHNESS_AGING_MAX_MINUTES) aging += 1;
    else stale += 1;
  }
  return [
    { id: "fresh", labelKey: "freshness.fresh", tone: "pos", count: fresh },
    { id: "aging", labelKey: "freshness.aging", tone: "warn", count: aging },
    { id: "stale", labelKey: "freshness.stale", tone: "risk", count: stale },
  ];
}

export function Market() {
  const t = useT();
  const { locale } = useLocale();
  const targetsQuery = useObservationTargets();
  const offersQuery = useObservedOffers();
  const refresh = useConnectorAction("/connector/refresh");

  const targets = useMemo(() => targetsQuery.data?.items ?? [], [targetsQuery.data]);
  const offers = useMemo(() => offersQuery.data?.items ?? [], [offersQuery.data]);
  const now = Date.now();

  const offerByTarget = useMemo(() => {
    const map = new Map<string, ObservedOffer>();
    for (const o of offers) if (!map.has(o.targetId)) map.set(o.targetId, o);
    return map;
  }, [offers]);

  const qualityCounts = useMemo(() => {
    const counts = new Map<QualityState, number>();
    for (const o of offers) counts.set(o.quality, (counts.get(o.quality) ?? 0) + 1);
    return counts;
  }, [offers]);

  const conflicted = useMemo(() => offers.filter((o) => o.quality === "conflicted"), [offers]);

  const rows: WatchRow[] = targets.map((target) => ({
    target,
    offer: offerByTarget.get(target.id),
  }));

  const columns: readonly Column<WatchRow>[] = [
    {
      id: "product",
      header: "market.col.product",
      render: (r) => <LtrToken text={String(r.target.nativeProductId)} />,
    },
    {
      id: "sku",
      header: "market.col.sku",
      render: (r) => <LtrToken text={String(r.target.nativeVariantId)} />,
    },
    {
      id: "quality",
      header: "market.col.quality",
      render: (r) => (r.offer ? <QualityBadge state={r.offer.quality} /> : <LtrToken text="—" />),
    },
    {
      id: "freshness",
      header: "market.col.freshness",
      render: (r) => <FreshnessCell offer={r.offer} now={now} />,
    },
    {
      id: "price",
      header: "market.col.price",
      render: (r) => (r.offer ? <LtrToken text={r.offer.price.text} /> : <LtrToken text="—" />),
    },
  ];

  return (
    <div className="screen">
      <div className="toolbar">
        <button
          type="button"
          className="btn btn--secondary"
          data-testid="market-refresh"
          disabled={refresh.isPending}
          onClick={() => refresh.mutate()}
        >
          {t("market.refresh.request")}
        </button>
      </div>

      <ViewState
        pending={targetsQuery.isPending || offersQuery.isPending}
        error={targetsQuery.isError || offersQuery.isError}
        isEmpty={targets.length === 0}
        onRetry={() => {
          void targetsQuery.refetch();
          void offersQuery.refetch();
        }}
        skeletonRows={4}
      >
        <div className="screen__grid">
          <Section titleKey="market.coverage.title">
            <CoverageBars segments={freshnessSegments(offers, now)} />
          </Section>

          <Section titleKey="market.quality.title">
            <ul className="quality-dist" data-testid="quality-distribution">
              {QUALITY_ORDER.map((q) => (
                <li className="quality-dist__row" key={q}>
                  <QualityBadge state={q} />
                  <span className="quality-dist__count">
                    {formatCount(qualityCounts.get(q) ?? 0, locale)}
                  </span>
                </li>
              ))}
            </ul>
          </Section>
        </div>

        {conflicted.length > 0 ? (
          <Banner
            tone="conflict"
            title={t("market.conflict.title", {
              count: formatCount(conflicted.length, locale),
            })}
            body={
              <span>
                {t("market.conflict.body")}{" "}
                {conflicted.map((o) => (
                  <span className="chip" key={o.id}>
                    <LtrToken text={o.offerIdentity} /> · <LtrToken text={o.routes.join("/")} /> ·{" "}
                    {formatInstant(o.capturedAt, locale)}
                  </span>
                ))}
                <span className="muted"> {t("market.conflict.valuesUnavailable")}</span>
              </span>
            }
            actions={
              <AppLink to="/operations" className="btn btn--sm" testId="conflict-to-operations">
                {t("market.conflict.toOperations")}
              </AppLink>
            }
          />
        ) : null}

        <Section titleKey="market.watch.title">
          <DataTable columns={columns} rows={rows} rowKey={(r) => r.target.id} />
        </Section>
      </ViewState>
    </div>
  );
}
