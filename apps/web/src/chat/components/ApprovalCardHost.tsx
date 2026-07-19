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
//
// Withholding is explicit, not just "we re-fetch": TanStack Query serves cached
// card data SYNCHRONOUSLY on a remount/restore while that refetch runs in the
// background (isPending is already false, isFetching is true). Gating on isPending
// alone would let a cached executable control reach the reused S27 ApprovalCard
// before the fresh read proves the card is still awaiting confirmation with the
// same binding/version. `isFetchedAfterMount` flips true only once a read has
// SETTLED for THIS mount (success OR error), so no cached hasControl/binding/
// Confirm is ever reused — until then cached data backs only the loading skeleton.
export function ApprovalCardHost({ cardId }: { cardId: string }) {
  const t = useT();
  const cardQuery = useApprovalCard(cardId);
  const confirm = useConfirmApproval(cardId);
  const authoritative = cardQuery.isFetchedAfterMount;
  // Cached data is withheld until the post-mount read settles; only an
  // authoritative card ever backs an executable control.
  const card = authoritative ? cardQuery.data : undefined;

  // Per-card local approval state. A reused host instance whose cardId changes
  // (transcript restore / switching cards) must NOT inherit the prior card's
  // approval baseline or confirm result — reset synchronously on identity change
  // (React's "adjust state during render" pattern) so no cross-card state leaks.
  const [trackedCardId, setTrackedCardId] = useState(cardId);
  const [baseline, setBaseline] = useState<number | null>(null);
  const [result, setResult] = useState<ApprovalConfirmResult | null>(null);
  if (cardId !== trackedCardId) {
    setTrackedCardId(cardId);
    setBaseline(null);
    setResult(null);
  }
  useEffect(() => {
    // Anchor the APR-001 staleness baseline to the FIRST authoritative version,
    // never a cached snapshot (card stays undefined until `authoritative`).
    if (card && baseline === null) setBaseline(card.version);
  }, [card, baseline]);

  const errorCode = confirm.isError ? (confirm.error as { code?: string } | null)?.code : undefined;
  const permissionDenied = Boolean(errorCode?.includes("permission"));

  return (
    <section className="chat-card chat-approval" data-testid="chat-approval-card">
      <ViewState
        pending={!authoritative && !cardQuery.isError}
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
