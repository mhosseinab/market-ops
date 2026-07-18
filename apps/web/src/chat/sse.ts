import { GATEWAY_BASE_URL } from "../app/query";
import type { ChatStreamEvent, ChatTurnRequest, ChatUnavailable } from "./types";

// The chat SSE transport. It talks ONLY to the gateway `/chat` endpoint over the
// same cookie-authenticated origin the typed gen/ts client uses (PRD §19.3: SSE,
// no WebSocket). The browser never connects to, nor learns of, the LLM service —
// the gateway proxies the turn. openapi-fetch cannot decode a byte stream, so this
// uses `fetch` directly against the shared base, typed by the gen/ts schema
// (ChatTurnRequest / ChatStreamEvent / ChatUnavailable). The turn is read/Draft
// only; no frame ever carries an approval control.

export type ChatTurnOutcome =
  | { readonly kind: "unavailable"; readonly unavailable: ChatUnavailable }
  | { readonly kind: "stream"; readonly events: AsyncGenerator<ChatStreamEvent> };

/** Decode one SSE frame's concatenated `data:` payload into a ChatStreamEvent. */
function decodeFrame(frame: string): ChatStreamEvent | null {
  const dataLines = frame
    .split("\n")
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice("data:".length).replace(/^ /, ""));
  if (dataLines.length === 0) return null;
  const payload = dataLines.join("\n").trim();
  if (payload === "" || payload === "[DONE]") return null;
  try {
    return JSON.parse(payload) as ChatStreamEvent;
  } catch {
    // A malformed frame is dropped, never coerced — the turn simply carries no
    // event for it. A missing `final` frame surfaces as a still-streaming message.
    return null;
  }
}

async function* parseSseStream(body: ReadableStream<Uint8Array>): AsyncGenerator<ChatStreamEvent> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true }).replace(/\r/g, "");
      let sep = buffer.indexOf("\n\n");
      while (sep !== -1) {
        const frame = buffer.slice(0, sep);
        buffer = buffer.slice(sep + 2);
        const event = decodeFrame(frame);
        if (event) yield event;
        sep = buffer.indexOf("\n\n");
      }
    }
    const tail = decodeFrame(buffer);
    if (tail) yield tail;
  } finally {
    reader.releaseLock();
  }
}

/**
 * Open (or continue) a chat turn. A 503 returns the structured ChatUnavailable
 * state (kill switch or provider outage) — NOT an error the way a 5xx is; screens
 * stay fully functional (CHAT-009). Otherwise the SSE frames stream back.
 */
export async function postChatTurn(
  request: ChatTurnRequest,
  signal?: AbortSignal,
): Promise<ChatTurnOutcome> {
  const response = await globalThis.fetch(`${GATEWAY_BASE_URL}/chat`, {
    method: "POST",
    headers: { "content-type": "application/json", accept: "text/event-stream" },
    body: JSON.stringify(request),
    credentials: "include",
    ...(signal ? { signal } : {}),
  });

  if (response.status === 503) {
    const unavailable = (await response.json()) as ChatUnavailable;
    return { kind: "unavailable", unavailable };
  }
  if (!response.ok || !response.body) {
    throw new Error(`chat_turn_failed_${response.status}`);
  }
  return { kind: "stream", events: parseSseStream(response.body) };
}
