import type { components } from "@market-ops/gen-ts";
import type { QualityState } from "../components/badges";

// Chat-dock view-models.
//
// CONTRACT GAP (carry-forward for api_data_contracts): the gateway `/chat` SSE
// `final` frame carries `envelope` as `additionalProperties: true` — its internal
// shape (the seven CHAT-004 statement kinds, evidence refs, structured cards) is
// owned + validated inside the LLM plane and "lands with the response contract
// step", so it is NOT yet in the merged gateway contract. Until it is, the dock
// parses the envelope DEFENSIVELY into the tolerant view-models below and renders
// an explicit unavailable/degraded state for anything absent (never fabricated).
// These types are the FE's assumed shape, documented so the eventual owned
// contract can be matched against them.

export type ChatStreamEvent = components["schemas"]["ChatStreamEvent"];
export type ChatTurnRequest = components["schemas"]["ChatTurnRequest"];
export type ChatUnavailable = components["schemas"]["ChatUnavailable"];
export type ChatUnavailableReason = components["schemas"]["ChatUnavailableReason"];
export type ChatFailure = components["schemas"]["ChatFailure"];
export type DailyBriefing = components["schemas"]["DailyBriefing"];
export type BriefingEvent = components["schemas"]["BriefingEvent"];
export type LatestBriefingRead = components["schemas"]["LatestBriefingRead"];

// The seven CHAT-004 statement kinds a grounded operational response separates.
export type StatementKind =
  | "observed"
  | "dk"
  | "config"
  | "calculation"
  | "inference"
  | "missing"
  | "recommendation";

export const STATEMENT_KINDS: readonly StatementKind[] = [
  "observed",
  "dk",
  "config",
  "calculation",
  "inference",
  "missing",
  "recommendation",
];

/** One evidence reference accompanying an operational claim (CHAT-005). */
export interface EvidenceRef {
  /** Observation / evidence id — an LTR technical identifier (rendered isolated). */
  readonly ref: string;
  readonly quality?: QualityState;
  /** ISO capture instant; rendered as an as-of time. */
  readonly capturedAt?: string;
}

/** A visually-distinct statement section; its lines are grounded response DATA. */
export interface StatementSection {
  readonly kind: StatementKind;
  readonly lines: readonly string[];
}

/** A typed deep link back into the matching structured screen (CHAT-006). */
export interface DeepLink {
  readonly to: string;
  readonly search?: {
    variantId?: string;
    eventId?: string;
    cardId?: string;
    actionId?: string;
  };
}

/** An inline result table; capped at 20 rows in the view (CHAT-023). */
export interface InlineTable {
  readonly headers: readonly string[];
  readonly rows: readonly (readonly string[])[];
  /** Total rows the query matched (may exceed `rows`). */
  readonly totalRows: number;
  readonly deepLink?: DeepLink;
}

export interface ChatEnvelope {
  readonly sections: readonly StatementSection[];
  readonly evidence: readonly EvidenceRef[];
  readonly table?: InlineTable;
  readonly deepLink?: DeepLink;
}

/** One option in an ambiguity picker (CHAT-007). Selecting it never approves. */
export interface PickerOption {
  readonly id: string;
  /** Display title — grounded DATA (e.g. a variant title). */
  readonly label: string;
  /** Optional LTR technical id (SKU) rendered isolated. */
  readonly sku?: string;
  readonly deepLink?: DeepLink;
}

/** A Level-2 reversible-config before/after proposal (CHAT-061). */
export interface Level2Proposal {
  /** Grounded display strings for the change (LLM-plane localized DATA). */
  readonly setting?: string;
  readonly before?: string;
  readonly after?: string;
  readonly scope?: string;
  readonly consequence?: string;
  readonly expiresAt?: string;
  /** Where the reversible write is committed (screens-only — CHAT-061 audit). */
  readonly deepLink?: DeepLink;
}

// Structured cards rendered as OUR components mounted as custom message parts.
// assistant-ui NEVER owns any of these — an approval card is confirmed only by the
// reused S27 ApprovalCard control hitting the same gateway confirm endpoint.
export type DockCard =
  | { readonly kind: "picker"; readonly options: readonly PickerOption[] }
  | { readonly kind: "approval"; readonly cardId: string }
  | { readonly kind: "level2"; readonly proposal: Level2Proposal };

export interface DockUserMessage {
  readonly id: string;
  readonly role: "user";
  readonly text: string;
}

export interface DockAssistantMessage {
  readonly id: string;
  readonly role: "assistant";
  readonly status: "streaming" | "complete" | "failed";
  readonly text: string;
  readonly envelope?: ChatEnvelope;
  readonly cards: readonly DockCard[];
  readonly failure?: ChatFailure;
  /**
   * Set when the turn failed at the TRANSPORT seam (truncation, malformed frame,
   * invalid terminal payload, or EOF without a terminal — issue #116) rather than
   * via a structured `failure` frame. It renders the client-side incomplete
   * notice so partial/ungrounded output is never shown as a completed answer.
   */
  readonly transportFailed?: boolean;
}

export type DockMessage = DockUserMessage | DockAssistantMessage;
