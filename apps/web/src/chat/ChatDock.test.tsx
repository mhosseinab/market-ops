import { en, faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { ApprovalCardView } from "../data/types";
import { approvalCardAwaiting, CARD_ID, dailyBriefing, EVENT_ID } from "../test/msw/fixtures";
import { BASE, sseResponse } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";
import type { ChatStreamEvent, ChatTurnRequest } from "./types";

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

// ── Deterministic context binding on the wire (CHAT-007, issue #115) ────────
describe("ChatDock — binds route + picker context to the conversation (CHAT-007)", () => {
  function captureChatBodies(bodies: ChatTurnRequest[]) {
    server.use(
      http.post(`${BASE}/chat`, async ({ request }) => {
        bodies.push((await request.json()) as ChatTurnRequest);
        return sseResponse([
          { kind: "conversation", conversationId: `conv-${bodies.length}` },
          { kind: "final", envelope: { sections: [], evidence: [] } },
        ]);
      }),
    );
  }

  it("a first turn from a contextual route carries the exact displayed binding", async () => {
    const bodies: ChatTurnRequest[] = [];
    captureChatBodies(bodies);
    renderRoute(`/recommendation?cardId=${CARD_ID}`);
    fireEvent.click(await screen.findByLabelText(faIR["topbar.chat.toggle"]));
    await sendComposer("why this?");

    await waitFor(() => expect(bodies.length).toBe(1));
    const [first] = bodies;
    expect(first?.context).toEqual({ kind: "recommendation", entityId: CARD_ID });
    // A first turn never claims a server context version.
    expect(first?.context?.contextVersion).toBeUndefined();
    expect(first?.conversationId).toBeUndefined();
  });

  it("selecting a picker option binds THAT option via an explicit transition before any card", async () => {
    const bodies: ChatTurnRequest[] = [];
    server.use(
      http.post(`${BASE}/chat`, async ({ request }) => {
        bodies.push((await request.json()) as ChatTurnRequest);
        // First turn returns a picker; the picker-bound turn returns an envelope.
        // The `conversation` frame carries the AUTHORITATIVE context the GATEWAY
        // persisted (issue #115) — global v1 first, then the transitioned
        // product/opt-x v2 — never a value the client fabricated.
        const first = bodies.length === 1;
        const conversationFrame: ChatStreamEvent = first
          ? {
              kind: "conversation",
              conversationId: "conv-1",
              contextKind: "global",
              contextVersion: 1,
            }
          : {
              kind: "conversation",
              conversationId: "conv-1",
              contextKind: "product",
              contextEntityId: "opt-x",
              contextVersion: 2,
            };
        const cards = first
          ? [{ kind: "picker", options: [{ id: "opt-x", label: "Sony X" }] }]
          : [];
        return sseResponse([
          conversationFrame,
          { kind: "final", envelope: { sections: [], evidence: [], cards } },
        ]);
      }),
    );
    await openDock();
    await sendComposer("which sony?");
    const select = await screen.findByTestId("chat-picker-select");
    fireEvent.click(select);

    await waitFor(() => expect(bodies.length).toBe(2));
    // The picker-bound turn carries the EXACT option as an explicit, versioned
    // transition on the SAME conversation — never a silent relabel. The version it
    // sends back is the one the gateway ECHOED on the first turn (v1), not a
    // client-fabricated value.
    const bound = bodies[1];
    expect(bound?.conversationId).toBe("conv-1");
    expect(bound?.context).toEqual({
      kind: "product",
      entityId: "opt-x",
      contextVersion: 1,
      transition: true,
    });
  });

  it("the chip UPDATES to the gateway-bound kind after a picker transition from a non-product route", async () => {
    let calls = 0;
    server.use(
      http.post(`${BASE}/chat`, () => {
        calls += 1;
        const first = calls === 1;
        const conversationFrame: ChatStreamEvent = first
          ? {
              kind: "conversation",
              conversationId: "conv-1",
              contextKind: "global",
              contextVersion: 1,
            }
          : {
              kind: "conversation",
              conversationId: "conv-1",
              contextKind: "product",
              contextEntityId: "opt-x",
              contextVersion: 2,
            };
        const cards = first
          ? [{ kind: "picker", options: [{ id: "opt-x", label: "Sony X" }] }]
          : [];
        return sseResponse([
          conversationFrame,
          { kind: "final", envelope: { sections: [], evidence: [], cards } },
        ]);
      }),
    );
    // Open on /today (global route): the chip starts global.
    await openDock();
    const chip = screen.getByTestId("chat-context-chip");
    expect(chip).toHaveAttribute("data-context", "global");

    await sendComposer("which sony?");
    fireEvent.click(await screen.findByTestId("chat-picker-select"));

    // After the picker binds product/opt-x, the chip renders the GATEWAY-bound kind
    // (product) — never the route kind (global) it would show optimistically.
    await waitFor(() =>
      expect(screen.getByTestId("chat-context-chip")).toHaveAttribute("data-context", "product"),
    );
  });

  it("a gateway context rejection (409) fails the turn and leaves the bound context unchanged", async () => {
    let calls = 0;
    server.use(
      http.post(`${BASE}/chat`, () => {
        calls += 1;
        if (calls === 1) {
          // First turn commits the authoritative recommendation binding at v1.
          return sseResponse([
            {
              kind: "conversation",
              conversationId: "conv-1",
              contextKind: "recommendation",
              contextEntityId: CARD_ID,
              contextVersion: 1,
            },
            { kind: "final", envelope: { sections: [], evidence: [] } },
          ]);
        }
        // The binding turn is REJECTED as stale (409): no envelope/cards, no relabel.
        return HttpResponse.json(
          {
            code: "CONVERSATION_CONTEXT_STALE",
            message: "the conversation context has changed; reopen it from the current screen",
          },
          { status: 409 },
        );
      }),
    );
    renderRoute(`/recommendation?cardId=${CARD_ID}`);
    fireEvent.click(await screen.findByLabelText(faIR["topbar.chat.toggle"]));
    await sendComposer("why this?");
    // The first turn committed the authoritative recommendation chip.
    await waitFor(() =>
      expect(screen.getByTestId("chat-context-chip")).toHaveAttribute(
        "data-context",
        "recommendation",
      ),
    );

    await sendComposer("and now?");
    await waitFor(() => expect(calls).toBe(2));
    // The rejected turn renders the transport-failure/incomplete state and attaches
    // NO completed envelope/cards to THAT turn; the bound context/chip is UNCHANGED
    // (no relabel, no fabricated success).
    const failure = await screen.findByTestId("chat-transport-failure");
    const rejectedTurn = failure.closest('[data-testid="chat-msg-assistant"]') as HTMLElement;
    expect(within(rejectedTurn).queryByTestId("chat-envelope")).not.toBeInTheDocument();
    expect(within(rejectedTurn).queryByTestId("chat-picker")).not.toBeInTheDocument();
    expect(screen.getByTestId("chat-context-chip")).toHaveAttribute(
      "data-context",
      "recommendation",
    );
  });

  it("a route change mid-conversation opens a NEW bound conversation, never relabeling", async () => {
    const bodies: ChatTurnRequest[] = [];
    captureChatBodies(bodies);
    const { navigate } = renderRoute(`/recommendation?cardId=${CARD_ID}`);
    fireEvent.click(await screen.findByLabelText(faIR["topbar.chat.toggle"]));
    await sendComposer("first");
    await waitFor(() => expect(bodies.length).toBe(1));

    // Navigate to a different entity context, then send again.
    navigate(`/event?eventId=${EVENT_ID}`);
    await sendComposer("second");
    await waitFor(() => expect(bodies.length).toBe(2));

    // The second turn did NOT continue the first conversation under a new chip; it
    // opened a fresh bound conversation for the new context (no silent relabel).
    const second = bodies[1];
    expect(second?.conversationId).toBeUndefined();
    expect(second?.context).toEqual({ kind: "event", entityId: EVENT_ID });
  });
});

// ── Active locale on the wire (LOC-001/LOC-007, issue #120) ─────────────────
describe("ChatDock — sends the ACTIVE locale with every turn (LOC-001)", () => {
  function captureBodies(bodies: ChatTurnRequest[], frame?: Partial<ChatStreamEvent>) {
    server.use(
      http.post(`${BASE}/chat`, async ({ request }) => {
        bodies.push((await request.json()) as ChatTurnRequest);
        return sseResponse([
          { kind: "conversation", conversationId: "conv-1", ...frame },
          { kind: "final", envelope: { sections: [], evidence: [] } },
        ]);
      }),
    );
  }

  it("a first turn carries the ACTIVE locale (fa-IR) with no version or transition", async () => {
    const bodies: ChatTurnRequest[] = [];
    captureBodies(bodies);
    await openDock(); // renders at DEFAULT_LOCALE = fa-IR
    await sendComposer("سلام");
    await waitFor(() => expect(bodies.length).toBe(1));
    expect(bodies[0]?.locale).toBe("fa-IR");
    expect(bodies[0]?.localeVersion).toBeUndefined();
    expect(bodies[0]?.localeTransition).toBeUndefined();
  });

  it("a first turn carries the ACTIVE locale (en) when the app renders in English", async () => {
    const bodies: ChatTurnRequest[] = [];
    captureBodies(bodies);
    renderRoute("/today", { locale: "en" });
    fireEvent.click(await screen.findByLabelText(en["topbar.chat.toggle"]));
    await screen.findByTestId("chat-dock");
    await sendComposer("hi");
    await waitFor(() => expect(bodies.length).toBe(1));
    expect(bodies[0]?.locale).toBe("en");
  });

  it("Persian digits, Latin digits, technical ids, and neutral text NEVER change the sent locale", async () => {
    const bodies: ChatTurnRequest[] = [];
    captureBodies(bodies);
    await openDock(); // fa-IR active
    await sendComposer("قیمت ۱۲۳"); // Persian text + Persian digits
    await waitFor(() => expect(bodies.length).toBe(1));
    await sendComposer("price 123"); // Latin text + Latin digits
    await waitFor(() => expect(bodies.length).toBe(2));
    await sendComposer("SKU DKP-42 / v-7"); // technical ids only
    await waitFor(() => expect(bodies.length).toBe(3));
    // The message content differs wildly, but the locale is DATA from the active
    // provider — never inferred from digit shape or text.
    for (const b of bodies) expect(b.locale).toBe("fa-IR");
  });

  it("a same-locale continuation PRESERVES the bound locale, echoing the gateway version", async () => {
    const bodies: ChatTurnRequest[] = [];
    captureBodies(bodies, { localeTag: "fa-IR", localeVersion: 1 });
    await openDock();
    await sendComposer("یک");
    await waitFor(() => expect(bodies.length).toBe(1));
    await sendComposer("دو");
    await waitFor(() => expect(bodies.length).toBe(2));
    // The first turn claims no version; the continuation sends back the gateway's
    // echoed version and carries NO transition (idempotent, never a relabel).
    expect(bodies[0]?.localeVersion).toBeUndefined();
    expect(bodies[1]?.locale).toBe("fa-IR");
    expect(bodies[1]?.localeVersion).toBe(1);
    expect(bodies[1]?.localeTransition).toBeUndefined();
  });

  it("a locale switch mid-conversation is an EXPLICIT, versioned transition (never a silent relabel)", async () => {
    const bodies: ChatTurnRequest[] = [];
    server.use(
      http.post(`${BASE}/chat`, async ({ request }) => {
        bodies.push((await request.json()) as ChatTurnRequest);
        const first = bodies.length === 1;
        return sseResponse([
          {
            kind: "conversation",
            conversationId: "conv-1",
            localeTag: first ? "fa-IR" : "en",
            localeVersion: first ? 1 : 2,
          },
          { kind: "final", envelope: { sections: [], evidence: [] } },
        ]);
      }),
    );
    await openDock(); // fa-IR active
    await sendComposer("اول");
    await waitFor(() => expect(bodies.length).toBe(1));
    // Toggle the app locale to English via the TopBar control, then send again on the
    // SAME conversation.
    fireEvent.click(screen.getByText(faIR["app.langName.en"]));
    await sendComposer("second");
    await waitFor(() => expect(bodies.length).toBe(2));
    expect(bodies[1]?.locale).toBe("en");
    expect(bodies[1]?.localeVersion).toBe(1); // the from-version the gateway echoed
    expect(bodies[1]?.localeTransition).toBe(true);
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
