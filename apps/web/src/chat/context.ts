import type { components } from "@market-ops/gen-ts";
import type { MessageKey } from "@market-ops/locale";

// Chat context model (PRD §8.1, CHAT-001/007). Exactly one context is active per
// conversation and shown as a chip. Contextual entry from a product/event/
// recommendation/action deep link BINDS that context — derived deterministically
// from the current route + search here, never guessed. Ambiguity that could lead
// to a card is resolved by a structured picker (rendered as a card), not by
// silently carrying a context forward.

// The context kind is a CONTRACT enum: it aliases the generated
// ConversationContextKind so the gateway OpenAPI source is its single source of
// truth (issue #115). A kind added/removed there stops this type-checking until
// the FE is updated in lockstep — no hand-maintained divergent list.
export type ChatContextKind = components["schemas"]["ConversationContextKind"];

export interface ChatContext {
  readonly kind: ChatContextKind;
  readonly entityId?: string;
}

export const CONTEXT_LABEL_KEY: Record<ChatContextKind, MessageKey> = {
  global: "chat.context.global",
  product: "chat.context.product",
  event: "chat.context.event",
  recommendation: "chat.context.recommendation",
  bulk: "chat.context.bulk",
  action: "chat.context.action",
  settings: "chat.context.settings",
  operations: "chat.context.operations",
};

interface RouteSearch {
  readonly variantId?: string;
  readonly eventId?: string;
  readonly cardId?: string;
  readonly actionId?: string;
}

/** Derive the single active chat context from the current route + deep-link search. */
export function deriveChatContext(pathname: string, search: RouteSearch): ChatContext {
  switch (pathname) {
    case "/product":
    case "/cost":
      return search.variantId
        ? { kind: "product", entityId: search.variantId }
        : { kind: "product" };
    case "/event":
      return search.eventId ? { kind: "event", entityId: search.eventId } : { kind: "event" };
    case "/recommendation":
      return search.cardId
        ? { kind: "recommendation", entityId: search.cardId }
        : { kind: "recommendation" };
    case "/bulk":
      return { kind: "bulk" };
    case "/actions":
      return search.actionId ? { kind: "action", entityId: search.actionId } : { kind: "action" };
    case "/settings":
      return { kind: "settings" };
    case "/operations":
      return { kind: "operations" };
    default:
      return { kind: "global" };
  }
}
