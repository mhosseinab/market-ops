import type { MessageKey } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { type ReactNode, useEffect, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { ApprovalCard } from "../components/ApprovalCard";
import { QualityBadge, ReadinessBadge } from "../components/badges";
import { ContributionBreakdown } from "../components/ContributionBreakdown";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { Section } from "../components/primitives";
import { StateMachineView } from "../components/StateMachineView";
import { ViewState } from "../components/ViewState";
import { formatInstant } from "../data/format";
import { useApprovalCard, useConfirmApproval, useRecommendationDetail } from "../data/hooks";
import type {
  ApprovalConfirmResult,
  MoneyAmount,
  PolicyObjective,
  RecommendationDetail,
} from "../data/types";

// Recommendation + approval (design screen 3 / PRC-001 / APR-001): the core
// safety surface. The ApprovalCard is THE only mutation control; free text never
// confirms (§8). The right column binds to the AUTHORITATIVE getRecommendationDetail
// read (S37) so every PRC-001 field renders from server truth — never fabricated,
// never a blanket placeholder. Optional fields the payload genuinely omits render
// present-or-unavailable-with-STRUCTURED-reason. The StateMachineView renders the
// §8.4 lifecycle, the eight revalidation gates, and the Invalidated / Expired /
// permission-denied / recommend-only branches.

// The optimization objective (§9.3) is a closed enum → a typed catalog-key map.
const OBJECTIVE_LABEL: Record<PolicyObjective, MessageKey> = {
  maximize_contribution: "rec.objective.maximize_contribution",
  track_strategy: "rec.objective.track_strategy",
};

function fieldRow(label: string, node: ReactNode, testId?: string): ReactNode {
  return (
    <div className="kv__row" key={label}>
      <dt>{label}</dt>
      <dd {...(testId ? { "data-testid": testId } : {})}>{node}</dd>
    </div>
  );
}

function confirmErrorCode(error: unknown): string | undefined {
  return (error as { code?: string } | null)?.code;
}

/** A structured "unavailable — {reason}" (never blank, never fabricated). */
function Unavailable({ reasonKey }: { reasonKey: MessageKey }) {
  const t = useT();
  return <span className="muted">{t("rec.unavailable", { reason: t(reasonKey) })}</span>;
}

/** A money amount present-or-unavailable-with-reason (optional payload fields). */
function MoneyOrAbsent({ amount }: { amount?: MoneyAmount }) {
  if (!amount) return <Unavailable reasonKey="rec.unavailable.reason.absent" />;
  return <MoneyView amount={amount} />;
}

// The authoritative PRC-001 field set, bound verbatim to the detail payload.
function RecommendationFields({ detail }: { detail: RecommendationDetail }) {
  const t = useT();
  const { locale } = useLocale();
  const range = detail.allowedRange;

  return (
    <dl className="kv" data-testid="prc-fields">
      {fieldRow(t("rec.field.objective"), <span>{t(OBJECTIVE_LABEL[detail.objective])}</span>)}
      {fieldRow(
        t("rec.price.current"),
        <MoneyView amount={detail.currentPrice} />,
        "prc-currentPrice",
      )}
      {fieldRow(
        t("rec.price.proposed"),
        <MoneyOrAbsent amount={detail.proposedPrice} />,
        "prc-proposedPrice",
      )}
      {fieldRow(
        t("rec.field.currentContribution"),
        <MoneyOrAbsent amount={detail.currentContribution} />,
        "prc-currentContribution",
      )}
      {fieldRow(
        t("rec.field.proposedContribution"),
        <MoneyOrAbsent amount={detail.proposedContribution} />,
        "prc-proposedContribution",
      )}
      {fieldRow(
        t("rec.field.allowedRange"),
        range?.known && range.min && range.max ? (
          <span className="component-list">
            <span className="chip">
              {t("rec.range.min")} <MoneyView amount={range.min} />
            </span>
            <span className="chip">
              {t("rec.range.max")} <MoneyView amount={range.max} />
            </span>
          </span>
        ) : (
          <Unavailable reasonKey="rec.unavailable.reason.boundaryUnknown" />
        ),
        "prc-allowedRange",
      )}
      {fieldRow(
        t("rec.field.quality"),
        <QualityBadge state={detail.evidenceQuality} />,
        "prc-quality",
      )}
      {fieldRow(
        t("rec.field.readiness"),
        <ReadinessBadge state={detail.readiness} />,
        "prc-readiness",
      )}
      {fieldRow(
        t("rec.field.evidenceAge"),
        detail.evidenceAsOf ? (
          <span className="component-list">
            <span>{formatInstant(detail.evidenceAsOf, locale)}</span>
            {detail.evidenceObservationId ? (
              <span className="chip">
                <LtrToken text={detail.evidenceObservationId} />
              </span>
            ) : null}
          </span>
        ) : (
          <Unavailable reasonKey="rec.unavailable.reason.absent" />
        ),
        "prc-evidenceAge",
      )}
      {fieldRow(
        t("rec.field.assumptions"),
        detail.assumptions.length > 0 ? (
          <ul className="plain-list" data-testid="rec-assumptions">
            {detail.assumptions.map((a) => (
              <li key={a}>
                <LtrToken text={a} />
              </li>
            ))}
          </ul>
        ) : (
          <span className="muted">{t("rec.assumptions.none")}</span>
        ),
        "prc-assumptions",
      )}
      {fieldRow(
        t("rec.field.blockers"),
        detail.blockers.length > 0 ? (
          <ol className="blocker-list" data-testid="rec-blockers">
            {detail.blockers.map((b) => (
              <li key={`${b.code}:${b.message}`}>
                <LtrToken text={b.code} /> <span>{b.message}</span>
              </li>
            ))}
          </ol>
        ) : (
          <span className="muted">{t("rec.blockers.none")}</span>
        ),
        "prc-blockers",
      )}
      {fieldRow(
        t("rec.field.approvable"),
        <span>{t(detail.approvable ? "rec.approvable.yes" : "rec.approvable.no")}</span>,
        "prc-approvable",
      )}
      {fieldRow(
        t("rec.field.simulation"),
        <span>{t(detail.simulation ? "rec.simulation.on" : "rec.simulation.off")}</span>,
        "prc-simulation",
      )}
      {fieldRow(
        t("rec.field.event"),
        detail.eventId ? (
          <span className="chip">
            <LtrToken text={detail.eventId} />
          </span>
        ) : (
          <Unavailable reasonKey="rec.unavailable.reason.absent" />
        ),
        "prc-event",
      )}
    </dl>
  );
}

export function Recommendation() {
  const t = useT();
  const { locale } = useLocale();
  const search = useRouterState({
    select: (s) => s.location.search as { cardId?: string; recommendationId?: string },
  });
  const cardId = search.cardId;
  const cardQuery = useApprovalCard(cardId);
  const confirm = useConfirmApproval(cardId);
  const card = cardQuery.data;

  // The authoritative recommendation id: an explicit deep-link param wins,
  // otherwise it is resolved from the loaded card (APR-001 mints the card from
  // the same PRC-001 record). The detail read is the source of truth for the
  // field set; the card supplies the live control + its bound versions.
  const recommendationId = search.recommendationId ?? card?.recommendationId;
  const detailQuery = useRecommendationDetail(recommendationId);
  const detail = detailQuery.data;

  // The version the live control is bound to. Set on first load and re-adopted on
  // recalculate; a polled version change under it flags the control stale.
  const [baseline, setBaseline] = useState<number | null>(null);
  const [result, setResult] = useState<ApprovalConfirmResult | null>(null);
  useEffect(() => {
    if (card && baseline === null) setBaseline(card.version);
  }, [card, baseline]);

  const errorCode = confirm.isError ? confirmErrorCode(confirm.error) : undefined;
  const permissionDenied = Boolean(errorCode?.includes("permission"));
  const duplicate = Boolean(errorCode && /(idempoten|duplicate)/.test(errorCode));

  const hasContribution =
    detail && (detail.contributionDeductions.length > 0 || detail.proposedContribution);

  // The authoritative field set renders through its own state seam so an
  // unavailable detail read degrades to the approval card (screens-only fallback,
  // §8) rather than blanking the surface.
  const detailFields = (
    <ViewState
      pending={Boolean(recommendationId) && detailQuery.isPending}
      error={false}
      skeletonRows={6}
    >
      {detailQuery.isError ? (
        <div className="view-error" role="alert" data-testid="rec-detail-degraded">
          <p className="view-error__title">{t("rec.detail.degraded.title")}</p>
          <p className="view-error__body">{t("rec.detail.degraded.body")}</p>
          <button
            type="button"
            className="btn btn--secondary"
            data-testid="rec-detail-retry"
            onClick={() => void detailQuery.refetch()}
          >
            {t("action.retry")}
          </button>
        </div>
      ) : detail ? (
        <RecommendationFields detail={detail} />
      ) : null}
    </ViewState>
  );

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

              {hasContribution ? (
                <Section titleKey="rec.contribution.title">
                  <ContributionBreakdown
                    deductions={detail.contributionDeductions}
                    total={detail.proposedContribution}
                    readiness={detail.readiness}
                  />
                </Section>
              ) : null}
            </div>

            <aside className="split__aside">
              <Section titleKey="rec.fields.title">
                {detailFields}

                {/* The live control's BOUND versions (APR-001) come from the card,
                    not the recommendation read — they gate the exact confirmation. */}
                <dl className="kv" data-testid="prc-inputs">
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
                    t("rec.card.expiry"),
                    <span data-expires-at={detail?.expiresAt ?? card.binding.expiresAt}>
                      {formatInstant(detail?.expiresAt ?? card.binding.expiresAt, locale)}
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
