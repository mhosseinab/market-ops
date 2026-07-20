import { useMemo } from "react";
import { useLocale, useT } from "../app/i18n";
import { useNow } from "../app/useNow";
import { AppLink } from "../components/AppLink";
import { Banner } from "../components/Banner";
import { FreshnessPill, QualityBadge } from "../components/badges";
import { CoverageBars, type CoverageSegment } from "../components/CoverageBars";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { Section } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { formatCount, formatInstant } from "../data/format";
import { freshnessState, freshnessTransitions } from "../data/freshness";
import { useConnectorAction, useObservationTargets, useObservedOffers } from "../data/hooks";
import { offerRowKey, offersByTargetId } from "../data/offers";
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
  return <FreshnessPill state={freshnessState(offer, now)} />;
}

// Buckets by the SHARED deadline-driven derivation (apps/web/src/data/freshness
// → packages/locale) — the SAME `freshnessState` the row pill and the extension
// overlay use, at the SAME `now`, so counts can never silently drift.
function freshnessSegments(offers: readonly ObservedOffer[], now: number): CoverageSegment[] {
  let fresh = 0;
  let aging = 0;
  let stale = 0;
  for (const o of offers) {
    const state = freshnessState(o, now);
    if (state === "fresh") fresh += 1;
    else if (state === "aging") aging += 1;
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

  // A page left open must flip an offer to Stale AT its authoritative deadline
  // without navigation (OBS-004). `now` advances via a single timer aimed at
  // the nearest future freshness transition across the visible offers, so the
  // row badge, the coverage bars, and any action/bulk gate all read the SAME
  // instant. Memoize the transition list so useNow only reschedules on change.
  const transitions = useMemo(() => offers.flatMap((o) => freshnessTransitions(o)), [offers]);
  const now = useNow(transitions);

  // Every observed offer identity is kept and rendered on its OWN row (OBS-004):
  // a target may carry multiple offers, so collapsing to one arbitrary row hides
  // a conflicted/stale sibling and lets an unrelated timestamp pick the winner.
  // Grouping is order-independent (see data/offers), so reordering `updated_at`
  // never changes what is shown or how each offer's quality/freshness reads.
  const offersByTarget = useMemo(() => offersByTargetId(offers), [offers]);

  const qualityCounts = useMemo(() => {
    const counts = new Map<QualityState, number>();
    for (const o of offers) counts.set(o.quality, (counts.get(o.quality) ?? 0) + 1);
    return counts;
  }, [offers]);

  const conflicted = useMemo(() => offers.filter((o) => o.quality === "conflicted"), [offers]);

  // One row per observed offer identity; a target with no observed offer keeps a
  // single placeholder row so it never silently disappears from the watch list.
  const rows: WatchRow[] = targets.flatMap((target) => {
    const targetOffers = offersByTarget.get(target.id) ?? [];
    if (targetOffers.length === 0) return [{ target }];
    return targetOffers.map((offer) => ({ target, offer }));
  });

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
      id: "offer",
      // The observed offer identity (native variant + seller) — LTR-isolated —
      // so sibling offers on one target are individually attributable (OBS-004).
      header: "market.col.offer",
      render: (r) => (r.offer ? <LtrToken text={r.offer.offerIdentity} /> : <LtrToken text="—" />),
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
          <DataTable columns={columns} rows={rows} rowKey={(r) => offerRowKey(r.target.id, r.offer)} />
        </Section>
      </ViewState>
    </div>
  );
}
