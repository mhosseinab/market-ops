import {
  type AppendMessage,
  type AssistantRuntime,
  type ExternalStoreAdapter,
  useExternalStoreRuntime,
} from "@assistant-ui/react";
import { type LocaleId, normalizeDigits } from "@market-ops/locale";
import { useCallback, useMemo, useRef, useState } from "react";
import { useAccount } from "../data/account";
import type { ChatContext, ChatContextKind } from "./context";
import { toThreadMessageLike } from "./convertMessage";
import type { ChatDockActions } from "./dockActions";
import { parseEnvelope } from "./envelope";
import { postChatTurn } from "./sse";
import type {
  ChatTurnRequest,
  ChatUnavailable,
  DockAssistantMessage,
  DockMessage,
  PickerOption,
} from "./types";

// The chat-dock runtime. It drives an assistant-ui external-store runtime over the
// gateway `/chat` SSE transport — NO `@assistant-ui/react-langgraph`, no direct
// LLM connection. The store is our own message array; the composer's `onNew` opens
// a read/Draft-only turn and streams frames in. The composer NEVER approves — the
// only mutation path is the structured ApprovalCard control (rendered as a card
// part), so free text changes nothing.
//
// Deterministic single context (CHAT-007): every turn carries the route-derived
// context binding so the gateway persists exactly the context the operator sees.
// A route-context change opens a NEW bound conversation rather than silently
// relabeling the current one; a picker selection binds that exact option through
// an explicit, versioned context transition BEFORE any card-producing continuation.
// The gateway is authoritative for the version — the client tracks it and defers
// to a server rejection (a stale binding fails the turn, never mislabels it).

function extractText(message: AppendMessage): string {
  return message.content
    .map((part) => (part.type === "text" ? part.text : ""))
    .join("")
    .trim();
}

let idCounter = 0;
function nextId(prefix: string): string {
  idCounter += 1;
  return `${prefix}-${idCounter}`;
}

// BoundContext is the conversation's current context as the client believes the
// gateway has it. version is the server-issued context version echoed on the
// conversation frame (or tracked optimistically until the gateway corrects it).
interface BoundContext {
  readonly kind: ChatContextKind;
  readonly entityId?: string;
  readonly version: number;
}

// The declared binding a turn should carry, plus whether it must start a new
// conversation (a route-context change is never a silent relabel).
interface TurnPlan {
  readonly binding: ChatTurnRequest["context"];
  readonly startNewConversation: boolean;
  readonly nextBound: BoundContext;
}

function sameEntity(
  a: BoundContext | undefined,
  kind: ChatContextKind,
  entityId?: string,
): boolean {
  return a !== undefined && a.kind === kind && a.entityId === entityId;
}

// BoundLocale is the conversation's current locale as the client believes the
// gateway has it. version is the server-issued locale version echoed on the
// conversation frame (or tracked optimistically until the gateway corrects it).
interface BoundLocale {
  readonly locale: LocaleId;
  readonly version: number;
}

// LocalePlan is the locale a turn carries on the wire plus the optimistic bound
// locale to commit if the gateway echoes none. Locale is DATA (LOC-001): the ACTIVE
// locale is always sent; a same-locale continuation is idempotent; a locale change
// is an EXPLICIT, versioned transition — never a silent relabel, never inferred.
interface LocalePlan {
  readonly fields: Pick<ChatTurnRequest, "locale" | "localeVersion" | "localeTransition">;
  readonly next: BoundLocale;
}

function planLocale(active: LocaleId, bound: BoundLocale | undefined): LocalePlan {
  if (bound === undefined) {
    // First turn on this conversation: bind the active locale at version 1. No
    // version claim, no transition flag.
    return { fields: { locale: active }, next: { locale: active, version: 1 } };
  }
  if (bound.locale === active) {
    // Same locale: idempotent continuation. Send the version the gateway echoed so a
    // stale binding is rejected rather than silently relabeled.
    return {
      fields: { locale: active, localeVersion: bound.version },
      next: bound,
    };
  }
  // The active locale changed mid-conversation: an EXPLICIT, versioned transition
  // (never a silent relabel). The gateway appends the next version.
  return {
    fields: { locale: active, localeVersion: bound.version, localeTransition: true },
    next: { locale: active, version: bound.version + 1 },
  };
}

export interface ChatDockRuntime {
  readonly runtime: AssistantRuntime;
  readonly messages: readonly DockMessage[];
  readonly isRunning: boolean;
  /** Set once a turn is refused (kill switch / provider outage). Read-only after. */
  readonly unavailable: ChatUnavailable | null;
  /** Actions exposed to nested structured cards (picker binding). */
  readonly actions: ChatDockActions;
  /**
   * The context the chip renders (CHAT-007, issue #115). Once a conversation
   * exists it is the gateway-ECHOED authoritative binding (the kind/entity the
   * gateway actually persisted), never a claimed one; before the first turn it is
   * the route-derived context.
   */
  readonly activeContext: ChatContext;
}

export function useChatDock(
  context: ChatContext,
  locale: LocaleId,
  activateConversationLocale: (locale: unknown) => Promise<void>,
): ChatDockRuntime {
  const { marketplaceAccountId } = useAccount();
  const [messages, setMessages] = useState<readonly DockMessage[]>([]);
  const [isRunning, setIsRunning] = useState(false);
  const [unavailable, setUnavailable] = useState<ChatUnavailable | null>(null);
  const conversationIdRef = useRef<string | undefined>(undefined);
  const boundContextRef = useRef<BoundContext | undefined>(undefined);
  // The gateway-echoed authoritative bound locale (LOC-001, issue #120), committed
  // at the `conversation` frame. undefined until the first turn commits.
  const boundLocaleRef = useRef<BoundLocale | undefined>(undefined);
  // The latest active locale, read at send time so every turn carries the ACTIVE
  // locale (LOC-001) without a stale closure — the same authoritative signal the
  // rest of the app renders from; never inferred from the message text.
  const localeRef = useRef<LocaleId>(locale);
  localeRef.current = locale;
  // The gateway-echoed authoritative context, committed at the `conversation`
  // frame. Kept as STATE (not just the ref) so the chip re-renders to the bound
  // context the gateway persisted — undefined until the first turn commits.
  const [committedContext, setCommittedContext] = useState<ChatContext | undefined>(undefined);
  // The latest route-derived context, read at send time so a turn always binds
  // the context the operator currently sees.
  const contextRef = useRef<ChatContext>(context);
  contextRef.current = context;

  const patchAssistant = useCallback((id: string, patch: Partial<DockAssistantMessage>) => {
    setMessages((prev) =>
      prev.map((m) => (m.id === id && m.role === "assistant" ? { ...m, ...patch } : m)),
    );
  }, []);

  // planRouteTurn decides the binding for an ordinary composer turn from the
  // current route context. A route-context change on an existing conversation
  // starts a NEW conversation (no silent relabel).
  const planRouteTurn = useCallback((): TurnPlan => {
    const routeCtx = contextRef.current;
    const bound = boundContextRef.current;
    const hasConversation = conversationIdRef.current !== undefined;
    const startNew = hasConversation && !sameEntity(bound, routeCtx.kind, routeCtx.entityId);
    const isFirstTurn = !hasConversation || startNew;
    const binding: ChatTurnRequest["context"] = {
      kind: routeCtx.kind,
      ...(routeCtx.entityId !== undefined ? { entityId: routeCtx.entityId } : {}),
      ...(isFirstTurn || bound === undefined ? {} : { contextVersion: bound.version }),
    };
    return {
      binding,
      startNewConversation: startNew,
      nextBound: {
        kind: routeCtx.kind,
        entityId: routeCtx.entityId,
        version: isFirstTurn ? 1 : (bound?.version ?? 1),
      },
    };
  }, []);

  // planPickerTurn binds a picker option to the CURRENT conversation via an
  // explicit, versioned context transition (CHAT-007). It is a product-entity
  // transition; a stale version is rejected by the gateway rather than relabeling.
  const planPickerTurn = useCallback((option: PickerOption): TurnPlan => {
    const bound = boundContextRef.current;
    const binding: ChatTurnRequest["context"] = {
      kind: "product",
      entityId: option.id,
      ...(bound !== undefined ? { contextVersion: bound.version, transition: true } : {}),
    };
    return {
      binding,
      startNewConversation: false,
      nextBound: {
        kind: "product",
        entityId: option.id,
        version: bound !== undefined ? bound.version + 1 : 1,
      },
    };
  }, []);

  const runTurn = useCallback(
    async (text: string, plan: TurnPlan) => {
      const userId = nextId("user");
      const assistantId = nextId("assistant");
      setMessages((prev) => [
        ...prev,
        { id: userId, role: "user", text },
        { id: assistantId, role: "assistant", status: "streaming", text: "", cards: [] },
      ]);
      setIsRunning(true);

      if (plan.startNewConversation) {
        // A route-context change opens a fresh bound conversation; the previous
        // conversation keeps its own context (never relabeled). Drop the committed
        // chip context so it falls back to the new route until the fresh turn's
        // `conversation` frame commits the gateway-authoritative binding. The bound
        // locale is per-conversation, so it resets too — the fresh turn rebinds the
        // active locale at version 1.
        conversationIdRef.current = undefined;
        boundContextRef.current = undefined;
        boundLocaleRef.current = undefined;
        setCommittedContext(undefined);
      }

      // Every turn carries the ACTIVE locale (LOC-001). A same-locale continuation is
      // idempotent; a locale switch mid-conversation is an explicit, versioned
      // transition — never a silent relabel. The gateway is authoritative.
      const localePlan = planLocale(localeRef.current, boundLocaleRef.current);

      try {
        const outcome = await postChatTurn({
          message: text,
          ...(conversationIdRef.current ? { conversationId: conversationIdRef.current } : {}),
          marketplaceAccountId,
          ...(plan.binding ? { context: plan.binding } : {}),
          ...localePlan.fields,
        });

        if (outcome.kind === "unavailable") {
          setUnavailable(outcome.unavailable);
          patchAssistant(assistantId, { status: "failed" });
          return;
        }

        let streamedText = "";
        for await (const event of outcome.events) {
          switch (event.kind) {
            case "conversation": {
              if (event.conversationId) conversationIdRef.current = event.conversationId;
              // Commit the AUTHORITATIVE binding the gateway echoes (kind/entity/
              // version it actually persisted), never the optimistically-planned one:
              // the chip renders what the gateway bound, so a picker transition to
              // product/<id> shows the bound kind, not the route kind. Only when the
              // gateway omits the echo (no store wired) fall back to the plan.
              const committed: BoundContext =
                event.contextKind !== undefined
                  ? {
                      kind: event.contextKind,
                      ...(event.contextEntityId !== undefined
                        ? { entityId: event.contextEntityId }
                        : {}),
                      version: event.contextVersion ?? plan.nextBound.version,
                    }
                  : plan.nextBound;
              boundContextRef.current = committed;
              setCommittedContext({
                kind: committed.kind,
                ...(committed.entityId !== undefined ? { entityId: committed.entityId } : {}),
              });
              // Commit the AUTHORITATIVE bound locale the gateway echoes (tag +
              // version it actually persisted), never the optimistically-planned one;
              // only when the gateway omits the echo (no store wired) fall back to the
              // plan. The next turn sends this version back so a locale change is an
              // explicit, versioned transition.
              const committedLocale: BoundLocale =
                event.localeTag !== undefined
                  ? {
                      locale: event.localeTag,
                      version: event.localeVersion ?? localePlan.next.version,
                    }
                  : localePlan.next;
              // Catalog activation is an awaited stream barrier. A token or
              // terminal frame cannot become renderable until the chat-scoped
              // catalog for the authoritative locale is prepared and committed.
              await activateConversationLocale(committedLocale.locale);
              boundLocaleRef.current = committedLocale;
              break;
            }
            case "token":
              streamedText += event.token ?? "";
              patchAssistant(assistantId, { text: streamedText });
              break;
            case "final": {
              const parsed = parseEnvelope(event.envelope);
              patchAssistant(assistantId, {
                status: "complete",
                envelope: parsed.envelope,
                cards: parsed.cards,
              });
              break;
            }
            case "failure":
              patchAssistant(assistantId, {
                status: "failed",
                ...(event.failure ? { failure: event.failure } : {}),
              });
              break;
          }
        }
        // The generator only returns normally AFTER yielding one validated
        // terminal frame (which set complete/failed above). A truncated, malformed,
        // or terminal-less stream throws a typed ChatStreamError instead — never
        // silently completing the turn (issue #116).
      } catch {
        // Transport seam failure (including a gateway context rejection): keep any
        // partial streamed text but mark the turn failed and flag it as a transport
        // failure so the dock renders the incomplete notice — no completed
        // envelope/cards are ever attached.
        patchAssistant(assistantId, { status: "failed", transportFailed: true });
      } finally {
        setIsRunning(false);
      }
    },
    [marketplaceAccountId, patchAssistant, activateConversationLocale],
  );

  const onNew = useCallback(
    async (message: AppendMessage) => {
      // Digit-family normalization at the INPUT boundary (LOC-007, CHAT-081):
      // Persian and Latin digits produce an identical outgoing turn.
      const text = normalizeDigits(extractText(message));
      if (text.length === 0) return;
      await runTurn(text, planRouteTurn());
    },
    [runTurn, planRouteTurn],
  );

  const bindPickerOption = useCallback(
    (option: PickerOption) => {
      // The bound turn's message is the option's grounded display label; the
      // structured context binding does the actual, versioned binding.
      const text = normalizeDigits(option.label).trim();
      void runTurn(text.length > 0 ? text : option.id, planPickerTurn(option));
    },
    [runTurn, planPickerTurn],
  );

  const adapter = useMemo<ExternalStoreAdapter<DockMessage>>(
    () => ({
      messages,
      isRunning,
      // Kill switch / provider outage: the composer is disabled and the existing
      // conversation becomes read-only (§16 chat-disabled-mid-conversation).
      isDisabled: unavailable !== null,
      convertMessage: toThreadMessageLike,
      onNew,
    }),
    [messages, isRunning, unavailable, onNew],
  );

  const runtime = useExternalStoreRuntime(adapter);
  const actions = useMemo<ChatDockActions>(() => ({ bindPickerOption }), [bindPickerOption]);

  // The chip renders the gateway-authoritative committed context once a
  // conversation exists, falling back to the route-derived context only before the
  // first turn (issue #115).
  const activeContext = committedContext ?? context;

  return { runtime, messages, isRunning, unavailable, actions, activeContext };
}
