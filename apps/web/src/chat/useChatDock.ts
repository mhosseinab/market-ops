import {
  type AppendMessage,
  type AssistantRuntime,
  type ExternalStoreAdapter,
  useExternalStoreRuntime,
} from "@assistant-ui/react";
import { normalizeDigits } from "@market-ops/locale";
import { useCallback, useMemo, useRef, useState } from "react";
import { useAccount } from "../data/account";
import { toThreadMessageLike } from "./convertMessage";
import { parseEnvelope } from "./envelope";
import { postChatTurn } from "./sse";
import type { ChatUnavailable, DockAssistantMessage, DockMessage } from "./types";

// The chat-dock runtime. It drives an assistant-ui external-store runtime over the
// gateway `/chat` SSE transport — NO `@assistant-ui/react-langgraph`, no direct
// LLM connection. The store is our own message array; the composer's `onNew` opens
// a read/Draft-only turn and streams frames in. The composer NEVER approves — the
// only mutation path is the structured ApprovalCard control (rendered as a card
// part), so free text changes nothing.

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

export interface ChatDockRuntime {
  readonly runtime: AssistantRuntime;
  readonly messages: readonly DockMessage[];
  readonly isRunning: boolean;
  /** Set once a turn is refused (kill switch / provider outage). Read-only after. */
  readonly unavailable: ChatUnavailable | null;
}

export function useChatDock(): ChatDockRuntime {
  const { marketplaceAccountId } = useAccount();
  const [messages, setMessages] = useState<readonly DockMessage[]>([]);
  const [isRunning, setIsRunning] = useState(false);
  const [unavailable, setUnavailable] = useState<ChatUnavailable | null>(null);
  const conversationIdRef = useRef<string | undefined>(undefined);

  const patchAssistant = useCallback((id: string, patch: Partial<DockAssistantMessage>) => {
    setMessages((prev) =>
      prev.map((m) => (m.id === id && m.role === "assistant" ? { ...m, ...patch } : m)),
    );
  }, []);

  const onNew = useCallback(
    async (message: AppendMessage) => {
      // Digit-family normalization at the INPUT boundary (LOC-007, CHAT-081):
      // Persian and Latin digits produce an identical outgoing turn.
      const text = normalizeDigits(extractText(message));
      if (text.length === 0) return;

      const userId = nextId("user");
      const assistantId = nextId("assistant");
      setMessages((prev) => [
        ...prev,
        { id: userId, role: "user", text },
        { id: assistantId, role: "assistant", status: "streaming", text: "", cards: [] },
      ]);
      setIsRunning(true);

      try {
        const outcome = await postChatTurn({
          message: text,
          ...(conversationIdRef.current ? { conversationId: conversationIdRef.current } : {}),
          marketplaceAccountId,
        });

        if (outcome.kind === "unavailable") {
          setUnavailable(outcome.unavailable);
          patchAssistant(assistantId, { status: "failed" });
          return;
        }

        let streamedText = "";
        for await (const event of outcome.events) {
          switch (event.kind) {
            case "conversation":
              if (event.conversationId) conversationIdRef.current = event.conversationId;
              break;
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
        // Transport seam failure: keep any partial streamed text but mark the turn
        // failed and flag it as a transport failure so the dock renders the
        // incomplete notice — no completed envelope/cards are ever attached.
        patchAssistant(assistantId, { status: "failed", transportFailed: true });
      } finally {
        setIsRunning(false);
      }
    },
    [marketplaceAccountId, patchAssistant],
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

  return { runtime, messages, isRunning, unavailable };
}
