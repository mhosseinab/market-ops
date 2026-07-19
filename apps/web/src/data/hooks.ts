import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { gateway } from "../app/query";
import { useAccount } from "./account";
import { type ErrorEnvelope, GatewayError } from "./errors";
import type {
  ActionExecutionView,
  ApprovalBinding,
  ApprovalCardView,
  ApprovalConfirmResult,
  BulkApprovalConfirmRequest,
  BulkApprovalConfirmResult,
  CostImportCommitResult,
  CostImportPreview,
  CostProfileVersion,
  EventRelevanceKind,
  MarginReadiness,
  MarketEvent,
  OutcomeView,
  RecommendationDetail,
  RetryActionResult,
  SessionInfo,
  SingleCostEntryRequest,
  TodayFeed,
} from "./types";

// TanStack Query hooks over the GENERATED gateway client. Every hook is a thin,
// read-what-the-API-returns wrapper; no money/policy/readiness is recomputed
// here. openapi-fetch returns `{ data, error, response }`; `unwrap` reifies a
// structured error envelope into a `GatewayError` (carrying HTTP status, machine
// `code`, and correlation `requestId`) so Query/mutation consumers can render an
// ACTIONABLE, LOCALIZED error surface rather than a silent fallback (issue #82).

function unwrap<T>(result: { data?: T; error?: unknown; response?: Response }): T {
  if (result.error) {
    throw new GatewayError(result.error as ErrorEnvelope, result.response?.status);
  }
  if (result.data === undefined) throw new Error("empty_response");
  return result.data;
}

export const queryKeys = {
  connectorStatus: (accountId: string) => ["connector-status", accountId] as const,
  catalogProducts: (accountId: string, cursor: string) =>
    ["catalog-products", accountId, cursor] as const,
  catalogProduct: (accountId: string, variantId: string) =>
    ["catalog-product", accountId, variantId] as const,
  targets: (accountId: string) => ["observation-targets", accountId] as const,
  offers: (accountId: string) => ["observed-offers", accountId] as const,
  observations: (targetId: string) => ["observations", targetId] as const,
  readiness: (variantId: string) => ["margin-readiness", variantId] as const,
  costProfiles: (variantId: string) => ["cost-profiles", variantId] as const,
  needsReview: (accountId: string) => ["needs-review", accountId] as const,
  today: (accountId: string) => ["today", accountId] as const,
  event: (eventId: string) => ["event", eventId] as const,
  recommendationDetail: (recommendationId: string) =>
    ["recommendation-detail", recommendationId] as const,
  approvalCard: (cardId: string) => ["approval-card", cardId] as const,
  session: () => ["session"] as const,
  actionExecution: (actionId: string) => ["action-execution", actionId] as const,
  outcome: (actionId: string) => ["outcome", actionId] as const,
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

// The canonical Products read model (S26, PRD §6.1). Rows come from Product/
// Variant/Owned Offer entities — never observation targets. Pagination is by the
// stable native_variant_id cursor the server returns; the screen advances it.
export function useCatalogProducts(cursor: string | null) {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    queryKey: queryKeys.catalogProducts(marketplaceAccountId, cursor ?? ""),
    queryFn: async () =>
      unwrap(
        await gateway.GET("/catalog/products", {
          params: {
            query: {
              marketplaceAccountId,
              ...(cursor ? { cursor } : {}),
            },
          },
        }),
      ),
  });
}

// The single-variant canonical Product row backing Product detail (S26).
export function useCatalogProduct(variantId: string | undefined) {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    enabled: Boolean(variantId),
    queryKey: queryKeys.catalogProduct(marketplaceAccountId, variantId ?? ""),
    queryFn: async () =>
      unwrap(
        await gateway.GET("/catalog/product", {
          params: {
            query: { marketplaceAccountId, variantId: variantId as string },
          },
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

// The AUTHORITATIVE PRC-001 record for one persisted recommendation version
// (S37 read seam): objective, current/proposed price + contribution, the §9.2
// deduction breakdown, allowed range, evidence quality/age, readiness,
// assumptions, and blockers (PRC-002, in policy order). A pure read — it never
// advances state or mints a control (§8). Rendered verbatim; nothing recomputed.
export function useRecommendationDetail(recommendationId: string | undefined) {
  return useQuery({
    enabled: Boolean(recommendationId),
    queryKey: queryKeys.recommendationDetail(recommendationId ?? ""),
    queryFn: async (): Promise<RecommendationDetail> =>
      unwrap(
        await gateway.GET("/recommendations/detail", {
          params: { query: { recommendationId: recommendationId as string } },
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

// Starts an idempotent catalog sync (ACC-004/ACC-005, issue #76). The ONLY
// control that initiates a sync — capability support alone never advances
// onboarding. The server gates on catalog_read (fail closed) and collapses a
// duplicate request while a run is in-flight; the returned status carries the
// durable catalogSync state. On success we refetch connector status so the
// stepper re-derives completion from durable evidence, not capability.
export function useSyncCatalog() {
  const { marketplaceAccountId } = useAccount();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(await gateway.POST("/connector/catalog/sync", { body: { marketplaceAccountId } })),
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
      // The confirm surface branches on the machine-readable error code
      // (permission-denied vs idempotency/duplicate); GatewayError carries `code`
      // (plus status/requestId) so the shared error surface can render it too.
      const res = await gateway.POST("/approvals/confirm", {
        body: { cardId: cardId as string, binding },
      });
      if (res.error) throw new GatewayError(res.error as ErrorEnvelope, res.response?.status);
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

// ── S28: session / actions-outcomes / bulk ──────────────────────────────────

// The current principal (ACC-002): role drives the SHARED permission matrix that
// gates L3 guardrail edits (Owner-only) and the internal Operations surfaces. The
// screen renders what the session says; it never infers or elevates a role.
export function useSession() {
  return useQuery({
    queryKey: queryKeys.session(),
    queryFn: async (): Promise<SessionInfo> => unwrap(await gateway.GET("/auth/me")),
  });
}

// An action's single EXE-002 execution record (CHAT-073 read): mode + EXE-003
// external state + reconciliation instant. The external state is rendered exactly
// as given — pending_reconciliation is NEVER coerced to success/failure.
export function useActionExecution(actionId: string | undefined) {
  return useQuery({
    enabled: Boolean(actionId),
    queryKey: queryKeys.actionExecution(actionId ?? ""),
    queryFn: async (): Promise<ActionExecutionView> =>
      unwrap(
        await gateway.GET("/actions/execution", {
          params: { query: { actionId: actionId as string } },
        }),
      ),
  });
}

// The OUT-001 seven-day outcome window and, once closed, its §15.3 result +
// confidence (or Not Measurable). A read; it never advances state.
export function useOutcome(actionId: string | undefined) {
  return useQuery({
    enabled: Boolean(actionId),
    queryKey: queryKeys.outcome(actionId ?? ""),
    queryFn: async (): Promise<OutcomeView> =>
      unwrap(
        await gateway.GET("/outcomes", {
          params: { query: { actionId: actionId as string } },
        }),
      ),
  });
}

// Retry eligibility (EXE-003 / CHAT-074). The server REFUSES an action still in
// pending_reconciliation — an unknown result must reconcile first. The screen
// never even offers this control for an unreconciled action; this hook exists for
// the definitively-Failed path, and the actual re-write is a fresh approval card.
export function useRetryAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (actionId: string): Promise<RetryActionResult> => {
      const res = await gateway.POST("/actions/retry", { body: { actionId } });
      if (res.error) throw new GatewayError(res.error as ErrorEnvelope, res.response?.status);
      if (res.data === undefined) throw new Error("empty_response");
      return res.data;
    },
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: queryKeys.actionExecution(r.actionId) });
    },
  });
}

// The ONLY bulk-approval path (§8, free-text containment): a structured control
// POST bound to ONE exact selection-set version (CHAT-052). The server rejects a
// stale bound version (any set/evidence change mints a new version). Free text can
// never satisfy this contract; the payload carries the bound version verbatim.
export function useBulkConfirm() {
  return useMutation({
    mutationFn: async (req: BulkApprovalConfirmRequest): Promise<BulkApprovalConfirmResult> =>
      unwrap(await gateway.POST("/approvals/bulk/confirm", { body: req })),
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
