import { faIR } from "@market-ops/locale";
import { fireEvent, screen, within } from "@testing-library/react";
import { delay, HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import {
  ACTION_ID,
  actionAwaitingExternal,
  actionExternallyExecuted,
  actionLapsed,
  type actionList,
  actionProposed,
  actionWriteAccepted,
  approvalCardAwaiting,
  CARD_ID,
  CARD_ID_AWAITING,
  CARD_ID_EXTERNALLY_EXECUTED,
  CARD_ID_LAPSED,
  CARD_ID_PROPOSED,
  execAccepted,
  execFailed,
  execPendingReconciliation,
  execRejected,
  outcomeList,
} from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

/** Select a row through its ACCESSIBLE control (a real button, not a row click). */
async function selectCard(cardId: string) {
  const btn = await screen.findByTestId(`action-select-${cardId}`);
  fireEvent.click(btn);
  return btn;
}

/** Serve a one-row action list plus its matching execution record. */
function onlyRow(row: (typeof actionList)["items"][number], exec?: unknown) {
  const handlers = [http.get(`${BASE}/actions`, () => HttpResponse.json({ items: [row] }))];
  if (exec) handlers.push(http.get(`${BASE}/actions/execution`, () => HttpResponse.json(exec)));
  server.use(...handlers);
}

describe("Actions — multi-mode queue discovery (issue #106)", () => {
  it("lists write AND recommend-only actions grouped by canonical state, with NO deep link", async () => {
    renderRoute("/actions");

    // Discovery comes from the account list, not a deep link.
    expect(await screen.findByTestId(`action-select-${CARD_ID}`)).toBeInTheDocument();
    expect(screen.getByTestId(`action-select-${CARD_ID_AWAITING}`)).toBeInTheDocument();
    expect(screen.getByTestId(`action-select-${CARD_ID_EXTERNALLY_EXECUTED}`)).toBeInTheDocument();
    expect(screen.getByTestId(`action-select-${CARD_ID_LAPSED}`)).toBeInTheDocument();
    expect(screen.getByTestId(`action-select-${CARD_ID_PROPOSED}`)).toBeInTheDocument();

    // Grouped by CANONICAL state: the awaiting recommend-only row sits in the
    // awaiting group, the lapsed row in its own group, and both the accepted write
    // and the externally-executed recommend-only row share `succeeded`.
    expect(
      within(screen.getByTestId("actions-group-awaiting")).getByTestId(
        `action-select-${CARD_ID_AWAITING}`,
      ),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("actions-group-lapsed")).getByTestId(
        `action-select-${CARD_ID_LAPSED}`,
      ),
    ).toBeInTheDocument();
    const succeeded = within(screen.getByTestId("actions-group-succeeded"));
    expect(succeeded.getByTestId(`action-select-${CARD_ID}`)).toBeInTheDocument();
    expect(
      succeeded.getByTestId(`action-select-${CARD_ID_EXTERNALLY_EXECUTED}`),
    ).toBeInTheDocument();
    // A pre-execution card is its own group — never mixed into an executed one.
    expect(
      within(screen.getByTestId("actions-group-proposed")).getByTestId(
        `action-select-${CARD_ID_PROPOSED}`,
      ),
    ).toBeInTheDocument();
  });

  it("labels each mode with its OWN vocabulary: recommend-only never reads as an executed write", async () => {
    renderRoute("/actions");
    await screen.findByTestId(`action-select-${CARD_ID_EXTERNALLY_EXECUTED}`);

    // The recommend-only rows carry EXE-005 terms…
    expect(screen.getByText(faIR["state.awaitingExternalExecution"])).toBeInTheDocument();
    expect(screen.getByText(faIR["state.externallyExecuted"])).toBeInTheDocument();
    // "بدون تغییر متناظر" is BOTH the row state and its group heading — assert presence,
    // not uniqueness, and rely on the group-membership test above for placement.
    expect(screen.getAllByText(faIR["state.lapsed"]).length).toBeGreaterThan(0);
    // …and are labelled recommend-only, never as a marketplace write.
    expect(screen.getAllByText(faIR["actions.mode.recommendOnly"]).length).toBe(3);

    // NEGATIVE: the "Accepted by DK" write term appears ONLY for the write row.
    const accepted = faIR["state.accepted"].replace("{marketplace}", faIR["marketplace.name"]);
    expect(screen.getAllByText(accepted)).toHaveLength(1);
  });

  it("keeps a Lapsed action free of any execution claim (EXE-005, AUD-001)", async () => {
    renderRoute("/actions");
    await selectCard(CARD_ID_LAPSED);

    const panel = await screen.findByTestId("action-lapsed");
    expect(panel).toHaveTextContent(faIR["actions.lapsed.noClaimNote"]);
    // NEGATIVE: no write panel, no retry, and no fabricated outcome window.
    expect(screen.queryByTestId("action-accepted")).toBeNull();
    expect(screen.queryByTestId("action-failed")).toBeNull();
    expect(screen.queryByTestId("action-retry")).toBeNull();
    expect(screen.queryByTestId("outcome-window")).toBeNull();
    // Absence is STATED, never implied.
    expect(await screen.findByTestId("outcome-none")).toHaveTextContent(
      faIR["actions.outcome.none"],
    );
  });

  it("shows an awaiting recommend-only action as not-yet-written, with no retry", async () => {
    renderRoute("/actions");
    await selectCard(CARD_ID_AWAITING);

    expect(await screen.findByTestId("action-awaiting-external")).toHaveTextContent(
      faIR["actions.recommendOnly.noWriteNote"],
    );
    expect(screen.queryByTestId("action-retry")).toBeNull();
    expect(screen.queryByTestId("action-pending-reconciliation")).toBeNull();
  });

  it("opens the outcome window for an externally-executed action, matched on (actionId, cardId)", async () => {
    renderRoute("/actions");
    await selectCard(CARD_ID_EXTERNALLY_EXECUTED);

    expect(await screen.findByTestId("action-externally-executed")).toBeInTheDocument();
    expect(await screen.findByTestId("outcome-window")).toBeInTheDocument();
    // Its window is still open: no result is fabricated before the window closes.
    expect(screen.queryByTestId("outcome-result")).toBeNull();
  });

  it("never renders one action's outcome under another (exact card binding)", async () => {
    renderRoute("/actions");
    // The awaiting action has NO outcome window in the list; the write action does.
    await selectCard(CARD_ID_AWAITING);
    expect(await screen.findByTestId("outcome-none")).toBeInTheDocument();
    expect(screen.queryByTestId("outcome-window")).toBeNull();
  });
});

describe("Actions — write-mode result handling (EXE-003)", () => {
  it("explains Pending Reconciliation and offers NO retry for an unreconciled action", async () => {
    onlyRow(
      {
        ...actionWriteAccepted,
        state: "pending_reconciliation",
        canonicalState: "awaiting",
        externalState: "pending_reconciliation",
      },
      execPendingReconciliation,
    );
    renderRoute("/actions");
    await selectCard(CARD_ID);

    expect(await screen.findByTestId("action-pending-reconciliation")).toHaveTextContent(
      faIR["actions.pending.title"],
    );
    // NEGATIVE: no terminal controls for a non-terminal (unknown) result.
    expect(screen.queryByTestId("action-retry")).toBeNull();
    expect(screen.queryByTestId("action-rejected")).toBeNull();
    expect(screen.queryByTestId("action-failed")).toBeNull();
    expect(screen.getByTestId("action-reconcile-read")).toBeInTheDocument();
  });

  it("offers retry ONLY for a definitively failed write", async () => {
    onlyRow(
      {
        ...actionWriteAccepted,
        state: "failed",
        canonicalState: "failed",
        externalState: "failed",
      },
      execFailed,
    );
    renderRoute("/actions");
    await selectCard(CARD_ID);

    expect(await screen.findByTestId("action-failed")).toBeInTheDocument();
    expect(await screen.findByTestId("action-retry")).toBeInTheDocument();
    expect(screen.queryByTestId("action-rejected")).toBeNull();
  });

  it("keeps Rejected TERMINAL: rejection context and NEVER a Retry control", async () => {
    onlyRow(
      {
        ...actionWriteAccepted,
        state: "rejected",
        canonicalState: "rejected",
        externalState: "rejected",
      },
      execRejected,
    );
    renderRoute("/actions");
    await selectCard(CARD_ID);

    expect(await screen.findByTestId("action-rejected")).toHaveTextContent(
      faIR["actions.rejected.body"],
    );
    expect(screen.queryByTestId("action-retry")).toBeNull();
    expect(screen.queryByTestId("action-failed")).toBeNull();
  });

  it("shows the outcome window with result + confidence and the audit trail for an accepted write", async () => {
    renderRoute("/actions");
    await selectCard(CARD_ID);

    expect(await screen.findByTestId("outcome-window")).toBeInTheDocument();
    expect(screen.getByTestId("outcome-result")).toHaveTextContent(faIR["outcomeResult.positive"]);
    expect(screen.getByTestId("outcome-confidence")).toHaveTextContent(
      faIR["outcomeConfidence.high"],
    );
    expect(screen.queryByTestId("action-retry")).toBeNull();
    expect(screen.getByTestId("audit-trail")).toBeInTheDocument();
    expect(screen.getByText(faIR["actions.audit.independentNote"])).toBeInTheDocument();
  });
});

describe("Actions — truthful query and selection states (STATE_MATRIX)", () => {
  it("keeps an outcome-list ERROR distinct from no-outcome-yet", async () => {
    server.use(
      http.get(`${BASE}/outcomes/list`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
    );
    renderRoute("/actions");
    await selectCard(CARD_ID);

    expect(await screen.findByTestId("outcome-error")).toBeInTheDocument();
    expect(screen.queryByTestId("outcome-window")).toBeNull();
    expect(screen.queryByTestId("outcome-none")).toBeNull();
  });

  it("renders a LIST error as an error, never as an empty queue", async () => {
    server.use(
      http.get(`${BASE}/actions`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
    );
    renderRoute("/actions");

    expect(await screen.findByText(faIR["state.error.title"])).toBeInTheDocument();
    // NEGATIVE: the reassuring empty state must never stand in for a failed read.
    expect(screen.queryByText(faIR["state.empty.title"])).toBeNull();
  });

  it("shows the reassuring empty state only when the account genuinely has no actions", async () => {
    server.use(http.get(`${BASE}/actions`, () => HttpResponse.json({ items: [] })));
    renderRoute("/actions");
    expect(await screen.findByText(faIR["state.empty.title"])).toBeInTheDocument();
  });

  it("distinguishes a FILTERED empty result from an empty account", async () => {
    // Only a lapsed row exists; the "executed" filter matches nothing.
    onlyRow(actionLapsed);
    renderRoute("/actions");
    await screen.findByTestId(`action-select-${CARD_ID_LAPSED}`);

    fireEvent.click(screen.getByText(faIR["actions.filter.executed"]));
    expect(await screen.findByTestId("actions-empty-filtered")).toHaveTextContent(
      faIR["actions.list.emptyFiltered"],
    );
    expect(screen.queryByText(faIR["state.empty.title"])).toBeNull();
  });

  it("backs selection with the URL so it survives a reload, and prompts before selection", async () => {
    renderRoute("/actions");
    expect(await screen.findByTestId("actions-select-prompt")).toBeInTheDocument();

    await selectCard(CARD_ID_LAPSED);
    expect(await screen.findByTestId("action-lapsed")).toBeInTheDocument();
    expect(screen.getByTestId(`action-select-${CARD_ID_LAPSED}`)).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    // A fresh render at the deep link restores the SAME selection.
    renderRoute(`/actions?cardId=${CARD_ID_LAPSED}`);
    expect(await screen.findAllByTestId("action-lapsed")).not.toHaveLength(0);
  });

  it("still resolves a legacy actionId deep link to its card", async () => {
    server.use(http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execAccepted)));
    renderRoute(`/actions?actionId=${ACTION_ID}`);
    expect(await screen.findByTestId("action-accepted")).toBeInTheDocument();
  });
});

// ── Fix cycle 1 (issue #106 review) ─────────────────────────────────────────
// Each test below reproduces ONE upheld blocking finding. They are negative
// tests first: a pending/failed/page-bounded READ must never surface as a
// definitive negative claim, and a technical identifier must never be
// interpolated into RTL copy.
describe("Actions — F2: the outcome panel never makes an unsupported negative claim", () => {
  it("F2: does not claim 'no outcome window' while the CARD read is still in flight", async () => {
    server.use(
      http.get(`${BASE}/approvals/card`, async () => {
        await delay("infinite");
        return HttpResponse.json(approvalCardAwaiting);
      }),
    );
    renderRoute("/actions");
    await selectCard(CARD_ID);

    // The action id comes from the card binding; until it resolves, absence is
    // UNKNOWN, not established.
    expect(await screen.findByTestId("outcome-pending")).toBeInTheDocument();
    expect(screen.queryByTestId("outcome-none")).toBeNull();
  });

  it("F2: does not claim 'no outcome window' when the CARD read FAILS", async () => {
    server.use(
      http.get(`${BASE}/approvals/card`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
    );
    renderRoute("/actions");
    await selectCard(CARD_ID);

    expect(await screen.findByTestId("outcome-error")).toBeInTheDocument();
    // NEGATIVE: a permanently failed read must never harden into "no window".
    expect(screen.queryByTestId("outcome-none")).toBeNull();
  });

  it("F2: renders the window from the ACTION-scoped read when it is outside the list page", async () => {
    // The account-wide list is page-bounded (newest 200): the selected action's
    // window is real but simply not in the returned page.
    server.use(http.get(`${BASE}/outcomes/list`, () => HttpResponse.json({ items: [] })));
    renderRoute("/actions");
    await selectCard(CARD_ID);

    expect(await screen.findByTestId("outcome-window")).toBeInTheDocument();
    expect(screen.queryByTestId("outcome-none")).toBeNull();
  });

  it("F2: states absence ONLY from the authoritative per-action read (404)", async () => {
    renderRoute("/actions");
    await selectCard(CARD_ID_LAPSED);
    expect(await screen.findByTestId("outcome-none")).toHaveTextContent(
      faIR["actions.outcome.none"],
    );
  });
});

describe("Actions — F4: technical identifiers stay out of RTL copy", () => {
  it("F4: the row control's COPY carries no raw card id; the id reaches AT via the LTR-isolated cell", async () => {
    renderRoute("/actions");
    const btn = await screen.findByTestId(`action-select-${CARD_ID}`);

    // The rendered copy is the catalog label alone — no interpolated identifier.
    expect(btn).toHaveTextContent(faIR["actions.col.select"]);
    expect(btn.textContent).not.toContain(CARD_ID);
    // The accessible name still names the row, through the LTR-isolated ID cell.
    expect(btn).toHaveAccessibleName(expect.stringContaining(CARD_ID) as unknown as string);
  });
});

describe("Actions — F6: a deep-linked action outside the page is never 'nothing selected'", () => {
  it("F6: renders an explicit out-of-page state for a cardId absent from the returned page", async () => {
    // The queue page does not contain the deep-linked card (older than the page).
    onlyRow(actionWriteAccepted);
    renderRoute(`/actions?cardId=${CARD_ID_LAPSED}`);

    expect(await screen.findByTestId("action-not-in-page")).toHaveTextContent(
      faIR["actions.notInPage.body"],
    );
    // NEGATIVE: a valid selection must never render as no selection at all.
    expect(screen.queryByTestId("actions-select-prompt")).toBeNull();
  });

  it("F6: shows a legacy actionId deep link as RESOLVING, never as nothing selected", async () => {
    server.use(
      http.get(`${BASE}/actions/execution`, async () => {
        await delay("infinite");
        return HttpResponse.json(execAccepted);
      }),
    );
    renderRoute(`/actions?actionId=${ACTION_ID}`);

    expect(await screen.findByTestId("actions-deeplink-resolving")).toBeInTheDocument();
    expect(screen.queryByTestId("actions-select-prompt")).toBeNull();
  });
});

// ── Fix cycle 2 (issue #106 review) ─────────────────────────────────────────
// The outcome window belongs to the EXACT card version that was executed. The
// action-scoped read answers per ACTION, and the domain mints a NEWER Draft on
// the same action id after an execution (PD-4 rule 1), so that read may describe
// a different, executed version of the same lineage. Rendering it beside a
// pre-execution card is a false execution claim (EXE-005 / OUT-001).
describe("Actions — W-B1: an outcome window never renders under a pre-execution card version", () => {
  it("W-B1: renders NO outcome window for a card version that carries no execution overlay", async () => {
    renderRoute("/actions");
    await selectCard(CARD_ID_PROPOSED);

    // The main panel says the card was not executed…
    expect(await screen.findByTestId("action-proposed")).toBeInTheDocument();
    // …so the aside may never show an opened/closing/result window beside it,
    // even though this card's ACTION does have one (the executed sibling version).
    expect(await screen.findByTestId("outcome-none-card")).toHaveTextContent(
      faIR["actions.outcome.noneForCard"],
    );
    expect(screen.queryByTestId("outcome-window")).toBeNull();
    expect(screen.queryByTestId("outcome-result")).toBeNull();
    // The absence claim is CARD-scoped: "no window for this action" would itself
    // be untrue for a Draft head whose action does have one.
    expect(screen.queryByTestId("outcome-none")).toBeNull();
  });

  it("W-B1: makes no outcome claim for a card version outside the returned page", async () => {
    // The deep-linked card is not in the page, so whether it carries an execution
    // overlay is UNKNOWN — the action-scoped read cannot stand in for it.
    onlyRow(actionWriteAccepted);
    renderRoute(`/actions?cardId=${CARD_ID_EXTERNALLY_EXECUTED}`);

    expect(await screen.findByTestId("outcome-out-of-page")).toBeInTheDocument();
    expect(screen.queryByTestId("outcome-window")).toBeNull();
    expect(screen.queryByTestId("outcome-none")).toBeNull();
  });
});

describe("Actions — W-B3: a FAILED legacy actionId deep link is never 'nothing selected'", () => {
  it("W-B3: renders the detail error when the legacy deep-link read fails", async () => {
    server.use(
      http.get(`${BASE}/actions/execution`, () =>
        HttpResponse.json({ code: "EXECUTION_ERROR", message: "no_execution" }, { status: 404 }),
      ),
    );
    renderRoute(`/actions?actionId=${ACTION_ID}`);

    expect(await screen.findByTestId("actions-deeplink-error")).toHaveTextContent(
      faIR["actions.detail.error"],
    );
    // NEGATIVE: a selection that was made and could not be resolved is never
    // reported as no selection at all.
    expect(screen.queryByTestId("actions-select-prompt")).toBeNull();
  });
});

describe("Actions — S-1: OUT-001 absence comes only from the outcome handler's answer", () => {
  it("S-1: treats a 404 that is NOT ErrNoWindow as unknown, never as 'no window'", async () => {
    // A transport/routing 404 (unregistered route, proxy rewrite) carries no
    // EXECUTION_ERROR code: it is a failed read, not an absence answer.
    server.use(
      http.get(`${BASE}/outcomes`, () =>
        HttpResponse.json({ code: "NOT_FOUND", message: "no route" }, { status: 404 }),
      ),
    );
    renderRoute("/actions");
    await selectCard(CARD_ID_AWAITING);

    expect(await screen.findByTestId("outcome-error")).toBeInTheDocument();
    expect(screen.queryByTestId("outcome-none")).toBeNull();
  });
});

describe("Actions — fixtures stay contract-shaped", () => {
  it("keeps recommend-only rows free of a write externalState (never-cut)", () => {
    for (const row of [actionAwaitingExternal, actionExternallyExecuted, actionLapsed]) {
      expect(row.executionMode).toBe("recommend_only");
      expect(row.externalState).toBeUndefined();
    }
    // A pre-execution row carries NO overlay at all.
    expect(actionProposed.executionMode).toBeUndefined();
    expect(actionProposed.canonicalState).toBeUndefined();
    // Every outcome window names BOTH its action and its exact card version.
    for (const o of outcomeList.items) {
      expect(o.actionId).toBeTruthy();
      expect(o.cardId).toBeTruthy();
    }
  });
});
