import type { MessageKey } from "@market-ops/locale";
import { useQueries } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { gateway } from "../app/query";
import { BulkToolbar } from "../components/BulkToolbar";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { FilterChips } from "../components/primitives";
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
  readonly disposition: Disposition;
  readonly reasonKey: MessageKey;
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
    return targets
      .map((target, i) => {
        const offer = offerByTarget.get(target.id);
        const readiness = readinessQueries[i]?.data;
        const d = classifyDisposition(offer?.quality, readiness?.state);
        return { target, offer, readiness, disposition: d.disposition, reasonKey: d.reasonKey };
      })
      .filter((c) => !readinessFilter || c.readiness?.state === readinessFilter);
  }, [targets, offerByTarget, readinessQueries, readinessFilter]);

  // Membership: a candidate is IN the set unless explicitly excluded; a blocked
  // candidate is NEVER counted as executable regardless of membership.
  const included = (id: string) => !excluded.has(id);
  const counts = useMemo(() => {
    let executable = 0;
    let warning = 0;
    let blocked = 0;
    for (const c of candidates) {
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
        c.disposition === "blocked" ? (
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
      render: (c) => (
        <span className="bulk-status">
          <span className={`badge badge--pill ${DISPOSITION_META[c.disposition].tone}`}>
            <span className="badge__dot" aria-hidden />
            {t(DISPOSITION_META[c.disposition].labelKey)}
          </span>
          {c.disposition !== "executable" ? (
            <span className="muted bulk-status__reason">{t(c.reasonKey)}</span>
          ) : null}
        </span>
      ),
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
          mutateSet(() => setReadinessFilter(id === "all" ? null : (id as MarginReadinessState)))
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
        </section>
      </ViewState>
    </div>
  );
}
