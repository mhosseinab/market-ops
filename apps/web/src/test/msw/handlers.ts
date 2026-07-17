import { HttpResponse, http } from "msw";
import {
  connectorUnknown,
  needsReviewQueue,
  offer,
  previewWithDuplicate,
  readinessMissing,
  target,
} from "./fixtures";

// Default MSW handlers mirroring the core contract. Tests pin an absolute gateway
// base (see vite.config test.env), so handlers use that exact origin/prefix —
// MSW's `*` wildcard does not span path separators, so a fixed base is the
// reliable match. Query strings are ignored for matching. Individual tests
// override a handler with `server.use(...)` to exercise a specific scenario.

const B = "http://localhost/api";

export const handlers = [
  http.get(`${B}/connector/status`, () => HttpResponse.json(connectorUnknown)),
  http.post(`${B}/connector/refresh`, () => HttpResponse.json(connectorUnknown)),
  http.post(`${B}/connector/disconnect`, () => HttpResponse.json(connectorUnknown)),
  http.post(`${B}/connector/connect`, () => HttpResponse.json(connectorUnknown)),

  http.get(`${B}/observation/targets`, () => HttpResponse.json({ items: [target] })),
  http.get(`${B}/observation/observed-offers`, () => HttpResponse.json({ items: [offer] })),
  http.get(`${B}/observation/observations`, () => HttpResponse.json({ items: [] })),

  http.get(`${B}/cost/readiness`, () => HttpResponse.json(readinessMissing)),
  http.get(`${B}/cost/profiles`, () => HttpResponse.json({ items: [] })),
  http.post(`${B}/cost/import/preview`, () => HttpResponse.json(previewWithDuplicate)),
  http.post(`${B}/cost/import/commit`, () =>
    HttpResponse.json({
      batchId: previewWithDuplicate.batchId,
      status: "committed",
      committedRows: 1,
      affectedVariantIds: [target.variantId],
    }),
  ),

  http.get(`${B}/identity/needs-review`, () => HttpResponse.json(needsReviewQueue)),
  http.post(`${B}/identity/confirm`, () =>
    HttpResponse.json({
      id: needsReviewQueue.items[0]?.identityId,
      marketplaceAccountId: target.marketplaceAccountId,
      variantId: target.variantId,
      nativeVariantId: target.nativeVariantId,
      nativeProductId: target.nativeProductId,
      state: "confirmed",
      active: true,
      candidateSource: "exact_native_id",
      version: 2,
    }),
  ),
  http.post(`${B}/identity/reject`, () => HttpResponse.json({ ok: true })),
  http.post(`${B}/identity/defer`, () => HttpResponse.json({ ok: true })),

  http.post(`${B}/cost/value`, () =>
    HttpResponse.json({
      id: "77777777-7777-7777-7777-777777777777",
      marketplaceAccountId: target.marketplaceAccountId,
      variantId: target.variantId,
      component: "cogs",
      version: 1,
      amount: { mantissa: 8900000, currency: "IRR", exponent: 0 },
      effectiveFrom: "2026-07-17T09:00:00Z",
      source: "single_value",
    }),
  ),
];

/** The absolute gateway base tests pin; exported for per-test `server.use(...)`. */
export const BASE = B;
