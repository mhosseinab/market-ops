import type { MessageKey } from "@market-ops/locale";
import { useQueries } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { gateway } from "../app/query";
import { BulkToolbar } from "../components/BulkToolbar";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { FilterChips } from "../components/primitives";
import { SectionError } from "../components/SectionError";
import { ViewState } from "../components/ViewState";
import { classifyDisposition, type Disposition } from "../data/disposition";
import { formatCount } from "../data/format";
import { queryKeys, useBulkConfirm, useObservationTargets, useObservedOffers } from "../data/hooks";
import type {
  BulkApprovalConfirmResult,
  MarginReadiness,
  MarginReadinessState,
  ObservationTarget,
  ObservedOffer,
} from "../data/types";

// Bulk preview & approval (design screen 4 / journey 3, CHAT-050/052): a filtered
// candidate set → a NAMED, VERSIONED selection set → a preview that separates
// executable / warning / blocked → a confirmation BOUND to the previewed
// selection-set version. The never-cut invariant this surface carries (mirroring
// the individual ApprovalCard at the set level, APR-001): ANY change to the set or
// its filters mints a new version, which invalidates the preview and disables the
// approve control until a fresh preview is taken; the confirm payload carries the
// bound version verbatim and the server re-verifies it. Blocked candidates are
// shown but NEVER force-included. Free text / Enter can never confirm.
//
// The P0 gateway exposes no selection-set / preview endpoint, so the selection-set
// lineage + version are synthesized client-side and per-item from/to/movement +
// aggregate policy math are rendered explicitly unavailable (carry-forward for
// api_data_contracts). Disposition is DISPLAYED from the core's own readiness +
// quality verdicts (classifyDisposition) — no money is recomputed here.
//
// Bounded fan-out (issue #245, mirroring #75/Products.tsx): readiness is fetched
// per variant and the P0 contract exposes no batch/paginated readiness endpoint,
// so an UNBOUNDED fan-out over the whole target set blows the §17.2 latency
// envelope as the assortment grows. Because /observation/targets is itself not
// server-paginated, the candidate set is paged CLIENT-side to a fixed page size
// and the readiness fan-out is bound to the CURRENT page's targets only — never
// the whole set. Counts and the previewed selection set are therefore per-page
// (a page change mints a new selection-set version, invalidating any live
// preview, exactly as a filter change does); a whole-set preview needs a
// server-side paginated readiness/selection endpoint (the same api_data_contracts
// carry-forward #75 recorded). A readiness query that ERRORS is NOT classified —
// error is not absence, so a failed load never fabricates an authoritative
// "blocked / missing cost" verdict (issue #81); it renders a scoped SectionError
// with a retry of only the failed queries while successful rows keep rendering.

// Client-side page bound for the readiness fan-out. Approving up to a page of
// candidates at a time keeps the per-render fan-out within the latency envelope.
export const BULK_READINESS_PAGE_SIZE = 25;

const DISPOSITION_META: Record<Disposition, { tone: string; labelKey: MessageKey }> = {
  executable: { tone: "tone-pos", labelKey: "bulk.status.executable" },
  warning: { tone: "tone-warn", labelKey: "bulk.status.warning" },
  blocked: { tone: "tone-risk", labelKey: "bulk.status.blocked" },
};

const READINESS_FILTERS: readonly { id: MarginReadinessState; labelKey: MessageKey }[] = [
  { id: "complete", labelKey: "readiness.complete" },
  { id: "partial", labelKey: "readiness.partial" },
  { id: "stale", labelKey: "readiness.stale" },
  { id: "missing", labelKey: "readiness.missing" },
];

interface Candidate {
  readonly target: ObservationTarget;
  readonly offer?: ObservedOffer;
  readonly readiness?: MarginReadiness;
  // When the row's readiness query FAILED, the disposition is left undefined —
  // an errored load is never coerced into an authoritative verdict (error ≠
  // absence). Such a row renders unclassified and is excluded from the counts.
  readonly readinessFailed: boolean;
  readonly disposition?: Disposition;
  readonly reasonKey?: MessageKey;
}

function newLineage(): string {
  const c = globalThis.crypto;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  return `sel-${Date.now()}`;
}

// Named cell (Products.tsx pattern): single-element render. The observed raw price
// (LTR evidence) when present, else an explicit unavailable node — never blanked.
function FromCell({ offer }: { offer?: ObservedOffer }) {
  const t = useT();
  if (!offer) return <span className="muted">{t("common.notAvailable")}</span>;
  return <LtrToken text={offer.price.text} />;
}

export function BulkApproval() {
  const t = useT();
  const { locale } = useLocale();
  const targetsQuery = useObservationTargets();
  const offersQuery = useObservedOffers();
  const bulkConfirm = useBulkConfirm();

  const targets = useMemo(() => targetsQuery.data?.items ?? [], [targetsQuery.data]);

  // Client-side page over the (non-paginated) target set: the readiness fan-out
  // below is bound to THIS slice only, never the whole assortment (issue #245).
  const [page, setPage] = useState(0);
  const pageStart = page * BULK_READINESS_PAGE_SIZE;
  const pageTargets = useMemo(
    () => targets.slice(pageStart, pageStart + BULK_READINESS_PAGE_SIZE),
    [targets, pageStart],
  );
  const hasPrevPage = page > 0;
  const hasNextPage = pageStart + BULK_READINESS_PAGE_SIZE < targets.length;

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

  // Only the queries that ERRORED — the scoped-retry set for the degraded state.
  const failedReadiness = readinessQueries.filter((q) => q.isError);

  const offerByTarget = useMemo(() => {
    const map = new Map<string, ObservedOffer>();
    for (const o of offersQuery.data?.items ?? []) if (!map.has(o.targetId)) map.set(o.targetId, o);
    return map;
  }, [offersQuery.data]);

  // The versioned selection set. `version` bumps on ANY membership/filter mutation;
  // `previewedVersion` pins the version the current preview (and any approve
  // control) is bound to. `lineage` is stable for the screen session.
  const [lineage] = useState(newLineage);
  const [version, setVersion] = useState(1);
  const [previewedVersion, setPreviewedVersion] = useState<number | null>(null);
  const [readinessFilter, setReadinessFilter] = useState<MarginReadinessState | null>(null);
  const [excluded, setExcluded] = useState<ReadonlySet<string>>(new Set());
  const [result, setResult] = useState<BulkApprovalConfirmResult | null>(null);

  function mutateSet(fn: () => void) {
    fn();
    setVersion((v) => v + 1);
    setResult(null);
  }

  const candidates: Candidate[] = useMemo(() => {
    return pageTargets
      .map((target, i) => {
        const offer = offerByTarget.get(target.id);
        const query = readinessQueries[i];
        // A FAILED readiness load is left unclassified — never fabricated into a
        // "missing cost" blocked verdict (error ≠ absence, issue #81/#245).
        if (query?.isError) {
          return { target, offer, readiness: undefined, readinessFailed: true };
        }
        const readiness = query?.data;
        const d = classifyDisposition(offer?.quality, readiness?.state);
        return {
          target,
          offer,
          readiness,
          readinessFailed: false,
          disposition: d.disposition,
          reasonKey: d.reasonKey,
        };
      })
      .filter((c) => !readinessFilter || c.readiness?.state === readinessFilter);
  }, [pageTargets, offerByTarget, readinessQueries, readinessFilter]);

  // Membership: a candidate is IN the set unless explicitly excluded; a blocked
  // candidate is NEVER counted as executable regardless of membership.
  const included = (id: string) => !excluded.has(id);
  const counts = useMemo(() => {
    let executable = 0;
    let warning = 0;
    let blocked = 0;
    for (const c of candidates) {
      // An unclassified (readiness-failed) row is not a verdict — it counts as
      // neither executable, warning, nor blocked.
      if (c.readinessFailed || c.disposition === undefined) continue;
      if (c.disposition === "blocked") blocked += 1;
      else if (excluded.has(c.target.id)) continue;
      else if (c.disposition === "executable") executable += 1;
      else warning += 1;
    }
    return { executable, warning, blocked };
  }, [candidates, excluded]);

  const previewValid = previewedVersion !== null && previewedVersion === version;
  const unavailable = t("common.notAvailable");

  const columns: readonly Column<Candidate>[] = [
    {
      id: "include",
      header: "bulk.col.include",
      render: (c) =>
        // No include control for a blocked candidate, nor for an unclassified
        // (readiness-failed) row — an unknown verdict is never executable.
        c.disposition === "blocked" || c.disposition === undefined ? (
          <LtrToken text="—" />
        ) : (
          <input
            type="checkbox"
            aria-label={t("bulk.col.include")}
            data-testid={`bulk-include-${c.target.nativeVariantId}`}
            checked={included(c.target.id)}
            onChange={() =>
              mutateSet(() =>
                setExcluded((prev) => {
                  const next = new Set(prev);
                  if (next.has(c.target.id)) next.delete(c.target.id);
                  else next.add(c.target.id);
                  return next;
                }),
              )
            }
          />
        ),
    },
    {
      id: "product",
      header: "bulk.col.product",
      render: (c) => <LtrToken text={String(c.target.nativeProductId)} />,
    },
    {
      id: "sku",
      header: "bulk.col.sku",
      render: (c) => <LtrToken text={String(c.target.nativeVariantId)} />,
    },
    {
      id: "from",
      header: "bulk.col.from",
      render: (c) => <FromCell offer={c.offer} />,
    },
    {
      id: "to",
      // Proposed price is a recommendation the P0 contract does not expose to a
      // bulk candidate; rendered explicitly unavailable rather than fabricated.
      header: "bulk.col.to",
      render: () => <span className="muted">{unavailable}</span>,
    },
    {
      id: "movement",
      header: "bulk.col.movement",
      render: () => <span className="muted">{unavailable}</span>,
    },
    {
      id: "status",
      header: "bulk.col.status",
      render: (c) => {
        // Readiness failed to load: render the honest "not available" node — the
        // scoped SectionError above carries the retry — never a fabricated verdict.
        if (c.disposition === undefined) {
          return <span className="muted">{unavailable}</span>;
        }
        return (
          <span className="bulk-status">
            <span className={`badge badge--pill ${DISPOSITION_META[c.disposition].tone}`}>
              <span className="badge__dot" aria-hidden />
              {t(DISPOSITION_META[c.disposition].labelKey)}
            </span>
            {c.disposition !== "executable" && c.reasonKey ? (
              <span className="muted bulk-status__reason">{t(c.reasonKey)}</span>
            ) : null}
          </span>
        );
      },
    },
    {
      id: "result",
      header: "bulk.col.result",
      render: (c) => {
        if (!result?.valid) return <LtrToken text="—" />;
        if (c.disposition === "blocked" || !included(c.target.id))
          return (
            <span className="muted" data-testid="result-excluded">
              {t("bulk.result.excluded")}
            </span>
          );
        if (c.disposition !== "executable")
          return <span className="muted">{t("bulk.result.excluded")}</span>;
        return (
          <span className="sm-state" data-tone="info" data-testid="result-item">
            <span className="badge__dot" aria-hidden />
            {t("bulk.result.awaitingExternal")}
          </span>
        );
      },
    },
  ];

  return (
    <div className="screen">
      <FilterChips
        chips={[
          { id: "all", labelKey: "filter.all", active: readinessFilter === null },
          ...READINESS_FILTERS.map((f) => ({
            id: f.id,
            labelKey: f.labelKey,
            active: readinessFilter === f.id,
          })),
        ]}
        onToggle={(id) =>
          mutateSet(() => {
            setReadinessFilter(id === "all" ? null : (id as MarginReadinessState));
            setPage(0);
          })
        }
      />

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
        <BulkToolbar
          lineage={lineage}
          version={version}
          previewedVersion={previewedVersion}
          counts={counts}
          aggregateImpact={<span className="muted">{unavailable}</span>}
          maxMovement={<span className="muted">{unavailable}</span>}
          exclusions={<span>{formatCount(counts.blocked, locale)}</span>}
          confirmPending={bulkConfirm.isPending}
          onPreview={() => {
            setResult(null);
            setPreviewedVersion(version);
          }}
          onApprove={() => {
            if (previewedVersion === null) return;
            setResult(null);
            bulkConfirm.mutate(
              { selectionSetLineage: lineage, boundVersion: previewedVersion },
              { onSuccess: (r) => setResult(r) },
            );
          }}
        />

        {result && !result.valid ? (
          <div className="banner banner--warn" role="alert" data-testid="bulk-stale-result">
            <div className="banner__body">
              <p className="banner__title">{t("bulk.invalidated.title")}</p>
              <p className="banner__text">{t("bulk.invalidated.body")}</p>
            </div>
          </div>
        ) : null}

        {result?.valid && result.executionPending ? (
          <p className="success-note" data-testid="bulk-recommend-only">
            {t("bulk.result.recommendOnly", {
              count: formatCount(counts.executable, locale),
            })}
          </p>
        ) : null}

        {failedReadiness.length > 0 ? (
          <SectionError
            titleKey="bulk.readiness.error.title"
            bodyKey="bulk.readiness.error.body"
            testId="bulk-readiness-error"
            onRetry={() => {
              for (const q of failedReadiness) void q.refetch();
            }}
          />
        ) : null}

        <section className="panel">
          <div className="panel__head">
            <h2 className="panel__title">{t("bulk.table.title")}</h2>
            {previewValid ? null : (
              <span className="muted" data-testid="preview-required">
                {t("bulk.previewRequired")}
              </span>
            )}
          </div>
          <DataTable columns={columns} rows={candidates} rowKey={(c) => c.target.id} />

          <nav className="pagination" aria-label={t("bulk.pagination.label")}>
            <button
              type="button"
              className="btn btn--secondary"
              data-testid="bulk-prev-page"
              disabled={!hasPrevPage}
              onClick={() => mutateSet(() => setPage((p) => Math.max(0, p - 1)))}
            >
              {t("bulk.pagination.prev")}
            </button>
            <span className="muted" data-testid="bulk-page-indicator">
              {formatCount(page + 1, locale)}
            </span>
            <button
              type="button"
              className="btn btn--secondary"
              data-testid="bulk-next-page"
              disabled={!hasNextPage}
              onClick={() => mutateSet(() => setPage((p) => p + 1))}
            >
              {t("bulk.pagination.next")}
            </button>
          </nav>
        </section>
      </ViewState>
    </div>
  );
}
