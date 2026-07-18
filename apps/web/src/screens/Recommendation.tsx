import { useRouterState } from "@tanstack/react-router";
import { type ReactNode, useEffect, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { ApprovalCard } from "../components/ApprovalCard";
import { ContributionBreakdown } from "../components/ContributionBreakdown";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { Section } from "../components/primitives";
import { StateMachineView } from "../components/StateMachineView";
import { ViewState } from "../components/ViewState";
import { formatInstant } from "../data/format";
import { useApprovalCard, useConfirmApproval } from "../data/hooks";
import type { ApprovalConfirmResult, Contribution } from "../data/types";

// Recommendation + approval (design screen 3 / PRC-001 / APR-001): the core
// safety surface. The ApprovalCard is THE only mutation control; free text never
// confirms. Every PRC-001 field is present or explicitly rendered
// unavailable-with-reason (never blank, never fabricated). The StateMachineView
// renders the §8.4 lifecycle, the eight revalidation gates, and the
// Invalidated / Expired / permission-denied / recommend-only branches.
//
// Fields the current gateway contract does not surface (current price,
// contribution, allowed range, objective, quality, readiness, assumptions) are
// shown unavailable-with-reason per PRC-001 optionality — see the carry-forward
// note in the S27 handoff.

function fieldRow(label: string, node: ReactNode): ReactNode {
  return (
    <div className="kv__row" key={label}>
      <dt>{label}</dt>
      <dd>{node}</dd>
    </div>
  );
}

function confirmErrorCode(error: unknown): string | undefined {
  return (error as { code?: string } | null)?.code;
}

export function Recommendation() {
  const t = useT();
  const { locale } = useLocale();
  const cardId = useRouterState({
    select: (s) => (s.location.search as { cardId?: string }).cardId,
  });
  const cardQuery = useApprovalCard(cardId);
  const confirm = useConfirmApproval(cardId);
  const card = cardQuery.data;

  // The version the live control is bound to. Set on first load and re-adopted on
  // recalculate; a polled version change under it flags the control stale.
  const [baseline, setBaseline] = useState<number | null>(null);
  const [result, setResult] = useState<ApprovalConfirmResult | null>(null);
  useEffect(() => {
    if (card && baseline === null) setBaseline(card.version);
  }, [card, baseline]);

  const unavailable = t("rec.unavailable", { reason: t("rec.unavailable.reason.notSurfaced") });
  const errorCode = confirm.isError ? confirmErrorCode(confirm.error) : undefined;
  const permissionDenied = Boolean(errorCode?.includes("permission"));
  const duplicate = Boolean(errorCode && /(idempoten|duplicate)/.test(errorCode));

  // Contribution is not carried by the approval-card contract in P0; the
  // component renders it faithfully whenever the API does surface one.
  const contribution: Contribution | undefined = undefined;

  return (
    <div className="screen">
      <ViewState
        pending={Boolean(cardId) && cardQuery.isPending}
        error={cardQuery.isError}
        onRetry={() => void cardQuery.refetch()}
      >
        {!cardId || !card ? (
          <div className="screen-empty">
            <p>{t("rec.noCard")}</p>
          </div>
        ) : (
          <div className="split">
            <div className="split__main">
              <ApprovalCard
                card={card}
                baselineVersion={baseline ?? card.version}
                confirmPending={confirm.isPending}
                onConfirm={(binding) => {
                  setResult(null);
                  confirm.mutate(binding, { onSuccess: (r) => setResult(r) });
                }}
                onRecalculate={() => {
                  setBaseline(card.version);
                  setResult(null);
                  confirm.reset();
                  void cardQuery.refetch();
                }}
              />

              <StateMachineView
                state={result?.state ?? card.state}
                reason={result?.reason ?? ""}
                executionPending={result?.executionPending ?? false}
                permissionDenied={permissionDenied}
                idempotencyKey={card.idempotencyKey}
                onRecalculate={() => {
                  setBaseline(card.version);
                  setResult(null);
                  confirm.reset();
                  void cardQuery.refetch();
                }}
                onRequestOwner={() => confirm.reset()}
              />

              {duplicate ? (
                <p className="blocker-note" data-testid="confirm-duplicate">
                  {t("rec.confirm.duplicate")}
                </p>
              ) : null}

              {contribution ? (
                <Section titleKey="rec.contribution.title">
                  <ContributionBreakdown contribution={contribution} />
                </Section>
              ) : null}
            </div>

            <aside className="split__aside">
              <Section titleKey="rec.fields.title">
                <dl className="kv" data-testid="prc-fields">
                  {fieldRow(t("rec.field.objective"), <span className="muted">{unavailable}</span>)}
                  {fieldRow(t("rec.price.current"), <span className="muted">{unavailable}</span>)}
                  {fieldRow(t("rec.price.proposed"), <MoneyView amount={card.price} />)}
                  {fieldRow(
                    t("rec.field.currentContribution"),
                    <span className="muted">{unavailable}</span>,
                  )}
                  {fieldRow(
                    t("rec.field.proposedContribution"),
                    <span className="muted">{unavailable}</span>,
                  )}
                  {fieldRow(
                    t("rec.field.allowedRange"),
                    <span className="muted">{unavailable}</span>,
                  )}
                  {fieldRow(
                    t("rec.field.inputs"),
                    <span className="component-list">
                      <span className="chip">
                        {t("rec.inputs.parameterVersion", {
                          version: card.binding.parameterVersion,
                        })}
                      </span>
                      <span className="chip">
                        {t("rec.inputs.contextVersion", { version: card.binding.contextVersion })}
                      </span>
                      <span className="chip">
                        {t("rec.inputs.policyVersion", { version: card.binding.policyVersion })}
                      </span>
                      <span className="chip">
                        {t("rec.inputs.costVersion", { version: card.binding.costProfileVersion })}
                      </span>
                    </span>,
                  )}
                  {fieldRow(
                    t("rec.field.evidence"),
                    card.binding.evidenceVersions.length > 0 ? (
                      <span className="component-list">
                        {card.binding.evidenceVersions.map((e) => (
                          <span key={e.observationId} className="chip">
                            <LtrToken text={`${e.observationId}·v${e.version}`} />
                          </span>
                        ))}
                      </span>
                    ) : (
                      <span className="muted">{unavailable}</span>
                    ),
                  )}
                  {fieldRow(
                    t("rec.field.age"),
                    card.history.length > 0 ? (
                      <span>{formatInstant(card.history[0]?.occurredAt as string, locale)}</span>
                    ) : (
                      <span className="muted">{unavailable}</span>
                    ),
                  )}
                  {fieldRow(t("rec.field.quality"), <span className="muted">{unavailable}</span>)}
                  {fieldRow(t("rec.field.readiness"), <span className="muted">{unavailable}</span>)}
                  {fieldRow(
                    t("rec.field.assumptions"),
                    <span className="muted">{unavailable}</span>,
                  )}
                  {fieldRow(
                    t("rec.field.blockers"),
                    card.state === "blocked" && card.history[0] ? (
                      <span>{card.history[0].reason}</span>
                    ) : (
                      <span className="muted">{unavailable}</span>
                    ),
                  )}
                  {fieldRow(
                    t("rec.card.expiry"),
                    <span data-expires-at={card.binding.expiresAt}>
                      {formatInstant(card.binding.expiresAt, locale)}
                    </span>,
                  )}
                </dl>
              </Section>

              <Section titleKey="rec.evidenceChips.title">
                {card.binding.evidenceVersions.length > 0 ? (
                  <span className="component-list">
                    {card.binding.evidenceVersions.map((e) => (
                      <span key={e.observationId} className="chip">
                        <LtrToken text={e.observationId} />
                      </span>
                    ))}
                  </span>
                ) : (
                  <p className="muted">{t("common.notAvailable")}</p>
                )}
              </Section>
            </aside>
          </div>
        )}
      </ViewState>
    </div>
  );
}
