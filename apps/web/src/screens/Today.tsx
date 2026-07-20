import type { MessageKey } from "@market-ops/locale";
import { useLocale, useT } from "../app/i18n";
import { AppLink } from "../components/AppLink";
import { Banner } from "../components/Banner";
import { EventRow } from "../components/EventRow";
import { StatCard } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { formatCount } from "../data/format";
import { useTodayFeed } from "../data/hooks";
import type { QualityState, RankedEvent } from "../data/types";

// Today (design screen 1 / EVT-004): the ranked priority queue. Order is the
// core's DETERMINISTIC exposure × confidence × urgency rank — the screen renders
// `rank` and all three factors as given, never re-sorts or re-weights. Blocked
// (non-actionable) events surface a data-readiness banner whose chips deep-link
// per the IA map. Empty feed → the reassuring "no action needed" state.

type BlockerCategory = "cost" | "mapping" | "stale";

const BLOCKER_ROUTE: Record<BlockerCategory, { to: string; labelKey: MessageKey }> = {
  cost: { to: "/products", labelKey: "today.readiness.blocker.cost" },
  mapping: { to: "/operations", labelKey: "today.readiness.blocker.mapping" },
  stale: { to: "/market", labelKey: "today.readiness.blocker.stale" },
};

function blockerCategory(quality: QualityState): BlockerCategory | null {
  if (quality === "unverified" || quality === "unavailable") return "cost";
  if (quality === "conflicted") return "mapping";
  if (quality === "stale") return "stale";
  return null;
}

function collectBlockers(items: readonly RankedEvent[]): BlockerCategory[] {
  const seen = new Set<BlockerCategory>();
  for (const item of items) {
    const cat = blockerCategory(item.event.evidenceQuality as QualityState);
    if (cat) seen.add(cat);
  }
  return (["cost", "mapping", "stale"] as const).filter((c) => seen.has(c));
}

export function Today() {
  const t = useT();
  const { locale } = useLocale();
  const feedQuery = useTodayFeed();
  const items = feedQuery.data?.items ?? [];
  const blockers = collectBlockers(items);
  // COUNT of events whose exposure amount is known — NOT a monetary aggregate.
  // Summing Money client-side across possibly-mixed currencies/exponents is
  // unsafe (§4.6 money correctness), so this stat stays an honest count and is
  // labeled/announced as such. Unknown-exposure events are simply excluded here;
  // they are never treated as zero money.
  const knownExposureCount = items.filter((i) => i.event.factors.exposure.known).length;
  const blockedCount = items.filter(
    (i) => blockerCategory(i.event.evidenceQuality as QualityState) !== null,
  ).length;

  return (
    <div className="screen">
      <ViewState
        pending={feedQuery.isPending}
        error={feedQuery.isError}
        onRetry={() => void feedQuery.refetch()}
        skeletonRows={4}
      >
        {items.length === 0 ? (
          <div className="screen-empty" data-testid="today-no-action">
            <p>{t("today.noAction.title")}</p>
            <p>{t("today.noAction.body")}</p>
          </div>
        ) : (
          <>
            <div className="stat-row">
              <StatCard
                value={formatCount(items.length, locale)}
                labelKey="today.stat.highPriority"
                accent="risk"
              />
              <StatCard
                value={formatCount(knownExposureCount, locale)}
                labelKey="today.stat.knownExposureEvents"
                ariaLabel={t("today.stat.knownExposureEvents.aria", {
                  count: formatCount(knownExposureCount, locale),
                })}
                accent="info"
              />
            </div>

            {blockers.length > 0 ? (
              <Banner
                tone="warn"
                title={t("today.readiness.title")}
                body={t("today.readiness.count", { count: formatCount(blockedCount, locale) })}
                actions={
                  <span className="filter-chips">
                    {blockers.map((cat) => (
                      <AppLink key={cat} to={BLOCKER_ROUTE[cat].to} className="btn btn--sm">
                        {t(BLOCKER_ROUTE[cat].labelKey)} · {t("today.readiness.resolve")}
                      </AppLink>
                    ))}
                  </span>
                }
              />
            ) : null}

            <section className="panel" data-testid="today-queue">
              <div className="panel__head">
                <h2 className="panel__title">{t("today.queue.title")}</h2>
              </div>
              <ul className="event-list">
                {items.map((item) => (
                  <EventRow
                    key={item.event.id}
                    rank={item.rank}
                    event={item.event}
                    factors={item.factors}
                  />
                ))}
              </ul>
              <p className="muted event-list__end">{t("today.endOfQueue")}</p>
            </section>
          </>
        )}
      </ViewState>
    </div>
  );
}
