import type {
  ActionExecutionView,
  ApprovalCardView,
  ApprovalConfirmResult,
  BulkApprovalConfirmResult,
  ConnectorStatus,
  CostImportPreview,
  MarginReadiness,
  MarketEvent,
  NeedsReviewQueue,
  ObservationTarget,
  ObservedOffer,
  OutcomeView,
  SessionInfo,
  TodayFeed,
} from "../../data/types";

// Fixtures mirroring the core contract response shapes (gen/ts schema). Kept
// deliberately small and deterministic so component tests assert on exact values.

export const ACCOUNT_ID = "00000000-0000-0000-0000-000000000003";
export const VARIANT_ID = "11111111-1111-1111-1111-111111111111";
export const TARGET_ID = "22222222-2222-2222-2222-222222222222";
export const IDENTITY_ID = "33333333-3333-3333-3333-333333333333";

const CAPABILITIES: ConnectorStatus["capabilities"] = [
  "catalog_read",
  "owned_offer_read",
  "stock_read",
  "buybox_read",
  "boundary_read",
  "commission_read",
  "sales_context_read",
  "price_write",
  "change_feed",
].map((capability) => ({
  capability: capability as ConnectorStatus["capabilities"][number]["capability"],
  status: "unknown" as const,
}));

/** ACC-001 default: disconnected, every capability Unknown (never enables UI). */
export const connectorUnknown: ConnectorStatus = {
  marketplaceAccountId: ACCOUNT_ID,
  connectionState: "disconnected",
  capabilities: CAPABILITIES,
};

/** Connected account with catalog_read probed Supported. */
export const connectorSupported: ConnectorStatus = {
  marketplaceAccountId: ACCOUNT_ID,
  connectionState: "connected",
  capabilities: CAPABILITIES.map((c) =>
    c.capability === "catalog_read"
      ? { ...c, status: "supported", lastVerified: "2026-07-17T08:00:00Z" }
      : c,
  ),
};

export const target: ObservationTarget = {
  id: TARGET_ID,
  marketplaceAccountId: ACCOUNT_ID,
  identityId: IDENTITY_ID,
  variantId: VARIANT_ID,
  nativeVariantId: 8842213,
  nativeProductId: 7719004,
  tier: "priority",
  cadenceSeconds: 3600,
  freshnessDeadlineSeconds: 3600,
  active: true,
};

export const offer: ObservedOffer = {
  id: "44444444-4444-4444-4444-444444444444",
  targetId: TARGET_ID,
  marketplaceAccountId: ACCOUNT_ID,
  offerIdentity: "8842213:seller-1",
  nativeVariantId: 8842213,
  price: { text: "14,350,000", value: "14350000", unit: "IRR" },
  listPrice: { text: "15,000,000", value: "15000000", unit: "IRR" },
  availabilityStatus: "in_stock",
  stockSignal: 24,
  quality: "verified",
  capturedAt: "2026-07-17T09:00:00Z",
  freshnessDeadline: "2026-07-17T10:00:00Z",
  routes: ["route_c"],
};

export const readinessMissing: MarginReadiness = {
  variantId: VARIANT_ID,
  marketplaceAccountId: ACCOUNT_ID,
  state: "missing",
  missingComponents: ["cogs"],
  staleComponents: [],
  computedAt: "2026-07-17T09:00:00Z",
};

export const needsReviewQueue: NeedsReviewQueue = {
  items: [
    {
      identityId: IDENTITY_ID,
      variantId: VARIANT_ID,
      nativeVariantId: 8842213,
      nativeProductId: 7719004,
      supplierCode: "DKP-8842213",
      variantTitle: "هدفون بی‌سیم سونی",
      productTitle: "WH-1000XM5",
      candidateSource: "exact_native_id",
      version: 1,
    },
  ],
};

const acceptRow: CostImportPreview["rows"][number] = {
  rowNumber: 1,
  sku: "DKP-8842213",
  component: "cogs",
  rawValue: "8900000",
  normalizedValue: "8900000",
  variantId: VARIANT_ID,
  amount: { mantissa: 8900000, currency: "IRR", exponent: 0 },
  disposition: "accept",
  reason: "",
};

/** Preview with a duplicate conflict — commit must stay blocked. */
export const previewWithDuplicate: CostImportPreview = {
  batchId: "55555555-5555-5555-5555-555555555555",
  marketplaceAccountId: ACCOUNT_ID,
  filename: "costs.csv",
  status: "preview",
  counts: { accept: 2, reject: 1, duplicate: 1 },
  detected: {
    skuColumn: "SKU",
    componentColumns: [{ header: "COGS", component: "cogs" }],
  },
  rows: [
    acceptRow,
    {
      rowNumber: 2,
      sku: "DKP-0000-X",
      component: "cogs",
      rawValue: "0",
      normalizedValue: "0",
      variantId: null,
      amount: null,
      disposition: "reject",
      reason: "sku_not_found",
    },
    {
      rowNumber: 3,
      sku: "DKP-4410771",
      component: "cogs",
      rawValue: "1150000",
      normalizedValue: "1150000",
      variantId: VARIANT_ID,
      amount: { mantissa: 1150000, currency: "IRR", exponent: 0 },
      disposition: "duplicate",
      reason: "duplicate_in_file",
    },
  ],
};

/** Clean preview — all rows accept, commit allowed. */
export const previewClean: CostImportPreview = {
  ...previewWithDuplicate,
  batchId: "66666666-6666-6666-6666-666666666666",
  counts: { accept: 1, reject: 0, duplicate: 0 },
  rows: [acceptRow],
};

// ── S27: Today / events / recommendation + approval ─────────────────────────
export const EVENT_ID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
export const EVENT_ID_BLOCKED = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";
export const OBSERVATION_ID = "cccccccc-cccc-cccc-cccc-cccccccccccc";
export const CARD_ID = "dddddddd-dddd-dddd-dddd-dddddddddddd";
export const ACTION_ID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee";
export const RECOMMENDATION_ID = "ffffffff-ffff-ffff-ffff-ffffffffffff";

/** An actionable event: verified evidence, known exposure. */
export const marketEvent: MarketEvent = {
  id: EVENT_ID,
  marketplaceAccountId: ACCOUNT_ID,
  variantId: VARIANT_ID,
  targetId: TARGET_ID,
  type: "competitor_price",
  severity: "warning",
  state: "open",
  factors: {
    exposure: { known: true, amount: { mantissa: 14100000, currency: "IRR", exponent: 0 } },
    confidenceBp: 9200,
    urgencyBp: 6000,
  },
  thresholdVersion: 3,
  evidenceObservationId: OBSERVATION_ID,
  evidenceQuality: "verified",
  evidenceRef: "obs:route_c:8842213",
  firstDetectedAt: "2026-07-17T06:00:00Z",
  lastEvidenceAt: "2026-07-17T09:30:00Z",
  expiresAt: "2026-07-18T06:00:00Z",
  evidenceUpdateCount: 2,
};

/** A blocked event: conflicted observation, unknown exposure (EVT-005). */
export const blockedEvent: MarketEvent = {
  ...marketEvent,
  id: EVENT_ID_BLOCKED,
  type: "winning_state",
  severity: "critical",
  evidenceQuality: "conflicted",
  factors: { exposure: { known: false }, confidenceBp: 4000, urgencyBp: 8000 },
};

export const todayFeed: TodayFeed = {
  items: [
    { event: marketEvent, rank: 1, factors: marketEvent.factors },
    { event: blockedEvent, rank: 2, factors: blockedEvent.factors },
  ],
};

/** A live approval card in AwaitingConfirmation — carries a structured control. */
export const approvalCardAwaiting: ApprovalCardView = {
  id: CARD_ID,
  recommendationId: RECOMMENDATION_ID,
  version: 1,
  state: "awaiting_confirmation",
  binding: {
    actionId: ACTION_ID,
    parameterVersion: 4,
    contextVersion: 2,
    policyVersion: 7,
    costProfileVersion: 5,
    evidenceVersions: [{ observationId: OBSERVATION_ID, version: 3 }],
    expiresAt: "2026-07-17T12:00:00Z",
  },
  price: { mantissa: 13900000, currency: "IRR", exponent: 0 },
  idempotencyKey: "idem-dddddddd",
  hasControl: true,
  history: [{ toState: "awaiting_confirmation", reason: "", occurredAt: "2026-07-17T09:40:00Z" }],
};

/** The same card after a NEW version was minted under a live control (APR-001). */
export const approvalCardV2: ApprovalCardView = {
  ...approvalCardAwaiting,
  version: 2,
  binding: { ...approvalCardAwaiting.binding, parameterVersion: 5 },
};

/** Recommend-only terminal: Approved, execution deferred to S18. */
export const confirmApproved: ApprovalConfirmResult = {
  cardId: CARD_ID,
  state: "approved",
  reason: "",
  executionPending: true,
};

/** APR-001 invalidation: a bound dimension changed under the control. */
export const confirmInvalidated: ApprovalConfirmResult = {
  cardId: CARD_ID,
  state: "invalidated",
  reason: "parameter_version_changed",
  executionPending: false,
};

// ── S28: sessions / actions-outcomes / bulk ─────────────────────────────────
const USER_ID = "10000000-0000-0000-0000-000000000001";
const ORG_ID = "20000000-0000-0000-0000-000000000002";

export const sessionOwner: SessionInfo = {
  userId: USER_ID,
  organizationId: ORG_ID,
  email: "owner@example.com",
  role: "owner",
  expiresAt: "2026-07-18T12:00:00Z",
};

export const sessionOperator: SessionInfo = { ...sessionOwner, role: "operator" };
export const sessionInternal: SessionInfo = { ...sessionOwner, role: "internal" };

/** Unknown external result — never shown as success/failure; no retry (EXE-003). */
export const execPendingReconciliation: ActionExecutionView = {
  actionId: ACTION_ID,
  cardId: CARD_ID,
  mode: "write",
  externalState: "pending_reconciliation",
};

/** Definitively failed — retry-eligible only through a fresh approval card. */
export const execFailed: ActionExecutionView = {
  ...execPendingReconciliation,
  externalState: "failed",
  reconciledAt: "2026-07-17T11:00:00Z",
};

/** Accepted by DK — carries an external ref and opens a 7-day outcome window. */
export const execAccepted: ActionExecutionView = {
  ...execPendingReconciliation,
  externalState: "accepted",
  externalRef: "batch-8842213",
  reconciledAt: "2026-07-17T11:00:00Z",
};

export const outcomeOpen: OutcomeView = {
  actionId: ACTION_ID,
  openedAt: "2026-07-17T11:00:00Z",
  closesAt: "2026-07-24T11:00:00Z",
};

export const outcomeClosed: OutcomeView = {
  ...outcomeOpen,
  result: { result: "positive", confidence: "high", computedAt: "2026-07-24T11:05:00Z" },
};

/** A valid bulk confirmation bound to the previewed selection-set version. */
export const bulkValid: BulkApprovalConfirmResult = {
  selectionSetLineage: "30000000-0000-0000-0000-000000000003",
  boundVersion: 1,
  valid: true,
  executionPending: true,
};

/** A stale bulk confirmation: the bound version is no longer current. */
export const bulkStale: BulkApprovalConfirmResult = {
  selectionSetLineage: "30000000-0000-0000-0000-000000000003",
  boundVersion: 1,
  currentVersion: 2,
  valid: false,
  executionPending: false,
};

/** Complete readiness — drives an EXECUTABLE bulk candidate. */
export const readinessComplete: MarginReadiness = {
  variantId: VARIANT_ID,
  marketplaceAccountId: ACCOUNT_ID,
  state: "complete",
  missingComponents: [],
  staleComponents: [],
  computedAt: "2026-07-17T09:00:00Z",
};
