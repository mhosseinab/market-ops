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

/**
 * A typed transport failure on the chat stream (issue #116). A turn is complete
 * ONLY after one validated terminal frame — so transport truncation, a malformed
 * frame, an unknown discriminator, an invalid terminal payload, or EOF without a
 * `final`/`failure` frame throws this instead of silently completing the turn.
 * The dock consumer catches it and renders an unmistakable incomplete state,
 * never a completed answer.
 */
export class ChatStreamError extends Error {
  readonly code: ChatStreamErrorCode;
  constructor(code: ChatStreamErrorCode, message: string) {
    super(message);
    this.name = "ChatStreamError";
    this.code = code;
  }
}

export type ChatStreamErrorCode =
  | "malformed_frame"
  | "unknown_frame"
  | "invalid_final"
  | "truncated";

// Runtime mirror of the generated ChatStreamEvent discriminator. `satisfies`
// couples it to the gen/ts schema at compile time: if the contract adds/renames a
// frame kind, this list stops type-checking until it is updated in lockstep.
const FRAME_KINDS = [
  "conversation",
  "token",
  "final",
  "failure",
] as const satisfies readonly ChatStreamEvent["kind"][];

const TERMINAL_KINDS: ReadonlySet<ChatStreamEvent["kind"]> = new Set(["final", "failure"]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Decode one SSE frame's concatenated `data:` payload. Returns `null` ONLY for a
 * genuine non-event (heartbeat / comment / empty / `[DONE]`). Anything present but
 * unparseable, mis-discriminated, or (for `final`) missing a structured envelope
 * is a transport failure and THROWS — never a dropped/coerced frame.
 */
function decodeFrame(frame: string): ChatStreamEvent | null {
  const dataLines = frame
    .split("\n")
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice("data:".length).replace(/^ /, ""));
  if (dataLines.length === 0) return null;
  const payload = dataLines.join("\n").trim();
  if (payload === "" || payload === "[DONE]") return null;

  let parsed: unknown;
  try {
    parsed = JSON.parse(payload);
  } catch {
    throw new ChatStreamError("malformed_frame", "chat stream frame is not valid JSON");
  }
  if (!isRecord(parsed) || !FRAME_KINDS.includes(parsed.kind as ChatStreamEvent["kind"])) {
    throw new ChatStreamError("unknown_frame", "chat stream frame has no known discriminator");
  }
  // An invalid terminal payload must fail closed, not become an empty completed
  // answer: a `final` frame is authoritative only if it carries a structured
  // envelope object for the view-model to parse.
  if (parsed.kind === "final" && !isRecord(parsed.envelope)) {
    throw new ChatStreamError("invalid_final", "chat `final` frame carries no envelope object");
  }
  return parsed as ChatStreamEvent;
}

async function* parseSseStream(body: ReadableStream<Uint8Array>): AsyncGenerator<ChatStreamEvent> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let terminalSeen = false;
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
        if (event) {
          yield event;
          if (TERMINAL_KINDS.has(event.kind)) {
            // Exactly one terminal frame decides the turn: stop here so a second
            // terminal (or any trailing frame) can never replace the first.
            terminalSeen = true;
            return;
          }
        }
        sep = buffer.indexOf("\n\n");
      }
    }
    const tail = decodeFrame(buffer);
    if (tail) {
      yield tail;
      if (TERMINAL_KINDS.has(tail.kind)) terminalSeen = true;
    }
    if (!terminalSeen) {
      // EOF without a validated terminal frame — the stream was truncated. Fail
      // the turn rather than promote partial text to a completed answer.
      throw new ChatStreamError("truncated", "chat stream ended before a terminal frame");
    }
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
