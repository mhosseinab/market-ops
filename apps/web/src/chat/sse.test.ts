import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { BASE, sseResponse } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { postChatTurn } from "./sse";
import type { ChatStreamEvent } from "./types";

async function collect(gen: AsyncGenerator<ChatStreamEvent>): Promise<ChatStreamEvent[]> {
  const out: ChatStreamEvent[] = [];
  for await (const e of gen) out.push(e);
  return out;
}

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

  it("splits frames across chunk boundaries and ignores malformed frames", async () => {
    // A single frame delivered in two chunks, plus a garbage frame that is dropped.
    server.use(
      http.post(`${BASE}/chat`, () => {
        const encoder = new TextEncoder();
        const stream = new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"kind":"to'));
            controller.enqueue(encoder.encode('ken","token":"AB"}\n\n'));
            controller.enqueue(encoder.encode("data: {not json}\n\n"));
            controller.enqueue(encoder.encode('data: {"kind":"final"}\n\n'));
            controller.close();
          },
        });
        return new HttpResponse(stream, { headers: { "content-type": "text/event-stream" } });
      }),
    );
    const outcome = await postChatTurn({ message: "hi", marketplaceAccountId: "acct" });
    if (outcome.kind !== "stream") throw new Error("expected stream");
    const events = await collect(outcome.events);
    expect(events.map((e) => e.kind)).toEqual(["token", "final"]);
    expect(events[0]?.token).toBe("AB");
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
