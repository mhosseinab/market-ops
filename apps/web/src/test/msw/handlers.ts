import { HttpResponse, http } from "msw";
import type { ChatStreamEvent } from "../../chat/types";
import {
  approvalCardAwaiting,
  bulkValid,
  confirmApproved,
  connectorUnknown,
  dailyBriefing,
  execAccepted,
  marketEvent,
  needsReviewQueue,
  offer,
  outcomeClosed,
  previewWithDuplicate,
  readinessMissing,
  sessionOwner,
  target,
} from "./fixtures";

/** Build a `text/event-stream` response from a list of ChatStreamEvents. */
export function sseResponse(events: readonly ChatStreamEvent[]): Response {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const event of events) {
        controller.enqueue(encoder.encode(`data: ${JSON.stringify(event)}\n\n`));
      }
      controller.close();
    },
  });
  return new HttpResponse(stream, {
    headers: { "content-type": "text/event-stream" },
  });
}

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

  // Today defaults to EMPTY (deterministic no-action) so the app-shell snapshot
  // stays stable; Today's own tests override with a populated feed.
  http.get(`${B}/today`, () => HttpResponse.json({ items: [] })),
  http.get(`${B}/events`, () => HttpResponse.json({ items: [marketEvent] })),
  http.get(`${B}/event`, () => HttpResponse.json(marketEvent)),
  http.post(`${B}/events/relevance`, () =>
    HttpResponse.json({
      id: "relevance-1",
      eventId: marketEvent.id,
      relevance: "muted",
      createdAt: "2026-07-17T10:00:00Z",
    }),
  ),

  http.get(`${B}/approvals/card`, () => HttpResponse.json(approvalCardAwaiting)),
  http.post(`${B}/approvals/confirm`, () => HttpResponse.json(confirmApproved)),

  // ── S28 defaults ──────────────────────────────────────────────────────────
  http.get(`${B}/auth/me`, () => HttpResponse.json(sessionOwner)),
  http.get(`${B}/actions/execution`, () => HttpResponse.json(execAccepted)),
  http.get(`${B}/outcomes`, () => HttpResponse.json(outcomeClosed)),
  http.post(`${B}/actions/retry`, () =>
    HttpResponse.json({ actionId: execAccepted.actionId, eligible: true, state: "failed" }),
  ),
  http.post(`${B}/approvals/bulk/confirm`, () => HttpResponse.json(bulkValid)),

  // ── S29: chat dock ──────────────────────────────────────────────────────────
  http.get(`${B}/briefing`, () => HttpResponse.json(dailyBriefing)),
  // Default chat turn: a conversation id + a token + a final envelope frame. The
  // free-text-never-approves invariant holds regardless — /chat carries no control.
  http.post(`${B}/chat`, () =>
    sseResponse([
      { kind: "conversation", conversationId: "99999999-9999-9999-9999-999999999999" },
      { kind: "token", token: "…" },
      { kind: "final", envelope: { sections: [], evidence: [] } },
    ]),
  ),

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
