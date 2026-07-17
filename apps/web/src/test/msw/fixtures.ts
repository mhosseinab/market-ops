import type {
  ConnectorStatus,
  CostImportPreview,
  MarginReadiness,
  NeedsReviewQueue,
  ObservationTarget,
  ObservedOffer,
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
