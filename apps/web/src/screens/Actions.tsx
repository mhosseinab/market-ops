import type { MessageKey } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { type ReactNode, useMemo, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { StatusBadge, type StatusState } from "../components/badges";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { FilterChips, Section } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { formatInstant } from "../data/format";
import { useActionExecution, useApprovalCard, useOutcome, useRetryAction } from "../data/hooks";
import type { ActionExecutionView, ExecutionExternalState } from "../data/types";

// Actions (design screen 6 / EXE-002/003, OUT-001, AUD-001): proposed → executed
// → reconciled → measured. The detail panel switches by the EXE-003 external
// state. The never-cut rule this surface enforces visually: an action in
// PendingReconciliation (an UNKNOWN result) is NEVER shown as success/failure and
// carries NO retry control — retry is offered only for a definitively Failed
// action, and even then it is a fresh approval card, never an inverse write.
//
// The P0 gateway exposes no list-actions endpoint, so the tracked action is
// resolved from a deep-linked action/card id; the multi-row grouped queue awaits a
// list endpoint (carry-forward for api_data_contracts). All money/version fields
// are rendered from the execution record + its approval-card snapshot as given.

const EXTERNAL_TO_STATUS: Record<ExecutionExternalState, StatusState> = {
  accepted: "accepted",
  rejected: "rejected",
  pending_reconciliation: "pendingReconciliation",
  failed: "failed",
};

type FilterKey = "all" | "pending" | "failed" | "executed";

const FILTERS: readonly { id: FilterKey; labelKey: MessageKey }[] = [
  { id: "all", labelKey: "filter.all" },
  { id: "pending", labelKey: "actions.filter.pending" },
  { id: "failed", labelKey: "actions.filter.failed" },
  { id: "executed", labelKey: "actions.filter.executed" },
];

function matchesFilter(state: ExecutionExternalState, filter: FilterKey): boolean {
  if (filter === "all") return true;
  if (filter === "pending") return state === "pending_reconciliation";
  if (filter === "failed") return state === "failed" || state === "rejected";
  return state === "accepted";
}

function unavailableNode(label: string): ReactNode {
  return <span className="muted">{label}</span>;
}

// Named cell (Products.tsx pattern): the render returns a single element, keeping
// the copy-lint JSX-text heuristic and biome's line style from fighting over an
// inline ternary. Reconciliation instant, or an explicit unavailable node.
function TimeCell({ reconciledAt }: { reconciledAt?: string }) {
  const t = useT();
  const { locale } = useLocale();
  if (!reconciledAt) return <span className="muted">{t("common.notAvailable")}</span>;
  return <span>{formatInstant(reconciledAt, locale)}</span>;
}

export function Actions() {
  const t = useT();
  const { locale } = useLocale();
  const search = useRouterState({
    select: (s) => s.location.search as { actionId?: string; cardId?: string },
  });
  const [filter, setFilter] = useState<FilterKey>("all");

  // Resolve the tracked action from either a direct actionId or a card deep link.
  const seedCardQuery = useApprovalCard(search.cardId);
  const actionId = search.actionId ?? seedCardQuery.data?.binding.actionId;
  const execQuery = useActionExecution(actionId);
  const exec = execQuery.data;

  // The approval-card snapshot backing the audit trail (evidence + price versions
  // at execution). Prefer the card the action was bound to.
  const auditCardId = search.cardId ?? exec?.cardId;
  const auditCardQuery = useApprovalCard(auditCardId);
  const auditCard = auditCardQuery.data;

  const outcomeQuery = useOutcome(exec?.externalState === "accepted" ? actionId : undefined);
  const retry = useRetryAction();

  const rows: ActionExecutionView[] = useMemo(
    () => (exec && matchesFilter(exec.externalState, filter) ? [exec] : []),
    [exec, filter],
  );

  const unavailable = t("common.notAvailable");

  const columns: readonly Column<ActionExecutionView>[] = [
    {
      id: "id",
      header: "actions.col.id",
      render: (r) => <LtrToken text={r.actionId} />,
    },
    {
      id: "surface",
      header: "actions.col.surface",
      // Actor + originating surface (screen/chat) are not carried by the execution
      // record; rendered explicitly unavailable rather than blanked (PRC-001).
      render: () => unavailableNode(unavailable),
    },
    {
      id: "state",
      header: "actions.col.state",
      render: (r) => <StatusBadge state={EXTERNAL_TO_STATUS[r.externalState]} />,
    },
    {
      id: "reconciled",
      header: "actions.col.time",
      render: (r) => <TimeCell reconciledAt={r.reconciledAt} />,
    },
  ];

  return (
    <div className="screen">
      <FilterChips
        chips={FILTERS.map((f) => ({ id: f.id, labelKey: f.labelKey, active: filter === f.id }))}
        onToggle={(id) => setFilter(id as FilterKey)}
      />

      <ViewState
        pending={Boolean(actionId) && execQuery.isPending}
        error={execQuery.isError}
        isEmpty={!actionId || !exec}
        onRetry={() => void execQuery.refetch()}
      >
        <div className="split">
          <div className="split__main">
            <Section titleKey="actions.list.title">
              <DataTable columns={columns} rows={rows} rowKey={(r) => r.actionId} />
            </Section>

            {exec ? detailFor(exec) : null}
          </div>

          <aside className="split__aside">
            {exec?.externalState === "accepted" ? (
              <Section titleKey="actions.outcome.title">
                {outcomeQuery.data ? (
                  <dl className="kv" data-testid="outcome-window">
                    <div className="kv__row">
                      <dt>{t("actions.outcome.opened")}</dt>
                      <dd>{formatInstant(outcomeQuery.data.openedAt, locale)}</dd>
                    </div>
                    <div className="kv__row">
                      <dt>{t("actions.outcome.closes")}</dt>
                      <dd>{formatInstant(outcomeQuery.data.closesAt, locale)}</dd>
                    </div>
                    <div className="kv__row">
                      <dt>{t("actions.outcome.result")}</dt>
                      <dd>
                        {outcomeQuery.data.result ? (
                          <span data-testid="outcome-result">
                            {t(`outcomeResult.${outcomeQuery.data.result.result}` as MessageKey)}
                          </span>
                        ) : (
                          <span className="muted">{t("actions.outcome.open")}</span>
                        )}
                      </dd>
                    </div>
                    <div className="kv__row">
                      <dt>{t("actions.outcome.confidence")}</dt>
                      <dd>
                        {outcomeQuery.data.result ? (
                          <span data-testid="outcome-confidence">
                            {t(
                              `outcomeConfidence.${outcomeQuery.data.result.confidence}` as MessageKey,
                            )}
                          </span>
                        ) : (
                          unavailableNode(unavailable)
                        )}
                      </dd>
                    </div>
                  </dl>
                ) : (
                  <p className="muted">{unavailable}</p>
                )}
                <p className="muted">{t("actions.outcome.attributionNote")}</p>
              </Section>
            ) : null}

            <Section titleKey="actions.audit.title">
              {auditCard ? (
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
          </aside>
        </div>
      </ViewState>
    </div>
  );

  function detailFor(exec: ActionExecutionView): ReactNode {
    if (exec.externalState === "pending_reconciliation") {
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
              onClick={() => void execQuery.refetch()}
            >
              {t("actions.pending.readState")}
            </button>
          </div>
        </div>
      );
    }

    if (exec.externalState === "failed" || exec.externalState === "rejected") {
      return (
        <div className="panel" data-testid="action-failed">
          <p className="panel__title">{t("actions.failed.title")}</p>
          <p className="muted">{t("actions.failed.body")}</p>
          <div className="row-actions">
            <button
              type="button"
              className="btn btn--secondary"
              data-testid="action-retry"
              disabled={retry.isPending}
              onClick={() => retry.mutate(exec.actionId)}
            >
              {t("actions.action.retry")}
            </button>
          </div>
          {retry.data ? (
            <p className="muted" data-testid="retry-outcome">
              {retry.data.eligible ? t("actions.retry.eligible") : t("actions.retry.ineligible")}
            </p>
          ) : null}
        </div>
      );
    }

    // accepted
    return (
      <div className="panel" data-testid="action-accepted">
        <p className="success-note">
          {t("state.accepted", { marketplace: t("marketplace.name") })}
        </p>
        {exec.externalRef ? (
          <p className="muted">
            {t("actions.accepted.externalRef")} <LtrToken text={exec.externalRef} />
          </p>
        ) : null}
      </div>
    );
  }
}
