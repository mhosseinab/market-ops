import type { MessageKey } from "@market-ops/locale";
import { reportUnsupportedValue } from "../app/unsupportedTelemetry";

// Closed web-edge maps (LOC-002, issue #121). The chat `/chat` failure `code` and
// the briefing `event.eventType` cross the service boundary AS DATA (stable
// machine identifiers). The web edge maps each to a CLOSED catalog `MessageKey`
// so the surface renders ONLY localized catalog copy — never a raw machine value
// or a server diagnostic string. A value OUTSIDE the closed set renders a
// catalog-backed localized unavailable label AND emits PII-free telemetry so the
// drift is observable (a new server code/type is a detected drift, never silent
// user-visible copy).
//
// Because the map VALUES are the closed `MessageKey` union, a mapped key that
// lacks en/fa-IR labels fails the catalog-parity gate (catalog.test.ts) and
// pseudo-loc — so a new supported value cannot ship without its localized copy.

/**
 * §12.4 structured-failure codes the LLM plane emits on the `failure` frame
 * (services/llm graph `_HARD_BOUNDS`, the transient fail-closed state, and the
 * deterministic turn-context resolver). The set is CLOSED at the edge; anything
 * else is treated as an unknown/unsupported code.
 *
 * The plane declares its emittable set in `llm.envelope.models`
 * (EMITTABLE_FAILURE_CODES); catalogMaps.test.ts asserts this map covers it, and
 * services/llm/tests/test_failure_code_contract.py asserts the same from the
 * producer side — so a new code cannot reach the surface unmapped (which would
 * fire the drift alarm below on a normal, correct, fail-closed path).
 */
export const FAILURE_CODE_KEY: Record<string, MessageKey> = {
  TURN_RECURSION_LIMIT: "chat.failure.recursionLimit",
  TOOL_CALL_LIMIT: "chat.failure.toolCallLimit",
  TOOL_TIMEOUT: "chat.failure.toolTimeout",
  TOKEN_CEILING: "chat.failure.tokenCeiling",
  MODEL_PROVIDER_ERROR: "chat.failure.providerError",
  MODEL_TRANSIENT_FAILURE: "chat.failure.transient",
  CONTEXT_SCOPE_MISSING: "chat.failure.contextScopeMissing",
  CONTEXT_MALFORMED: "chat.failure.contextMalformed",
  CONTEXT_UNAVAILABLE: "chat.failure.contextUnavailable",
  CONTEXT_PICKER_UNAVAILABLE: "chat.failure.contextPickerUnavailable",
  CONTEXT_NOT_FOUND: "chat.failure.contextNotFound",
  TURN_INCOMPLETE: "chat.failure.turnIncomplete",
  INTENT_UNCLASSIFIED: "chat.failure.intentUnclassified",
};

/** Localized body shown for an unknown/unsupported failure code (never the raw value). */
export const FAILURE_UNKNOWN_KEY: MessageKey = "chat.failure.unsupported";

/**
 * The five closed P0 market-event types (PRD §7.4 EVT-001). `BriefingEvent.eventType`
 * is an unconstrained string in the gateway contract, so this edge map is the
 * closed set — matched to the SAME catalog keys the Today/EventDetail screens use
 * (glossary consistency). An unlisted value is an observable drift, not raw copy.
 */
export const BRIEFING_EVENT_TYPE_KEY: Record<string, MessageKey> = {
  winning_state: "eventType.buyBox",
  competitor_price: "eventType.competitorOffer",
  seller_count: "eventType.sellerCount",
  suppression_boundary: "eventType.priceBoundary",
  contribution_floor: "eventType.marginFloor",
};

/** Localized label shown for an unknown/unsupported event type (never the raw value). */
export const EVENT_TYPE_UNKNOWN_KEY: MessageKey = "chat.briefing.eventTypeUnknown";

/**
 * Resolve a chat failure `code` to its localized-copy `MessageKey`. An unmapped
 * code returns the unavailable label and emits PII-free drift telemetry — the raw
 * `code` is a stable technical identifier, never rendered as copy.
 */
export function failureMessageKey(code: string): MessageKey {
  const key = FAILURE_CODE_KEY[code];
  if (key) return key;
  reportUnsupportedValue({ kind: "chat_failure_code", value: code });
  return FAILURE_UNKNOWN_KEY;
}

/**
 * Resolve a briefing `eventType` to its localized-label `MessageKey`. An unmapped
 * type returns the unavailable label and emits PII-free drift telemetry — the raw
 * `eventType` is a stable technical identifier, never rendered as copy.
 */
export function briefingEventTypeKey(eventType: string): MessageKey {
  const key = BRIEFING_EVENT_TYPE_KEY[eventType];
  if (key) return key;
  reportUnsupportedValue({ kind: "briefing_event_type", value: eventType });
  return EVENT_TYPE_UNKNOWN_KEY;
}
