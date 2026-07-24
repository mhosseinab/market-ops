import type { MessageKey } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { type ReactNode, useMemo, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { useDeepLinkNavigate } from "../components/AppLink";
import {
  ApprovalStateBadge,
  ExecutionModeBadge,
  RecommendOnlyBadge,
  StatusBadge,
  type StatusState,
} from "../components/badges";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { FilterChips, Section } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { GatewayError } from "../data/errors";
import { formatInstant } from "../data/format";
import {
  useActionExecution,
  useActions,
  useApprovalCard,
  useOutcome,
  useOutcomesList,
  useRetryAction,
} from "../data/hooks";
import type {
  ActionCanonicalState,
  ActionSummary,
  ExecutionExternalState,
  OutcomeSummary,
  OutcomeView,
} from "../data/types";

// Actions (design screen 6 / EXE-003, EXE-005, OUT-001, AUD-001): proposed →
// executed → reconciled → measured, for BOTH execution modes.
//
// The queue is discovered from the account-scoped list (GET /actions) — never from
// a deep link alone (issue #106). Every row is an approval-card version carrying an
// OPTIONAL execution overlay bound to that EXACT card version; a pre-execution card
// carries none. Rows group by CANONICAL state so a marketplace write and a
// recommend-only action sit in one queue WITHOUT either borrowing the other's
// vocabulary.
//
// The never-cut rules this surface enforces visually:
//   * an action in PendingReconciliation (an UNKNOWN result) is NEVER shown as
//     success/failure and carries NO retry control — retry is offered only for a
//     definitively Failed WRITE, and even then it is a fresh approval card;
//   * a recommend-only action NEVER renders as an executed write: it has no write
//     external state, and a Lapsed action explicitly claims no execution at all;
//   * every detail/evidence panel is bound to the SELECTED card, so evidence from
//     one action can never render under another.
//
// `ActionSummary` carries no actionId by contract; rows correlate to outcomes and
// executions through the CARD id (ActionSummary.id ↔ OutcomeSummary.cardId ↔
// ActionExecutionView.cardId), and the selected card's own binding supplies the
// action id for the action-keyed reads.

const EXTERNAL_TO_STATUS: Record<ExecutionExternalState, StatusState> = {
  accepted: "accepted",
  rejected: "rejected",
  pending_reconciliation: "pendingReconciliation",
  failed: "failed",
};

// Affordances derive from the TYPED contract, NEVER from a visual grouping. The
// write state machine makes Accepted, Rejected AND Failed terminal (only Pending
// Reconciliation is an UNKNOWN result); /actions/retry is Failed-ONLY
// (Accepted/Rejected → ErrAlreadyTerminal). Deriving each control from these
// predicates guarantees a successful read never renders a control the server is
// guaranteed to reject.
function isTerminalExternalState(
  state: ExecutionExternalState | undefined,
): state is Exclude<ExecutionExternalState, "pending_reconciliation"> {
  return state !== undefined && state !== "pending_reconciliation";
}

// Retry is offered ONLY for a definitively Failed WRITE — the sole retry-eligible
// external state in the contract, and only in write mode (a recommend-only action
// never wrote anything, so there is nothing to retry).
function isRetryEligible(row: ActionSummary | undefined): boolean {
  return row?.executionMode === "write" && row.externalState === "failed";
}

// ── Canonical-state grouping (issue #106 acceptance #1) ─────────────────────
// A row with NO overlay is pre-execution ("proposed"); otherwise its canonical
// bucket is authoritative. The order is the lifecycle order an operator reads.
type GroupId = "proposed" | ActionCanonicalState;

const GROUPS: readonly { id: GroupId; titleKey: MessageKey }[] = [
  { id: "awaiting", titleKey: "actions.group.awaiting" },
  { id: "succeeded", titleKey: "actions.group.succeeded" },
  { id: "rejected", titleKey: "actions.group.rejected" },
  { id: "failed", titleKey: "actions.group.failed" },
  { id: "lapsed", titleKey: "actions.group.lapsed" },
  { id: "proposed", titleKey: "actions.group.proposed" },
  { id: "unknown", titleKey: "actions.group.unknown" },
];

function groupOf(row: ActionSummary): GroupId {
  return row.canonicalState ?? "proposed";
}

type FilterKey = "all" | "pending" | "failed" | "executed";

const FILTERS: readonly { id: FilterKey; labelKey: MessageKey }[] = [
  { id: "all", labelKey: "filter.all" },
  { id: "pending", labelKey: "actions.filter.pending" },
  { id: "failed", labelKey: "actions.filter.failed" },
  { id: "executed", labelKey: "actions.filter.executed" },
];

// Filters run on the CANONICAL state, so they span both modes: "pending" covers a
// write awaiting reconciliation AND a recommend-only action awaiting external
// execution. `all` is the only view that also shows pre-execution and lapsed rows.
function matchesFilter(row: ActionSummary, filter: FilterKey): boolean {
  if (filter === "all") return true;
  const canonical = row.canonicalState;
  if (canonical === undefined) return false;
  if (filter === "pending") return canonical === "awaiting";
  if (filter === "failed") return canonical === "failed" || canonical === "rejected";
  return canonical === "succeeded";
}

function unavailableNode(label: string): ReactNode {
  return <span className="muted">{label}</span>;
}

// Stable per-row element ids so the select control can borrow the row's
// LTR-ISOLATED identifier cell as its accessible name (aria-labelledby) instead
// of interpolating a raw UUID into RTL copy. The identifier stays inside
// <LtrToken>, where `unicode-bidi:isolate` keeps it from corrupting the
// surrounding Persian text (LOC-005) — and out of the visible button label,
// which is a plain catalog term.
const selectControlId = (cardId: string) => `action-select-control-${cardId}`;
const rowIdCellId = (cardId: string) => `action-id-cell-${cardId}`;

// A single outcome window in the shape the panel renders, normalized from EITHER
// authoritative read: the account list row (card-exact) or the action-scoped
// read. Normalizing here keeps the panel from branching on which read answered.
interface OutcomeWindow {
  readonly openedAt: string;
  readonly closesAt: string;
  readonly result?: OutcomeSummary["result"];
  readonly confidence?: OutcomeSummary["confidence"];
}

function windowFromSummary(o: OutcomeSummary): OutcomeWindow {
  return { openedAt: o.openedAt, closesAt: o.closesAt, result: o.result, confidence: o.confidence };
}

function windowFromView(o: OutcomeView): OutcomeWindow {
  return {
    openedAt: o.openedAt,
    closesAt: o.closesAt,
    result: o.result?.result,
    confidence: o.result?.confidence,
  };
}

// OUT-001 absence is a DEFINITIVE claim, so it may only be made from the read
// that is authoritative for that one action answering "no window" (404 /
// ErrNoWindow). Any other failure is an unknown, never an absence.
function isNoWindowAnswer(error: unknown): boolean {
  return error instanceof GatewayError && error.status === 404;
}

// Named cell (Products.tsx pattern): a single element keeps the copy-lint JSX-text
// heuristic and biome's line style from fighting over an inline ternary.
function TimeCell({ at }: { at?: string }) {
  const t = useT();
  const { locale } = useLocale();
  if (!at) return <span className="muted">{t("common.notAvailable")}</span>;
  return <span>{formatInstant(at, locale)}</span>;
}

// The state cell renders from the row's OWN authoritative fields. The three axes
// are kept disjoint by construction: a write shows its EXE-003 external state, a
// recommend-only action shows its EXE-005 state, and a pre-execution card shows
// its §8.4 approval state. No axis can borrow another's vocabulary.
function StateCell({ row }: { row: ActionSummary }) {
  if (row.executionMode === "write" && row.externalState) {
    return <StatusBadge state={EXTERNAL_TO_STATUS[row.externalState]} />;
  }
  if (row.executionMode === "recommend_only" && row.recommendOnlyState) {
    return <RecommendOnlyBadge state={row.recommendOnlyState} />;
  }
  return <ApprovalStateBadge state={row.state} />;
}

function ModeCell({ row }: { row: ActionSummary }) {
  const t = useT();
  if (!row.executionMode) return <span className="muted">{t("common.notAvailable")}</span>;
  return <ExecutionModeBadge mode={row.executionMode} />;
}

export function Actions() {
  const t = useT();
  const { locale } = useLocale();
  const navigate = useDeepLinkNavigate();
  const search = useRouterState({
    select: (s) => s.location.search as { actionId?: string; cardId?: string },
  });
  const [filter, setFilter] = useState<FilterKey>("all");

  const actionsQuery = useActions();
  const outcomesQuery = useOutcomesList();
  const rows = useMemo(() => actionsQuery.data?.items ?? [], [actionsQuery.data]);

  // Selection is URL-backed (`cardId`) so it survives refresh and is shareable. A
  // legacy `actionId` deep link is resolved to its card through the common single
  // read, which answers for BOTH modes — the deep link is a convenience, never the
  // only discovery path.
  const deepLinkExec = useActionExecution(search.cardId ? undefined : search.actionId);
  const selectedCardId = search.cardId ?? deepLinkExec.data?.cardId;
  const selectedRow = rows.find((r) => r.id === selectedCardId);

  const selectCard = (cardId: string) => {
    navigate("/actions", { cardId });
  };

  const visible = useMemo(() => rows.filter((r) => matchesFilter(r, filter)), [rows, filter]);
  const grouped = useMemo(
    () => GROUPS.map((g) => ({ ...g, rows: visible.filter((r) => groupOf(r) === g.id) })),
    [visible],
  );

  const unavailable = t("common.notAvailable");

  // The approval-card snapshot backing the audit trail. It is bound to the SELECTED
  // card id: a stale/in-flight response for a PREVIOUS selection is discarded rather
  // than rendered under the current action (no cross-action evidence mixing).
  const cardQuery = useApprovalCard(selectedCardId);
  const auditCard = cardQuery.data?.id === selectedCardId ? cardQuery.data : undefined;
  const selectedActionId = auditCard?.binding.actionId;

  // The write-mode execution record supplies the external ref / reconciliation
  // instant and backs the retry control. It is requested ONLY for a write action,
  // and only accepted when it names the exact (actionId, cardId) pair selected.
  const execQuery = useActionExecution(
    selectedRow?.executionMode === "write" ? selectedActionId : undefined,
  );
  const exec =
    execQuery.data?.cardId === selectedCardId && execQuery.data?.actionId === selectedActionId
      ? execQuery.data
      : undefined;

  const retry = useRetryAction();

  // OUT-001: the account list is matched on the EXACT (actionId, cardId) pair, so a
  // window opened for one card version never renders under another version of the
  // same action lineage. The list is PAGE-BOUNDED, though, so a miss proves
  // nothing — the action-scoped read below is the authority for absence.
  const listWindow: OutcomeSummary | undefined = outcomesQuery.data?.items.find(
    (o) => o.cardId === selectedCardId && o.actionId === selectedActionId,
  );

  // The authoritative per-action window read. Only THIS read may establish that
  // no window was opened; the page-bounded list never can.
  const outcomeQuery = useOutcome(selectedActionId);

  const columns: readonly Column<ActionSummary>[] = [
    {
      id: "select",
      header: "actions.col.select",
      // A real button, so selection is reachable by keyboard and announced by AT
      // (a click handler on the row alone is not).
      // The visible label is a plain catalog term; the row it acts on is named to
      // assistive tech by the LTR-isolated ID cell (aria-labelledby), and its
      // selected/unselected state by aria-pressed. No identifier is interpolated
      // into copy — that is what LtrToken exists for.
      render: (r) => (
        <button
          type="button"
          id={selectControlId(r.id)}
          className="btn btn--sm btn--secondary"
          aria-pressed={r.id === selectedCardId}
          aria-labelledby={`${selectControlId(r.id)} ${rowIdCellId(r.id)}`}
          data-testid={`action-select-${r.id}`}
          onClick={() => selectCard(r.id)}
        >
          {t("actions.col.select")}
        </button>
      ),
    },
    {
      id: "id",
      header: "actions.col.id",
      render: (r) => (
        <span id={rowIdCellId(r.id)}>
          <LtrToken text={r.id} />
        </span>
      ),
    },
    {
      id: "mode",
      header: "actions.col.mode",
      render: (r) => <ModeCell row={r} />,
    },
    {
      id: "state",
      header: "actions.col.state",
      render: (r) => <StateCell row={r} />,
    },
    {
      id: "surface",
      header: "actions.col.surface",
      // Actor + originating surface are not carried by the list row; rendered
      // explicitly unavailable rather than blanked (PRC-001).
      render: () => unavailableNode(unavailable),
    },
    {
      id: "time",
      header: "actions.col.time",
      render: (r) => <TimeCell at={r.createdAt} />,
    },
  ];

  return (
    <div className="screen">
      <FilterChips
        chips={FILTERS.map((f) => ({ id: f.id, labelKey: f.labelKey, active: filter === f.id }))}
        onToggle={(id) => setFilter(id as FilterKey)}
      />

      {/* The list's own loading/error/empty states are kept distinct: an error is
          never rendered as "no actions", and a filter that matches nothing is a
          DIFFERENT state from an account with no actions at all. */}
      <ViewState
        pending={actionsQuery.isPending}
        error={actionsQuery.isError}
        isEmpty={rows.length === 0}
        onRetry={() => void actionsQuery.refetch()}
      >
        <div className="split">
          <div className="split__main">
            {visible.length === 0 ? (
              <Section titleKey="actions.list.title">
                <p className="muted" data-testid="actions-empty-filtered">
                  {t("actions.list.emptyFiltered")}
                </p>
              </Section>
            ) : (
              grouped
                .filter((g) => g.rows.length > 0)
                .map((g) => (
                  <Section key={g.id} titleKey={g.titleKey}>
                    <div data-testid={`actions-group-${g.id}`}>
                      <DataTable
                        columns={columns}
                        rows={g.rows}
                        rowKey={(r) => r.id}
                        selectedId={selectedCardId}
                      />
                    </div>
                  </Section>
                ))
            )}

            {selectedRow ? detailFor(selectedRow) : selectedCardId ? notInPageDetail() : null}
          </div>

          <aside className="split__aside">
            {selectedCardId ? (
              <>
                <Section titleKey="actions.outcome.title">{outcomeBody()}</Section>

                <Section titleKey="actions.audit.title">
                  {cardQuery.isError ? (
                    <p className="muted" role="alert" data-testid="audit-error">
                      {t("actions.detail.error")}
                    </p>
                  ) : auditCard ? (
                    <dl className="kv" data-testid="audit-trail">
                      <div className="kv__row">
                        <dt>{t("actions.audit.card")}</dt>
                        <dd>
                          <LtrToken text={`${auditCard.id}·v${auditCard.version}`} />
                        </dd>
                      </div>
                      <div className="kv__row">
                        <dt>{t("actions.audit.price")}</dt>
                        <dd>
                          <MoneyView amount={auditCard.price} />
                        </dd>
                      </div>
                      <div className="kv__row">
                        <dt>{t("actions.audit.parameterVersion")}</dt>
                        <dd>
                          <LtrToken text={String(auditCard.binding.parameterVersion)} />
                        </dd>
                      </div>
                      <div className="kv__row">
                        <dt>{t("actions.audit.evidence")}</dt>
                        <dd>
                          <span className="component-list">
                            {auditCard.binding.evidenceVersions.map((e) => (
                              <span className="chip" key={e.observationId}>
                                <LtrToken text={`${e.observationId}·v${e.version}`} />
                              </span>
                            ))}
                          </span>
                        </dd>
                      </div>
                      <div className="kv__row">
                        <dt>{t("actions.audit.conversation")}</dt>
                        <dd>{unavailableNode(unavailable)}</dd>
                      </div>
                    </dl>
                  ) : (
                    <p className="muted">{unavailable}</p>
                  )}
                  <p className="muted">{t("actions.audit.independentNote")}</p>
                </Section>
              </>
            ) : deepLinkExec.isPending && deepLinkExec.fetchStatus !== "idle" ? (
              // A legacy `?actionId=` deep link IS a selection; while it resolves
              // to its card it is reported as resolving, never as no selection.
              <p className="muted" data-testid="actions-deeplink-resolving">
                {t("actions.detail.resolving")}
              </p>
            ) : (
              <p className="muted" data-testid="actions-select-prompt">
                {t("actions.detail.selectPrompt")}
              </p>
            )}
          </aside>
        </div>
      </ViewState>
    </div>
  );

  // The outcome panel distinguishes four states that must never collapse into one
  // another: loading, an explicit query error, a real window, and the truthful
  // "no window was opened".
  //
  // "No window" is a DEFINITIVE claim about OUT-001, so it is gated on the only
  // read that can support it: the action-scoped read answering 404 (ErrNoWindow).
  // Neither an in-flight/failed CARD read (which is what supplies the action id)
  // nor a miss in the PAGE-BOUNDED account list is evidence of absence — both are
  // unknowns and render as pending/error instead.
  function outcomeBody(): ReactNode {
    const pending = (
      <p className="muted" data-testid="outcome-pending">
        {t("actions.outcome.pending")}
      </p>
    );
    const failed = (
      <p className="muted" role="alert" data-testid="outcome-error">
        {t("actions.outcome.error")}
      </p>
    );

    // The action id is not known yet / can no longer be known: unknown, not absent.
    if (cardQuery.isPending) return pending;
    if (cardQuery.isError) return failed;
    if (selectedActionId === undefined) return failed;

    if (outcomesQuery.isPending || outcomeQuery.isPending) return pending;
    // A failed list read is reported as a failed read — never as "no window".
    if (outcomesQuery.isError) return failed;

    // Prefer the card-EXACT list row when the page happens to carry it; fall back
    // to the action-scoped read when the window is outside the returned page. The
    // fallback is accepted only when it NAMES the selected action, so a response
    // for a previous selection can never render under the current one.
    const actionWindow =
      outcomeQuery.data?.actionId === selectedActionId ? outcomeQuery.data : undefined;
    const win: OutcomeWindow | undefined = listWindow
      ? windowFromSummary(listWindow)
      : actionWindow
        ? windowFromView(actionWindow)
        : undefined;

    if (win) return outcomeWindowBody(win);
    // Only ErrNoWindow (404) establishes absence; any other failure is unknown.
    if (outcomeQuery.isError && isNoWindowAnswer(outcomeQuery.error)) {
      return (
        <p className="muted" data-testid="outcome-none">
          {t("actions.outcome.none")}
        </p>
      );
    }
    return failed;
  }

  function outcomeWindowBody(outcome: OutcomeWindow): ReactNode {
    return (
      <>
        <dl className="kv" data-testid="outcome-window">
          <div className="kv__row">
            <dt>{t("actions.outcome.opened")}</dt>
            <dd>{formatInstant(outcome.openedAt, locale)}</dd>
          </div>
          <div className="kv__row">
            <dt>{t("actions.outcome.closes")}</dt>
            <dd>{formatInstant(outcome.closesAt, locale)}</dd>
          </div>
          <div className="kv__row">
            <dt>{t("actions.outcome.result")}</dt>
            <dd>
              {outcome.result ? (
                <span data-testid="outcome-result">
                  {t(`outcomeResult.${outcome.result}` as MessageKey)}
                </span>
              ) : (
                <span className="muted">{t("actions.outcome.open")}</span>
              )}
            </dd>
          </div>
          <div className="kv__row">
            <dt>{t("actions.outcome.confidence")}</dt>
            <dd>
              {outcome.confidence ? (
                <span data-testid="outcome-confidence">
                  {t(`outcomeConfidence.${outcome.confidence}` as MessageKey)}
                </span>
              ) : (
                unavailableNode(unavailable)
              )}
            </dd>
          </div>
        </dl>
        <p className="muted">{t("actions.outcome.attributionNote")}</p>
      </>
    );
  }

  // A deep-linked (or reloaded) selection whose card is NOT in the returned page.
  // The queue read is page-bounded, so this is a page boundary, not an absence:
  // rendering "select an action" would be untrue, and rendering an execution
  // panel would be a fabricated claim (the list row is the only carrier of the
  // execution overlay, and a 404 from the action-scoped read cannot be told apart
  // from "not visible to this org"). So the boundary itself is stated, and the
  // card-bound audit trail beside it still renders from its own direct read.
  function notInPageDetail(): ReactNode {
    if (cardQuery.isPending) {
      return (
        <div className="panel" data-testid="action-detail-resolving">
          <p className="muted">{t("actions.detail.resolving")}</p>
        </div>
      );
    }
    if (cardQuery.isError) {
      return (
        <div className="panel" role="alert" data-testid="action-detail-error">
          <p className="muted">{t("actions.detail.error")}</p>
        </div>
      );
    }
    return (
      <div className="panel" data-testid="action-not-in-page">
        <p className="panel__title">{t("actions.notInPage.title")}</p>
        <p className="muted">{t("actions.notInPage.body")}</p>
      </div>
    );
  }

  function detailFor(row: ActionSummary): ReactNode {
    if (row.executionMode === "recommend_only") return recommendOnlyDetail(row);
    if (row.executionMode === "write") return writeDetail(row);
    // No overlay ⇒ the card has not been executed. Say so plainly rather than
    // rendering a write panel that would imply something happened.
    return (
      <div className="panel" data-testid="action-proposed">
        <p className="panel__title">{t("actions.proposed.title")}</p>
        <p className="muted">{t("actions.proposed.body")}</p>
      </div>
    );
  }

  // EXE-005. None of these panels may read as a marketplace write: awaiting and
  // lapsed both state explicitly that nothing was written, and a lapse is neither
  // an execution nor a write failure.
  function recommendOnlyDetail(row: ActionSummary): ReactNode {
    if (row.recommendOnlyState === "externally_executed") {
      return (
        <div className="panel" data-testid="action-externally-executed">
          <p className="panel__title">{t("actions.externallyExecuted.title")}</p>
          <p className="muted">{t("actions.externallyExecuted.body")}</p>
        </div>
      );
    }
    if (row.recommendOnlyState === "lapsed") {
      return (
        <div className="panel" data-testid="action-lapsed">
          <p className="panel__title">{t("actions.lapsed.title")}</p>
          <p className="muted">{t("actions.lapsed.body")}</p>
          <p className="muted">{t("actions.lapsed.noClaimNote")}</p>
        </div>
      );
    }
    return (
      <div className="panel" data-testid="action-awaiting-external">
        <p className="panel__title">{t("actions.recommendOnly.title")}</p>
        <p className="muted">{t("actions.recommendOnly.body")}</p>
        <p className="muted">{t("actions.recommendOnly.noWriteNote")}</p>
      </div>
    );
  }

  function writeDetail(row: ActionSummary): ReactNode {
    if (row.externalState === "pending_reconciliation") {
      return (
        <div
          className="banner banner--warn"
          role="alert"
          data-testid="action-pending-reconciliation"
        >
          <div className="banner__body">
            <p className="banner__title">{t("actions.pending.title")}</p>
            <p className="banner__text">{t("actions.pending.body")}</p>
            <p className="banner__text">{t("actions.pending.retryNote")}</p>
          </div>
          <div className="banner__actions">
            <button
              type="button"
              className="btn btn--sm"
              data-testid="action-reconcile-read"
              onClick={() => void actionsQuery.refetch()}
            >
              {t("actions.pending.readState")}
            </button>
          </div>
        </div>
      );
    }

    // Rejected is TERMINAL (EXE-003): its own rejection context and NEVER a Retry
    // control — /actions/retry would deterministically return ErrAlreadyTerminal.
    if (row.externalState === "rejected") {
      return (
        <div className="panel" data-testid="action-rejected">
          <p className="panel__title">
            {t("actions.rejected.title", { marketplace: t("marketplace.name") })}
          </p>
          <p className="muted">{t("actions.rejected.body")}</p>
          {exec?.externalRef ? (
            <p className="muted">
              {t("actions.accepted.externalRef")} <LtrToken text={exec.externalRef} />
            </p>
          ) : null}
        </div>
      );
    }

    // Failed is the SOLE retry-eligible state. The Retry affordance is gated on the
    // typed predicate, not on this panel's identity, so it can never leak elsewhere.
    if (row.externalState === "failed") {
      return (
        <div className="panel" data-testid="action-failed">
          <p className="panel__title">{t("actions.failed.title")}</p>
          <p className="muted">{t("actions.failed.body")}</p>
          {isRetryEligible(row) && selectedActionId ? (
            <div className="row-actions">
              <button
                type="button"
                className="btn btn--secondary"
                data-testid="action-retry"
                disabled={retry.isPending}
                onClick={() => retry.mutate(selectedActionId)}
              >
                {t("actions.action.retry")}
              </button>
            </div>
          ) : null}
          {/* The retry result belongs to the action it was issued for; it is shown
              only while that action is still the selected one. */}
          {retry.data && retry.data.actionId === selectedActionId ? (
            <p className="muted" data-testid="retry-outcome">
              {retry.data.eligible ? t("actions.retry.eligible") : t("actions.retry.ineligible")}
            </p>
          ) : null}
        </div>
      );
    }

    if (row.externalState === "accepted") {
      return (
        <div className="panel" data-testid="action-accepted">
          <p className="success-note">
            {t("state.accepted", { marketplace: t("marketplace.name") })}
          </p>
          {exec?.externalRef ? (
            <p className="muted">
              {t("actions.accepted.externalRef")} <LtrToken text={exec.externalRef} />
            </p>
          ) : null}
          {isTerminalExternalState(row.externalState) && exec?.reconciledAt ? (
            <p className="muted">
              <TimeCell at={exec.reconciledAt} />
            </p>
          ) : null}
        </div>
      );
    }

    // A write overlay with no recognised external state fails closed: report the
    // unknown rather than guessing a result.
    return (
      <div className="panel" data-testid="action-unknown-state">
        <p className="panel__title">{t("actions.group.unknown")}</p>
        <p className="muted">{unavailable}</p>
      </div>
    );
  }
}
