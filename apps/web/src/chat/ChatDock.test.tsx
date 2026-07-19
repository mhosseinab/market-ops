import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { ApprovalCardView } from "../data/types";
import { approvalCardAwaiting, CARD_ID, dailyBriefing, EVENT_ID } from "../test/msw/fixtures";
import { BASE, sseResponse } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";
import type { ChatStreamEvent } from "./types";

// S29 chat-dock component tests against MSW + a mocked SSE `/chat` stream. They
// exercise the never-cut invariants on the dock surface: free-text containment,
// §8.1 cached-control non-reuse, the kill switch, the 20-row rule, the seven
// statement kinds, deep links, and Persian/Latin digit normalization.

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

/** Open the dock via the TopBar toggle (proves CHAT-001 one-interaction reach). */
async function openDock() {
  renderRoute("/today");
  fireEvent.click(await screen.findByLabelText(faIR["topbar.chat.toggle"]));
  return screen.findByTestId("chat-dock");
}

function chatFinal(...events: ChatStreamEvent[]): ChatStreamEvent[] {
  return [{ kind: "conversation", conversationId: "conv-1" }, ...events];
}

async function sendComposer(text: string) {
  const input = await screen.findByTestId("chat-input");
  fireEvent.change(input, { target: { value: text } });
  fireEvent.click(screen.getByTestId("chat-send"));
}

describe("ChatDock — reachability + context chip (CHAT-001)", () => {
  it("opens in one interaction from an area and shows the account context chip", async () => {
    await openDock();
    const chip = screen.getByTestId("chat-context-chip");
    expect(chip).toHaveAttribute("data-context", "global");
    expect(chip).toHaveTextContent(faIR["chat.context.global"]);
    expect(screen.getByTestId("chat-footnote")).toHaveTextContent(faIR["chat.footnote"]);
  });

  it("binds the recommendation context chip when entered from a recommendation deep link", async () => {
    renderRoute(`/recommendation?cardId=${CARD_ID}`);
    fireEvent.click(await screen.findByLabelText(faIR["topbar.chat.toggle"]));
    const chip = await screen.findByTestId("chat-context-chip");
    expect(chip).toHaveAttribute("data-context", "recommendation");
  });
});

// ── Free-text containment (headline invariant) ──────────────────────────────
describe("ChatDock — free text never approves (§8, CHAT-041)", () => {
  it("typing 'approve it' fires a read turn but NEVER hits the confirm endpoint", async () => {
    let confirmCalls = 0;
    let chatCalls = 0;
    server.use(
      http.post(`${BASE}/approvals/confirm`, () => {
        confirmCalls += 1;
        return HttpResponse.json({
          cardId: CARD_ID,
          state: "approved",
          reason: "",
          executionPending: true,
        });
      }),
      http.post(`${BASE}/chat`, () => {
        chatCalls += 1;
        return sseResponse(chatFinal({ kind: "final", envelope: { sections: [], evidence: [] } }));
      }),
    );

    await openDock();
    await sendComposer("approve it");

    // The turn fired (a read/Draft turn)…
    await waitFor(() => expect(chatCalls).toBe(1));
    // …but no mutation reached the structured confirm endpoint. Free text changed
    // nothing — the ONLY confirm path is the ApprovalCard control.
    expect(confirmCalls).toBe(0);
  });
});

// ── Envelope: seven statement kinds + evidence + deep link ──────────────────
describe("ChatDock — grounded envelope (CHAT-004/005/006)", () => {
  it("renders the seven visually-distinct statement kinds, evidence, and a deep link", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse(
          chatFinal({
            kind: "final",
            envelope: {
              sections: [
                { kind: "observed", lines: ["fact"] },
                { kind: "dk", lines: ["signal"] },
                { kind: "config", lines: ["floor"] },
                { kind: "calculation", lines: ["contribution"] },
                { kind: "inference", lines: ["guess"] },
                { kind: "missing", lines: ["no cogs"] },
                { kind: "recommendation", lines: ["lower price"] },
              ],
              evidence: [{ ref: "obs-1", quality: "verified", capturedAt: "2026-07-17T09:00:00Z" }],
              deepLink: "/event?eventId=e1",
            },
          }),
        ),
      ),
    );
    await openDock();
    await sendComposer("what happened?");

    const envelope = await screen.findByTestId("chat-envelope");
    for (const kind of [
      "observed",
      "dk",
      "config",
      "calculation",
      "inference",
      "missing",
      "recommendation",
    ]) {
      expect(within(envelope).getByTestId(`statement-${kind}`)).toBeInTheDocument();
    }
    // Inference is explicitly labeled as model inference, not observed fact.
    expect(within(envelope).getByTestId("statement-inference")).toHaveTextContent(
      faIR["chat.statement.inferenceNote"],
    );
    expect(within(envelope).getByTestId("chat-evidence")).toBeInTheDocument();
    expect(within(envelope).getByTestId("chat-envelope-deeplink")).toBeInTheDocument();
  });

  it("fails closed to a missing-evidence state when a claim carries no evidence (CHAT-005)", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse(
          chatFinal({
            kind: "final",
            envelope: { sections: [{ kind: "observed", lines: ["fact"] }], evidence: [] },
          }),
        ),
      ),
    );
    await openDock();
    await sendComposer("price?");
    expect(await screen.findByTestId("chat-evidence-missing")).toBeInTheDocument();
  });

  it("caps an inline table at 20 rows then summarizes + deep-links (CHAT-023)", async () => {
    const rows = Array.from({ length: 45 }, (_, i) => [`sku-${i}`, `${i}`]);
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse(
          chatFinal({
            kind: "final",
            envelope: {
              sections: [],
              evidence: [],
              table: { headers: ["sku", "n"], rows, totalRows: 45, deepLink: "/products" },
            },
          }),
        ),
      ),
    );
    await openDock();
    await sendComposer("list them");
    const table = await screen.findByTestId("chat-table");
    // 20 rendered rows (+ header row), the true total announced, and a deep link.
    expect(within(table).getAllByRole("row").length).toBe(21);
    expect(within(table).getByTestId("chat-table-truncated")).toBeInTheDocument();
    expect(within(table).getByTestId("chat-table-deeplink")).toBeInTheDocument();
  });
});

// ── Structured cards ────────────────────────────────────────────────────────
describe("ChatDock — structured cards", () => {
  it("renders an ambiguity picker before any card (CHAT-007)", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse(
          chatFinal({
            kind: "final",
            envelope: {
              sections: [],
              evidence: [],
              cards: [
                {
                  kind: "picker",
                  options: [
                    {
                      id: "o1",
                      label: "Sony",
                      sku: "DKP-1",
                      deepLink: `/event?eventId=${EVENT_ID}`,
                    },
                  ],
                },
              ],
            },
          }),
        ),
      ),
    );
    await openDock();
    await sendComposer("which sony?");
    expect(await screen.findByTestId("chat-picker")).toBeInTheDocument();
    expect(screen.getByTestId("chat-picker-select")).toBeInTheDocument();
  });

  it("renders a Level-2 before/after proposal that confirms via Settings (CHAT-061)", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse(
          chatFinal({
            kind: "final",
            envelope: {
              sections: [],
              evidence: [],
              cards: [
                {
                  kind: "level2",
                  proposal: { setting: "notify", before: "10:00", after: "12:00" },
                },
              ],
            },
          }),
        ),
      ),
    );
    await openDock();
    await sendComposer("change notify time");
    const card = await screen.findByTestId("chat-level2");
    expect(within(card).getByTestId("l2-before")).toHaveTextContent("10:00");
    expect(within(card).getByTestId("l2-after")).toHaveTextContent("12:00");
    expect(within(card).getByTestId("l2-confirm-settings")).toBeInTheDocument();
  });
});

// ── Approval card host: reuses S27 control + §8.1 non-reuse ──────────────────
describe("ChatDock — approval card (reuses S27 control)", () => {
  async function sendApprovalTurn() {
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse(
          chatFinal({
            kind: "final",
            envelope: {
              sections: [],
              evidence: [],
              cards: [{ kind: "approval", cardId: CARD_ID }],
            },
          }),
        ),
      ),
    );
    await openDock();
    await sendComposer("prepare a price change");
  }

  it("confirms ONLY through the structured control, hitting the same gateway endpoint", async () => {
    let captured: { cardId?: string; binding?: { parameterVersion?: number } } | undefined;
    server.use(
      http.get(`${BASE}/approvals/card`, () => HttpResponse.json(approvalCardAwaiting)),
      http.post(`${BASE}/approvals/confirm`, async ({ request }) => {
        captured = (await request.json()) as typeof captured;
        return HttpResponse.json({
          cardId: CARD_ID,
          state: "approved",
          reason: "",
          executionPending: true,
        });
      }),
    );
    await sendApprovalTurn();

    const confirm = await screen.findByTestId("confirm-approval");
    expect(confirm).toBeEnabled();
    fireEvent.click(confirm);

    await waitFor(() => expect(captured).toBeTruthy());
    expect(captured?.cardId).toBe(CARD_ID);
    expect(captured?.binding?.parameterVersion).toBe(approvalCardAwaiting.binding.parameterVersion);
  });

  it("§8.1: a restored/expired card RE-FETCHES and never reuses a cached executable control", async () => {
    // The live card is EXPIRED with no control — even though the streamed card
    // part could have carried a stale snapshot, the host re-fetches and the
    // confirm control is not actionable.
    const expiredCard: ApprovalCardView = {
      ...approvalCardAwaiting,
      state: "expired",
      hasControl: false,
    };
    let confirmCalls = 0;
    server.use(
      http.get(`${BASE}/approvals/card`, () => HttpResponse.json(expiredCard)),
      http.post(`${BASE}/approvals/confirm`, () => {
        confirmCalls += 1;
        return HttpResponse.json({
          cardId: CARD_ID,
          state: "approved",
          reason: "",
          executionPending: true,
        });
      }),
    );
    await sendApprovalTurn();

    const confirm = await screen.findByTestId("confirm-approval");
    expect(confirm).toBeDisabled();
    fireEvent.click(confirm);
    expect(confirmCalls).toBe(0);
  });
});

// ── Kill switch / disabled mid-conversation (CHAT-009 / §16) ─────────────────
describe("ChatDock — kill switch + screens-only fallback", () => {
  it("degrades to a read-only conversation, disables the composer, and deep-links to Actions", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        HttpResponse.json(
          { code: "CHAT_DISABLED", message: "off", reason: "kill_switch_account" },
          { status: 503 },
        ),
      ),
    );
    await openDock();
    await sendComposer("status?");

    const banner = await screen.findByTestId("chat-unavailable");
    expect(banner).toHaveAttribute("data-reason", "kill_switch_account");
    expect(screen.getByTestId("chat-unavailable-actions")).toBeInTheDocument();
    // Existing turn stays visible (read-only); the composer is disabled.
    expect(screen.getByTestId("chat-msg-user")).toHaveTextContent("status?");
    await waitFor(() => expect(screen.getByTestId("chat-input")).toBeDisabled());
  });
});

// ── Briefing panel (CHAT-010 / §16 failure) ─────────────────────────────────
describe("ChatDock — daily briefing", () => {
  it("renders the ranked briefing whose events match the Today feed", async () => {
    server.use(http.get(`${BASE}/briefing`, () => HttpResponse.json(dailyBriefing)));
    await openDock();
    const briefing = await screen.findByTestId("briefing");
    expect(within(briefing).getAllByTestId("briefing-row").length).toBe(2);
    expect(within(briefing).getAllByTestId("briefing-open")[0]).toBeInTheDocument();
  });

  it("shows the dated last-briefing failure state on error; Today stays current (§16)", async () => {
    server.use(
      http.get(`${BASE}/briefing`, () =>
        HttpResponse.json({ code: "NO_BRIEFING", message: "none" }, { status: 404 }),
      ),
    );
    await openDock();
    expect(await screen.findByTestId("briefing-failure")).toBeInTheDocument();
  });
});

// ── Persian/Latin digit normalization at the input boundary (CHAT-081) ──────
describe("ChatDock — digit normalization (CHAT-081)", () => {
  it("Persian and Latin digits produce an identical outgoing turn message", async () => {
    const messages: string[] = [];
    server.use(
      http.post(`${BASE}/chat`, async ({ request }) => {
        const body = (await request.json()) as { message: string };
        messages.push(body.message);
        return sseResponse(chatFinal({ kind: "final", envelope: { sections: [], evidence: [] } }));
      }),
    );
    await openDock();
    await sendComposer("قیمت ۱۲۳");
    await waitFor(() => expect(messages.length).toBe(1));
    await sendComposer("قیمت 123");
    await waitFor(() => expect(messages.length).toBe(2));
    expect(messages[0]).toBe(messages[1]);
    expect(messages[0]).toBe("قیمت 123");
  });
});

// ── Terminal-frame invariant on the dock surface (issue #116) ───────────────
// A truncated or malformed stream must render an unmistakable incomplete state,
// never a completed answer. Partial tokens may show, but with no completed
// envelope/cards and a visible transport-failure notice.
describe("ChatDock — truncated / malformed streams never complete (#116)", () => {
  function rawChat(chunks: readonly string[]) {
    server.use(
      http.post(`${BASE}/chat`, () => {
        const encoder = new TextEncoder();
        const stream = new ReadableStream<Uint8Array>({
          start(controller) {
            for (const c of chunks) controller.enqueue(encoder.encode(c));
            controller.close();
          },
        });
        return new HttpResponse(stream, { headers: { "content-type": "text/event-stream" } });
      }),
    );
  }

  it("EOF after tokens but before a terminal frame renders incomplete, never complete", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse(chatFinal({ kind: "token", token: "partial answer" })),
      ),
    );
    await openDock();
    await sendComposer("what happened?");
    // The transport-failure notice appears; no completed envelope is rendered.
    expect(await screen.findByTestId("chat-transport-failure")).toBeInTheDocument();
    expect(screen.queryByTestId("chat-envelope")).not.toBeInTheDocument();
  });

  it("a malformed JSON frame fails the turn", async () => {
    rawChat(['data: {"kind":"conversation","conversationId":"c1"}\n\n', "data: {not json}\n\n"]);
    await openDock();
    await sendComposer("price?");
    expect(await screen.findByTestId("chat-transport-failure")).toBeInTheDocument();
  });

  it("a final frame with an invalid envelope fails closed, not an empty completed answer", async () => {
    server.use(
      http.post(`${BASE}/chat`, () => sseResponse(chatFinal({ kind: "final" } as ChatStreamEvent))),
    );
    await openDock();
    await sendComposer("summary?");
    expect(await screen.findByTestId("chat-transport-failure")).toBeInTheDocument();
    expect(screen.queryByTestId("chat-envelope")).not.toBeInTheDocument();
  });

  it("a valid `failure` terminal renders the structured server failure state", async () => {
    server.use(
      http.post(`${BASE}/chat`, () =>
        sseResponse(
          chatFinal({
            kind: "failure",
            failure: { code: "BUDGET_EXCEEDED", message: "stopped", deepLink: "/actions" },
          }),
        ),
      ),
    );
    await openDock();
    await sendComposer("do it");
    expect(await screen.findByTestId("chat-failure")).toBeInTheDocument();
    // A server failure is NOT the client transport-failure notice.
    expect(screen.queryByTestId("chat-transport-failure")).not.toBeInTheDocument();
  });
});
