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
import {
  queryKeys,
  useAwaitingConfirmationActions,
  useBulkConfirm,
  useObservationTargets,
  useObservedOffers,
  useSelectionPreview,
} from "../data/hooks";
import { offerRowKey, offersByTargetId } from "../data/offers";
import type {
  BulkApprovalConfirmResult,
  BulkApprovalItemResult,
  BulkApprovalItemState,
  MarginReadiness,
  MarginReadinessState,
  ObservationTarget,
  ObservedOffer,
  SelectionSetMemberView,
} from "../data/types";

// Bulk preview & approval (design screen 4 / journey 3, CHAT-050/052): a filtered
// candidate set → a NAMED, VERSIONED selection set MINTED BY THE SERVER → a preview
// that separates executable / warning / blocked → a confirmation BOUND to exactly
// that server-minted version. The never-cut invariant this surface carries
// (mirroring the individual ApprovalCard at the set level, APR-001): ANY change to
// the set or its filters invalidates the preview and disables the approve control
// until a fresh preview is taken; the confirm payload carries the SERVER's lineage
// and version verbatim and the server re-verifies both. Blocked candidates are shown
// but NEVER force-included. Free text / Enter can never confirm.
//
// Selection identity is SERVER-OWNED (issue #90). This screen previously synthesized
// the selection-set lineage with crypto.randomUUID() and counted versions locally,
// so the confirm payload named a lineage that matched no row and the approve button
// could not succeed against a real backend. It now POSTs its filtered membership to
// /selection-sets/preview and binds the confirmation to the returned (lineageId,
// version) pair. The browser contributes MEMBERSHIP ONLY: the version, the
// membership fingerprint, and every member's disposition are resolved server-side
// from the members' own persisted recommendations, and the per-item results rendered
// after a confirmation are the server's authoritative `result.items` — never a
// client reconstruction.
//
// A member is the PAIR (variantId, recommendationId), so a candidate can only be
// included when the account's actions queue shows a live control-bearing card for
// its variant; a candidate with no such card is shown but never sent. Per-item
// from/to/movement + aggregate policy math the P0 contract does not expose to a bulk
// candidate stay explicitly unavailable (carry-forward for api_data_contracts).
// Pre-preview disposition is DISPLAYED from the core's own readiness + quality
// verdicts (classifyDisposition) — advisory only, never authoritative, and no money
// is recomputed here.
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
  // The live control-bearing card's recommendation for this candidate's variant,
  // from the account's actions queue. Absent ⇒ there is nothing to authorize, so the
  // candidate can never be a selection member (it is shown, never sent).
  readonly recommendationId?: string;
  readonly readiness?: MarginReadiness;
  // When the row's readiness query FAILED, the disposition is left undefined —
  // an errored load is never coerced into an authoritative verdict (error ≠
  // absence). Such a row renders unclassified and is excluded from the counts.
  readonly readinessFailed: boolean;
  readonly disposition?: Disposition;
  readonly reasonKey?: MessageKey;
}

// A candidate is one OBSERVED OFFER IDENTITY on a target (OBS-004): its selection
// membership, inclusion, and classification are keyed by the offer, so a
// conflicted/stale sibling is never hidden behind a verified one and one arbitrary
// offer never stands in for the whole target.
function candidateKey(c: Candidate): string {
  return offerRowKey(c.target.id, c.offer);
}

// The stable, per-offer include-control test id: the offer identity when present
// (native variant + seller), else the target's native variant id.
function candidateSlug(c: Candidate): string {
  return c.offer ? c.offer.offerIdentity : String(c.target.nativeVariantId);
}

// Per-item result copy, keyed by the SERVER's BulkApprovalItemState. Every state the
// contract can return has an explicit entry, so a state can never render blank or be
// silently treated as success. Canonical glossary terms are REUSED, never duplicated
// under a bulk-specific key: `invalidated` and `failed` are the same §8.4 states the
// rest of the app renders, so they read from `state.*` (design/README.md glossary is
// the single source for state copy). `excluded` likewise reuses its existing term.
const ITEM_STATE_META: Record<BulkApprovalItemState, { tone: string; labelKey: MessageKey }> = {
  authorized: { tone: "pos", labelKey: "bulk.result.state.authorized" },
  already_authorized: { tone: "pos", labelKey: "bulk.result.state.alreadyAuthorized" },
  excluded: { tone: "info", labelKey: "bulk.result.excluded" },
  invalidated: { tone: "warn", labelKey: "state.invalidated" },
  failed: { tone: "risk", labelKey: "state.failed" },
};

// The item states that mean the SERVER durably authorized the member. The post-confirm
// summary counts these, never a locally reconstructed number.
const AUTHORIZED_ITEM_STATES: readonly BulkApprovalItemState[] = [
  "authorized",
  "already_authorized",
];

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
  const actionsQuery = useAwaitingConfirmationActions();
  const selectionPreview = useSelectionPreview();
  const bulkConfirm = useBulkConfirm();

  // variantId → the live control-bearing card's recommendation. The actions queue is
  // the authoritative source of the (variantId, recommendationId) pairs a selection
  // member is made of; a row it does not cover has no approval control and is never
  // sent as a member.
  const recommendationByVariant = useMemo(() => {
    const map = new Map<string, string>();
    for (const a of actionsQuery.data?.items ?? []) {
      if (a.variantId) map.set(a.variantId, a.recommendationId);
    }
    return map;
  }, [actionsQuery.data]);

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

  // Every observed offer identity is preserved (OBS-004), grouped order-
  // independently. A target's readiness is per-VARIANT, so all its offers share
  // one readiness query — the fan-out below stays bound to the page's targets.
  const offersByTarget = useMemo(
    () => offersByTargetId(offersQuery.data?.items ?? []),
    [offersQuery.data],
  );
  const readinessByTargetId = useMemo(() => {
    const map = new Map<string, (typeof readinessQueries)[number]>();
    for (let i = 0; i < pageTargets.length; i++) {
      const tg = pageTargets[i];
      const q = readinessQueries[i];
      if (tg && q) {
        map.set(tg.id, q);
      }
    }
    return map;
  }, [pageTargets, readinessQueries]);

  // `revision` counts LOCAL selection mutations (membership, filters, page). It is
  // NOT a selection-set version — the server mints those. Its only job is to detect
  // that the local selection has drifted from the previewed one, which invalidates
  // the approve control until a fresh server preview is taken.
  const [revision, setRevision] = useState(1);
  // `selection` is the SERVER's minted selection set: its lineage, its version, its
  // sealed membership, and the local revision it was taken at. A confirmation binds
  // to selection.lineageId + selection.version verbatim.
  const [selection, setSelection] = useState<{
    readonly lineageId: string;
    readonly version: number;
    readonly members: readonly SelectionSetMemberView[];
    readonly revision: number;
  } | null>(null);
  const [readinessFilter, setReadinessFilter] = useState<MarginReadinessState | null>(null);
  const [excluded, setExcluded] = useState<ReadonlySet<string>>(new Set());
  const [result, setResult] = useState<BulkApprovalConfirmResult | null>(null);

  function mutateSet(fn: () => void) {
    fn();
    setRevision((v) => v + 1);
    setResult(null);
  }

  const candidates: Candidate[] = useMemo(() => {
    const rows: Candidate[] = [];
    for (const target of pageTargets) {
      const query = readinessByTargetId.get(target.id);
      const targetOffers = offersByTarget.get(target.id) ?? [];
      // A target with no observed offer keeps a single placeholder row; each
      // observed offer identity is classified on its OWN quality (OBS-004), so a
      // conflicted/stale sibling never hides behind a verified one.
      const offerList: (ObservedOffer | undefined)[] = targetOffers.length
        ? targetOffers
        : [undefined];
      for (const offer of offerList) {
        // A FAILED readiness load is left unclassified — never fabricated into a
        // "missing cost" blocked verdict (error ≠ absence, issue #81/#245).
        const recommendationId = recommendationByVariant.get(target.variantId);
        if (query?.isError) {
          rows.push({
            target,
            offer,
            recommendationId,
            readiness: undefined,
            readinessFailed: true,
          });
          continue;
        }
        const readiness = query?.data;
        const d = classifyDisposition(offer?.quality, readiness?.state);
        rows.push({
          target,
          offer,
          recommendationId,
          readiness,
          readinessFailed: false,
          disposition: d.disposition,
          reasonKey: d.reasonKey,
        });
      }
    }
    return rows.filter((c) => !readinessFilter || c.readiness?.state === readinessFilter);
  }, [pageTargets, offersByTarget, readinessByTargetId, readinessFilter, recommendationByVariant]);

  // Membership: a candidate (one offer identity) is IN the set unless explicitly
  // excluded; a blocked candidate is NEVER counted as executable regardless of
  // membership. Exclusion is keyed per OFFER, so excluding one sibling never
  // silently drops the other.
  const included = (key: string) => !excluded.has(key);
  const counts = useMemo(() => {
    let executable = 0;
    let warning = 0;
    let blocked = 0;
    for (const c of candidates) {
      // An unclassified (readiness-failed) row is not a verdict — it counts as
      // neither executable, warning, nor blocked.
      if (c.readinessFailed || c.disposition === undefined) continue;
      if (c.disposition === "blocked") blocked += 1;
      else if (excluded.has(candidateKey(c))) continue;
      else if (c.disposition === "executable") executable += 1;
      else warning += 1;
    }
    return { executable, warning, blocked };
  }, [candidates, excluded]);

  // The approve control is live ONLY while the server-minted selection still
  // describes the local selection. Any local mutation bumps `revision` and the
  // previewed selection goes stale — the control disables until a fresh server
  // preview is taken (APR-001 at the set level).
  const previewValid = selection !== null && selection.revision === revision;
  const previewStale = selection !== null && selection.revision !== revision;

  // The membership POSTed to the server: included, classified, non-blocked
  // candidates that have a live control-bearing card. Deduplicated by
  // recommendation, since sibling offer identities on one target share one variant
  // and therefore one approval control.
  const memberPayload = useMemo(() => {
    const byRecommendation = new Map<string, { variantId: string; recommendationId: string }>();
    for (const c of candidates) {
      if (c.readinessFailed || c.disposition === undefined || c.disposition === "blocked") continue;
      if (excluded.has(candidateKey(c)) || !c.recommendationId) continue;
      byRecommendation.set(c.recommendationId, {
        variantId: c.target.variantId,
        recommendationId: c.recommendationId,
      });
    }
    return [...byRecommendation.values()];
  }, [candidates, excluded]);

  // The server's authoritative per-item outcome, keyed by recommendation. Rendering
  // reads THIS, never a reconstruction from local candidate state.
  const resultByRecommendation = useMemo(() => {
    const map = new Map<string, BulkApprovalItemResult>();
    for (const item of result?.items ?? []) map.set(item.recommendationId, item);
    return map;
  }, [result]);

  // The post-confirm summary is SERVER-authoritative (issue #90 fix cycle 1, F8):
  // it counts the items the server actually reported as authorized. Announcing the
  // local `counts.executable` overstated a partial failure — every member the server
  // invalidated or failed was still announced as approved.
  const authorizedCount = useMemo(
    () => (result?.items ?? []).filter((i) => AUTHORIZED_ITEM_STATES.includes(i.state)).length,
    [result],
  );

  const unavailable = t("common.notAvailable");

  const columns: readonly Column<Candidate>[] = [
    {
      id: "include",
      header: "bulk.col.include",
      render: (c) => {
        // No include control for a blocked candidate, nor for an unclassified
        // (readiness-failed) row — an unknown verdict is never executable.
        if (c.disposition === "blocked" || c.disposition === undefined) {
          return <LtrToken text="—" />;
        }
        const key = candidateKey(c);
        return (
          <input
            type="checkbox"
            aria-label={t("bulk.col.include")}
            data-testid={`bulk-include-${candidateSlug(c)}`}
            checked={included(key)}
            onChange={() =>
              mutateSet(() =>
                setExcluded((prev) => {
                  const next = new Set(prev);
                  if (next.has(key)) next.delete(key);
                  else next.add(key);
                  return next;
                }),
              )
            }
          />
        );
      },
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
      id: "offer",
      // The observed offer identity (native variant + seller), LTR-isolated, so
      // sibling offers on one target are individually attributable (OBS-004).
      header: "bulk.col.offer",
      render: (c) => (c.offer ? <LtrToken text={c.offer.offerIdentity} /> : <LtrToken text="—" />),
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
      // The AUTHORITATIVE per-item outcome, read from the server's response items
      // and keyed by recommendation (issue #90). The client no longer infers a
      // result from its own candidate state: a row that the server did not report
      // on is explicitly "not in the selection set", never an assumed success.
      render: (c) => {
        if (!result?.valid) return <LtrToken text="—" />;
        const item = c.recommendationId
          ? resultByRecommendation.get(c.recommendationId)
          : undefined;
        if (!item) {
          return (
            <span className="muted" data-testid="result-excluded">
              {t("bulk.result.notAMember")}
            </span>
          );
        }
        const meta = ITEM_STATE_META[item.state];
        return (
          <span className="sm-state" data-tone={meta.tone} data-testid={`result-${item.state}`}>
            <span className="badge__dot" aria-hidden />
            {t(meta.labelKey)}
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
          lineage={selection?.lineageId ?? null}
          version={selection?.version ?? null}
          previewValid={previewValid}
          stale={previewStale}
          counts={counts}
          aggregateImpact={<span className="muted">{unavailable}</span>}
          maxMovement={<span className="muted">{unavailable}</span>}
          exclusions={<span>{formatCount(counts.blocked, locale)}</span>}
          confirmPending={bulkConfirm.isPending}
          previewPending={selectionPreview.isPending}
          onPreview={() => {
            // The SERVER mints the selection set: this POSTs the membership only and
            // pins whatever lineage + version comes back. Refreshing an existing
            // lineage mints its NEXT version server-side — the browser never counts.
            setResult(null);
            const revisionAtPreview = revision;
            selectionPreview.mutate(
              {
                name: "bulk-approval",
                ...(selection ? { lineageId: selection.lineageId } : {}),
                criteria: readinessFilter ? { readiness: readinessFilter } : {},
                members: memberPayload,
              },
              {
                onSuccess: (r) =>
                  setSelection({
                    lineageId: r.lineageId,
                    version: Number(r.version),
                    members: r.members,
                    revision: revisionAtPreview,
                  }),
              },
            );
          }}
          onApprove={() => {
            // Bound to EXACTLY the server-minted identity. Without a server preview
            // there is nothing to bind to and no request is made — a client-minted
            // lineage/version can never reach the confirm endpoint.
            if (!selection || !previewValid) return;
            setResult(null);
            bulkConfirm.mutate(
              {
                selectionSetLineage: selection.lineageId,
                boundVersion: selection.version,
              },
              { onSuccess: (r) => setResult(r) },
            );
          }}
        />

        {memberPayload.length === 0 ? (
          <p className="muted" role="status" data-testid="bulk-preview-empty">
            {t("bulk.preview.empty")}
          </p>
        ) : null}

        {selectionPreview.isPending ? (
          <p className="muted" role="status" data-testid="bulk-preview-pending">
            {t("bulk.preview.pending")}
          </p>
        ) : null}

        {selectionPreview.isError ? (
          <SectionError
            titleKey="bulk.preview.error.title"
            bodyKey="bulk.preview.error.body"
            testId="bulk-preview-error"
            onRetry={() => selectionPreview.reset()}
          />
        ) : null}

        {bulkConfirm.isError ? (
          <SectionError
            titleKey="bulk.confirm.error.title"
            bodyKey="bulk.confirm.error.body"
            testId="bulk-confirm-error"
            onRetry={() => bulkConfirm.reset()}
          />
        ) : null}

        {actionsQuery.isError ? (
          <SectionError
            titleKey="bulk.preview.error.title"
            bodyKey="bulk.preview.error.body"
            testId="bulk-actions-error"
            onRetry={() => void actionsQuery.refetch()}
          />
        ) : null}

        {actionsQuery.data?.hasMore ? (
          <p className="muted" data-testid="bulk-candidates-incomplete">
            {t("bulk.candidates.incomplete")}
          </p>
        ) : null}

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
              count: formatCount(authorizedCount, locale),
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
          <DataTable columns={columns} rows={candidates} rowKey={candidateKey} />

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
