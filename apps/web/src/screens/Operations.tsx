import { useMemo } from "react";
import { useLocale, useT } from "../app/i18n";
import { RUNBOOKS, type RunbookQueueKey, runbookTo } from "../app/runbooks";
import { AppLink } from "../components/AppLink";
import { QueueCard } from "../components/QueueCard";
import { ViewState } from "../components/ViewState";
import { formatCount } from "../data/format";
import { freshnessState } from "../data/freshness";
import { useConnectorStatus, useNeedsReview, useObservedOffers, useSession } from "../data/hooks";

// Operations (design screen 10 / OPS-002, internal): the internal data-quality &
// execution queues. The never-cut rule: this surface is behind the INTERNAL-role
// gate — a non-internal principal never sees the queues. Counts are derived ONLY
// from surfaced observation/identity/connector state; queues whose backing list
// endpoint the P0 contract does not expose (parser/schema drift, pending-
// reconciliation actions) render an explicit unavailable count, never a
// fabricated zero (carry-forward for api_data_contracts).

// Each queue links to its runbook via the canonical registry (app/runbooks.ts) —
// the SAME source the in-SPA viewer and the Grafana validator read, so a Runbook
// link can never point at a dead/unregistered destination. Links route through
// AppLink to the in-SPA viewer (honours router basepath, so a base-path
// deployment resolves correctly) instead of a raw `<a href>` anchor.
function RunbookLink({ queue }: { queue: RunbookQueueKey }) {
  const t = useT();
  return (
    <AppLink to={runbookTo(RUNBOOKS[queue].slug)} className="link" testId="runbook-link">
      {t("operations.runbook")}
    </AppLink>
  );
}

export function Operations() {
  const t = useT();
  const { locale } = useLocale();
  const sessionQuery = useSession();
  const connectorQuery = useConnectorStatus();
  const offersQuery = useObservedOffers();
  const needsReviewQuery = useNeedsReview();

  const role = sessionQuery.data?.role;
  const now = Date.now();

  const offers = useMemo(() => offersQuery.data?.items ?? [], [offersQuery.data]);
  const conflictedCount = offers.filter((o) => o.quality === "conflicted").length;
  const staleCount = offers.filter((o) => freshnessState(o, now) === "stale").length;
  const mappingCount = needsReviewQuery.data?.items.length ?? 0;
  const failedSyncCount =
    connectorQuery.data && connectorQuery.data.connectionState !== "connected" ? 1 : 0;

  const num = (n: number) => <span>{formatCount(n, locale)}</span>;
  const unavailableCount = <span className="muted">{t("common.notAvailable")}</span>;

  if (sessionQuery.isPending) {
    return (
      <div className="screen">
        <ViewState pending error={false}>
          <span />
        </ViewState>
      </div>
    );
  }

  if (role !== "internal") {
    return (
      <div className="screen">
        <div className="screen-empty" data-testid="operations-internal-only">
          <p>{t("operations.internalOnly.title")}</p>
          <p>{t("operations.internalOnly.body")}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="screen">
      <ViewState
        pending={connectorQuery.isPending || offersQuery.isPending}
        error={connectorQuery.isError || offersQuery.isError}
        onRetry={() => {
          void connectorQuery.refetch();
          void offersQuery.refetch();
        }}
      >
        <div className="queue-grid" data-testid="operations-queues">
          <QueueCard
            titleKey="operations.queue.failedSync"
            descKey="operations.queue.failedSync.desc"
            accent="risk"
            count={num(failedSyncCount)}
            open={
              <AppLink to="/onboarding" className="btn btn--sm">
                {t("operations.openQueue")}
              </AppLink>
            }
            runbook={<RunbookLink queue="failedSync" />}
          />
          <QueueCard
            titleKey="operations.queue.staleTargets"
            descKey="operations.queue.staleTargets.desc"
            accent="warn"
            count={num(staleCount)}
            open={
              <AppLink to="/market" className="btn btn--sm">
                {t("operations.openQueue")}
              </AppLink>
            }
            runbook={<RunbookLink queue="staleTargets" />}
          />
          <QueueCard
            titleKey="operations.queue.identityMapping"
            descKey="operations.queue.identityMapping.desc"
            accent="info"
            count={num(mappingCount)}
            open={
              <AppLink to="/products" className="btn btn--sm">
                {t("operations.openQueue")}
              </AppLink>
            }
            runbook={<RunbookLink queue="identityMapping" />}
          />
          <QueueCard
            titleKey="operations.queue.conflicted"
            descKey="operations.queue.conflicted.desc"
            accent="conflict"
            count={num(conflictedCount)}
            open={
              <AppLink to="/market" className="btn btn--sm">
                {t("operations.openQueue")}
              </AppLink>
            }
            runbook={<RunbookLink queue="conflicted" />}
          />
          <QueueCard
            titleKey="operations.queue.parserDrift"
            descKey="operations.queue.parserDrift.desc"
            accent="warn"
            count={unavailableCount}
            runbook={<RunbookLink queue="parserDrift" />}
          />
          <QueueCard
            titleKey="operations.queue.pendingRecon"
            descKey="operations.queue.pendingRecon.desc"
            accent="warn"
            count={unavailableCount}
            runbook={<RunbookLink queue="pendingRecon" />}
          />
        </div>
      </ViewState>
    </div>
  );
}
