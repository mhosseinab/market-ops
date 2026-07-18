import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { gateway } from "../app/query";
import { useAccount } from "./account";
import type {
  ApprovalBinding,
  ApprovalCardView,
  ApprovalConfirmResult,
  CostImportCommitResult,
  CostImportPreview,
  CostProfileVersion,
  EventRelevanceKind,
  MarginReadiness,
  MarketEvent,
  SingleCostEntryRequest,
  TodayFeed,
} from "./types";

// TanStack Query hooks over the GENERATED gateway client. Every hook is a thin,
// read-what-the-API-returns wrapper; no money/policy/readiness is recomputed
// here. openapi-fetch returns `{ data, error }`; `unwrap` turns a structured
// error envelope into a thrown error so Query surfaces the error state.

function unwrap<T>(result: { data?: T; error?: unknown }): T {
  if (result.error) {
    const env = result.error as { code?: string; message?: string };
    throw new Error(env.message ?? env.code ?? "request_failed");
  }
  if (result.data === undefined) throw new Error("empty_response");
  return result.data;
}

export const queryKeys = {
  connectorStatus: (accountId: string) => ["connector-status", accountId] as const,
  targets: (accountId: string) => ["observation-targets", accountId] as const,
  offers: (accountId: string) => ["observed-offers", accountId] as const,
  observations: (targetId: string) => ["observations", targetId] as const,
  readiness: (variantId: string) => ["margin-readiness", variantId] as const,
  costProfiles: (variantId: string) => ["cost-profiles", variantId] as const,
  needsReview: (accountId: string) => ["needs-review", accountId] as const,
  today: (accountId: string) => ["today", accountId] as const,
  event: (eventId: string) => ["event", eventId] as const,
  approvalCard: (cardId: string) => ["approval-card", cardId] as const,
};

export function useConnectorStatus() {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    queryKey: queryKeys.connectorStatus(marketplaceAccountId),
    queryFn: async () =>
      unwrap(
        await gateway.GET("/connector/status", {
          params: { query: { marketplaceAccountId } },
        }),
      ),
  });
}

export function useObservationTargets() {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    queryKey: queryKeys.targets(marketplaceAccountId),
    queryFn: async () =>
      unwrap(
        await gateway.GET("/observation/targets", {
          params: { query: { marketplaceAccountId } },
        }),
      ),
  });
}

export function useObservedOffers() {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    queryKey: queryKeys.offers(marketplaceAccountId),
    queryFn: async () =>
      unwrap(
        await gateway.GET("/observation/observed-offers", {
          params: { query: { marketplaceAccountId } },
        }),
      ),
  });
}

export function useObservations(targetId: string | undefined) {
  return useQuery({
    enabled: Boolean(targetId),
    queryKey: queryKeys.observations(targetId ?? ""),
    queryFn: async () =>
      unwrap(
        await gateway.GET("/observation/observations", {
          params: { query: { targetId: targetId as string } },
        }),
      ),
  });
}

export function useMarginReadiness(variantId: string | undefined) {
  return useQuery({
    enabled: Boolean(variantId),
    queryKey: queryKeys.readiness(variantId ?? ""),
    queryFn: async (): Promise<MarginReadiness> =>
      unwrap(
        await gateway.GET("/cost/readiness", {
          params: { query: { variantId: variantId as string } },
        }),
      ),
  });
}

export function useCostProfiles(variantId: string | undefined) {
  return useQuery({
    enabled: Boolean(variantId),
    queryKey: queryKeys.costProfiles(variantId ?? ""),
    queryFn: async (): Promise<{ items: CostProfileVersion[] }> =>
      unwrap(
        await gateway.GET("/cost/profiles", {
          params: { query: { variantId: variantId as string } },
        }),
      ),
  });
}

export function useNeedsReview() {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    queryKey: queryKeys.needsReview(marketplaceAccountId),
    queryFn: async () =>
      unwrap(
        await gateway.GET("/identity/needs-review", {
          params: { query: { marketplaceAccountId } },
        }),
      ),
  });
}

export function useTodayFeed() {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    queryKey: queryKeys.today(marketplaceAccountId),
    queryFn: async (): Promise<TodayFeed> =>
      unwrap(
        await gateway.GET("/today", {
          params: { query: { marketplaceAccountId } },
        }),
      ),
  });
}

export function useEvent(eventId: string | undefined) {
  return useQuery({
    enabled: Boolean(eventId),
    queryKey: queryKeys.event(eventId ?? ""),
    queryFn: async (): Promise<MarketEvent> =>
      unwrap(
        await gateway.GET("/event", {
          params: { query: { eventId: eventId as string } },
        }),
      ),
  });
}

// The approval card POLLS (APR-001 / journey 6): a version change under a live
// control must be observed so the surface can disable the stale control and
// offer recalculate. Polling is the read side; it never advances state.
export function useApprovalCard(cardId: string | undefined) {
  return useQuery({
    enabled: Boolean(cardId),
    queryKey: queryKeys.approvalCard(cardId ?? ""),
    refetchInterval: 4000,
    queryFn: async (): Promise<ApprovalCardView> =>
      unwrap(
        await gateway.GET("/approvals/card", {
          params: { query: { cardId: cardId as string } },
        }),
      ),
  });
}

// ── Mutations ──────────────────────────────────────────────────────────────

export function useConnect() {
  const { marketplaceAccountId } = useAccount();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (authorizationCode: string) =>
      unwrap(
        await gateway.POST("/connector/connect", {
          body: { marketplaceAccountId, authorizationCode },
        }),
      ),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: queryKeys.connectorStatus(marketplaceAccountId) }),
  });
}

export function useConnectorAction(path: "/connector/refresh" | "/connector/disconnect") {
  const { marketplaceAccountId } = useAccount();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => unwrap(await gateway.POST(path, { body: { marketplaceAccountId } })),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: queryKeys.connectorStatus(marketplaceAccountId) }),
  });
}

export function useCostImportPreview() {
  const { marketplaceAccountId } = useAccount();
  return useMutation({
    mutationFn: async (args: { csv: string; filename?: string }): Promise<CostImportPreview> =>
      unwrap(
        await gateway.POST("/cost/import/preview", {
          body: {
            marketplaceAccountId,
            csv: args.csv,
            ...(args.filename ? { filename: args.filename } : {}),
          },
        }),
      ),
  });
}

export function useCostImportCommit() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (batchId: string): Promise<CostImportCommitResult> =>
      unwrap(await gateway.POST("/cost/import/commit", { body: { batchId } })),
    onSuccess: (result) => {
      for (const variantId of result.affectedVariantIds) {
        void qc.invalidateQueries({ queryKey: queryKeys.readiness(variantId) });
        void qc.invalidateQueries({ queryKey: queryKeys.costProfiles(variantId) });
      }
    },
  });
}

export function useSingleCostEntry() {
  const { marketplaceAccountId } = useAccount();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (
      args: Omit<SingleCostEntryRequest, "marketplaceAccountId">,
    ): Promise<CostProfileVersion> =>
      unwrap(await gateway.POST("/cost/value", { body: { marketplaceAccountId, ...args } })),
    onSuccess: (version) => {
      void qc.invalidateQueries({ queryKey: queryKeys.readiness(version.variantId) });
      void qc.invalidateQueries({ queryKey: queryKeys.costProfiles(version.variantId) });
    },
  });
}

// The ONLY individual-approval path (§8, free-text containment): a structured
// control POST carrying the EXACT bound versions. Free text can never satisfy
// this contract. On success we refetch the card so its new §8.4 state renders.
export function useConfirmApproval(cardId: string | undefined) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (binding: ApprovalBinding): Promise<ApprovalConfirmResult> => {
      // Unlike other reads, the confirm surface must branch on the machine-readable
      // error code (permission-denied vs idempotency/duplicate), so preserve it on
      // the thrown error rather than collapsing to a message string.
      const res = await gateway.POST("/approvals/confirm", {
        body: { cardId: cardId as string, binding },
      });
      if (res.error) {
        const env = res.error as { code?: string; message?: string };
        throw Object.assign(new Error(env.message ?? env.code ?? "confirm_failed"), {
          code: env.code,
        });
      }
      if (res.data === undefined) throw new Error("empty_response");
      return res.data;
    },
    onSuccess: () => {
      if (cardId) void qc.invalidateQueries({ queryKey: queryKeys.approvalCard(cardId) });
    },
  });
}

export function useEventRelevance() {
  return useMutation({
    mutationFn: async (args: { eventId: string; relevance: EventRelevanceKind; note?: string }) =>
      unwrap(
        await gateway.POST("/events/relevance", {
          body: {
            eventId: args.eventId,
            relevance: args.relevance,
            ...(args.note ? { note: args.note } : {}),
          },
        }),
      ),
  });
}

export function useIdentityDecision(
  path: "/identity/confirm" | "/identity/reject" | "/identity/defer",
) {
  const { marketplaceAccountId } = useAccount();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (args: { identityId: string; note?: string }) =>
      unwrap(
        await gateway.POST(path, {
          body: { identityId: args.identityId, ...(args.note ? { note: args.note } : {}) },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.needsReview(marketplaceAccountId) });
      void qc.invalidateQueries({ queryKey: queryKeys.targets(marketplaceAccountId) });
    },
  });
}
