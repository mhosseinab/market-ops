import { useEffect, useState } from "react";
import { useT } from "../../app/i18n";
import { ApprovalCard } from "../../components/ApprovalCard";
import { StateMachineView } from "../../components/StateMachineView";
import { ViewState } from "../../components/ViewState";
import { useApprovalCard, useConfirmApproval } from "../../data/hooks";
import type { ApprovalConfirmResult } from "../../data/types";
import { DeepLinkButton } from "./DeepLinkButton";

// The dock's individual-approval card. It mounts the SAME S27 ApprovalCard +
// StateMachineView and confirms through the SAME gateway `/approvals/confirm`
// endpoint the Recommendation screen uses — chat NEVER owns a confirm path
// (§8, CHAT-041). assistant-ui only laid this out as a data part.
//
// §8.1 cached-control non-reuse: the host is given ONLY a cardId and ALWAYS
// re-fetches the live card (useApprovalCard). A restored conversation therefore
// re-fetches every card; a stale/cached executable control is never reused — the
// rendered control reflects the server's CURRENT state (expired/invalidated →
// disabled), regardless of any snapshot the transcript carried.
export function ApprovalCardHost({ cardId }: { cardId: string }) {
  const t = useT();
  const cardQuery = useApprovalCard(cardId);
  const confirm = useConfirmApproval(cardId);
  const card = cardQuery.data;

  const [baseline, setBaseline] = useState<number | null>(null);
  const [result, setResult] = useState<ApprovalConfirmResult | null>(null);
  useEffect(() => {
    if (card && baseline === null) setBaseline(card.version);
  }, [card, baseline]);

  const errorCode = confirm.isError ? (confirm.error as { code?: string } | null)?.code : undefined;
  const permissionDenied = Boolean(errorCode?.includes("permission"));

  return (
    <section className="chat-card chat-approval" data-testid="chat-approval-card">
      <ViewState
        pending={cardQuery.isPending}
        error={cardQuery.isError}
        onRetry={() => void cardQuery.refetch()}
      >
        {!card ? (
          <p className="chat-card__unavailable" data-testid="chat-approval-unavailable">
            {t("chat.card.unavailable")}
          </p>
        ) : (
          <>
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
            />
            <DeepLinkButton
              link={{ to: "/recommendation", search: { cardId } }}
              testId="chat-approval-deeplink"
            />
          </>
        )}
      </ViewState>
    </section>
  );
}
