import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { BASE, sseResponse } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { ChatStreamError, postChatTurn } from "./sse";
import type { ChatStreamEvent } from "./types";

async function collect(gen: AsyncGenerator<ChatStreamEvent>): Promise<ChatStreamEvent[]> {
  const out: ChatStreamEvent[] = [];
  for await (const e of gen) out.push(e);
  return out;
}

/** A raw SSE byte stream from pre-encoded chunk strings (no JSON wrapping). */
function rawSse(chunks: readonly string[]): Response {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const c of chunks) controller.enqueue(encoder.encode(c));
      controller.close();
    },
  });
  return new HttpResponse(stream, { headers: { "content-type": "text/event-stream" } });
}

async function streamOf(chunks: readonly string[]): Promise<AsyncGenerator<ChatStreamEvent>> {
  server.use(http.post(`${BASE}/chat`, () => rawSse(chunks)));
  const outcome = await postChatTurn({ message: "hi", marketplaceAccountId: "acct" });
  if (outcome.kind !== "stream") throw new Error("expected stream");
  return outcome.events;
}

const VALID_FINAL = 'data: {"kind":"final","envelope":{"sections":[],"evidence":[]}}\n\n';

describe("postChatTurn / SSE transport", () => {
  it("decodes conversation + token + final frames from the byte stream", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse([
          { kind: "conversation", conversationId: "conv-1" },
          { kind: "token", token: "hel" },
          { kind: "token", token: "lo" },
          {
            kind: "final",
            envelope: { sections: [{ kind: "observed", lines: ["x"] }], evidence: [] },
          },
        ]),
      ),
    );
    const outcome = await postChatTurn({ message: "hi", marketplaceAccountId: "acct" });
    expect(outcome.kind).toBe("stream");
    if (outcome.kind !== "stream") return;
    const events = await collect(outcome.events);
    expect(events.map((e) => e.kind)).toEqual(["conversation", "token", "token", "final"]);
    expect(events[1]?.token).toBe("hel");
  });

  it("splits frames across chunk boundaries, including a split multibyte terminal", async () => {
    // A token frame arrives in two chunks, then a valid terminal `final`. The
    // terminal invariant holds even when frames straddle chunk boundaries.
    const events = await collect(
      await streamOf(['data: {"kind":"to', 'ken","token":"AB"}\n\n', VALID_FINAL]),
    );
    expect(events.map((e) => e.kind)).toEqual(["token", "final"]);
    expect(events[0]?.token).toBe("AB");
  });

  it("skips heartbeat / empty / [DONE] frames but still requires a terminal", async () => {
    const events = await collect(
      await streamOf([
        ": keep-alive\n\n",
        "data: [DONE]\n\n",
        'data: {"kind":"token","token":"hi"}\n\n',
        VALID_FINAL,
      ]),
    );
    expect(events.map((e) => e.kind)).toEqual(["token", "final"]);
  });

  // ── Terminal invariant (issue #116) ───────────────────────────────────────
  // A turn is complete only after ONE validated terminal frame. Truncation,
  // malformed JSON, an invalid terminal payload, or EOF without final/failure
  // must throw a typed transport failure — never resolve as a completed answer.

  it("throws on EOF after tokens without a terminal frame (truncation)", async () => {
    const events = await streamOf(['data: {"kind":"token","token":"partial"}\n\n']);
    await expect(collect(events)).rejects.toBeInstanceOf(ChatStreamError);
  });

  it("throws on a malformed JSON frame instead of silently dropping it", async () => {
    const events = await streamOf(["data: {not json}\n\n", VALID_FINAL]);
    await expect(collect(events)).rejects.toBeInstanceOf(ChatStreamError);
  });

  it("throws on an unknown / missing frame discriminator", async () => {
    const events = await streamOf(['data: {"kind":"mystery"}\n\n']);
    await expect(collect(events)).rejects.toBeInstanceOf(ChatStreamError);
  });

  it("throws on a final frame whose envelope is missing or not an object", async () => {
    await expect(collect(await streamOf(['data: {"kind":"final"}\n\n']))).rejects.toBeInstanceOf(
      ChatStreamError,
    );
    await expect(
      collect(await streamOf(['data: {"kind":"final","envelope":"nope"}\n\n'])),
    ).rejects.toBeInstanceOf(ChatStreamError);
  });

  it("accepts exactly one valid `final` and stops (a second terminal cannot replace it)", async () => {
    const events = await collect(
      await streamOf([
        VALID_FINAL,
        'data: {"kind":"failure","failure":{"code":"X","message":"y"}}\n\n',
      ]),
    );
    // Only the first terminal is delivered; trailing frames are never read.
    expect(events.map((e) => e.kind)).toEqual(["final"]);
  });

  it("accepts a valid `failure` terminal and ends cleanly", async () => {
    const events = await collect(
      await streamOf(['data: {"kind":"failure","failure":{"code":"BUDGET","message":"m"}}\n\n']),
    );
    expect(events.map((e) => e.kind)).toEqual(["failure"]);
  });

  it("returns the structured ChatUnavailable state on 503 (kill switch — CHAT-009)", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        HttpResponse.json(
          { code: "CHAT_DISABLED", message: "off", reason: "kill_switch_global" },
          { status: 503 },
        ),
      ),
    );
    const outcome = await postChatTurn({ message: "hi", marketplaceAccountId: "acct" });
    expect(outcome.kind).toBe("unavailable");
    if (outcome.kind !== "unavailable") return;
    expect(outcome.unavailable.reason).toBe("kill_switch_global");
  });
});
