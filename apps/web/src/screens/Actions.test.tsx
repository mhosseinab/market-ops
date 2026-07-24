import { faIR } from "@market-ops/locale";
import { fireEvent, screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import {
  ACTION_ID,
  actionAwaitingExternal,
  actionExternallyExecuted,
  actionLapsed,
  type actionList,
  actionProposed,
  actionWriteAccepted,
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
    // "بدون تطبیق" is BOTH the row state and its group heading — assert presence,
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
