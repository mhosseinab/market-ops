import { faIR } from "@market-ops/locale";
import { screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import {
  ACTION_ID,
  execAccepted,
  execFailed,
  execPendingReconciliation,
  execRejected,
  outcomeClosed,
  outcomeOpen,
} from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

const AT = `/actions?actionId=${ACTION_ID}`;

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Actions (reconciliation / retry gating / outcome / audit)", () => {
  it("explains Pending Reconciliation and offers NO retry for an unreconciled action (EXE-003)", async () => {
    server.use(
      http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execPendingReconciliation)),
    );
    renderRoute(AT);

    // The unknown-external-state explainer is shown, never success/failure.
    expect(await screen.findByTestId("action-pending-reconciliation")).toHaveTextContent(
      faIR["actions.pending.title"],
    );
    // NEGATIVE: no terminal controls for an unreconciled (non-terminal) action.
    expect(screen.queryByTestId("action-retry")).toBeNull();
    expect(screen.queryByTestId("action-rejected")).toBeNull();
    expect(screen.queryByTestId("action-failed")).toBeNull();
    // No outcome assumptions for a non-terminal action.
    expect(screen.queryByTestId("outcome-window")).toBeNull();
    // The only offered action is to read the current DK state (reconcile-first).
    expect(screen.getByTestId("action-reconcile-read")).toBeInTheDocument();
  });

  it("offers retry ONLY for a definitively failed action", async () => {
    server.use(http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execFailed)));
    renderRoute(AT);
    // Failed renders its OWN panel with the Retry affordance (contract: retry is Failed-only).
    expect(await screen.findByTestId("action-failed")).toBeInTheDocument();
    expect(screen.getByTestId("action-retry")).toBeInTheDocument();
    // A failed action is terminal too — its outcome window is loaded.
    expect(await screen.findByTestId("outcome-window")).toBeInTheDocument();
    // It is NOT rendered through the rejected panel.
    expect(screen.queryByTestId("action-rejected")).toBeNull();
  });

  it("keeps Rejected TERMINAL: rejection context, an outcome window, and NEVER a Retry control", async () => {
    server.use(
      http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execRejected)),
      http.get(`${BASE}/outcomes`, () => HttpResponse.json(outcomeClosed)),
    );
    renderRoute(AT);

    // Rejected renders its OWN terminal panel with the rejection explanation…
    expect(await screen.findByTestId("action-rejected")).toHaveTextContent(
      faIR["actions.rejected.body"],
    );
    // …and NEVER the Retry control (the server would return ErrAlreadyTerminal).
    expect(screen.queryByTestId("action-retry")).toBeNull();
    // It is NOT collapsed into the failed panel.
    expect(screen.queryByTestId("action-failed")).toBeNull();
    // OUT-001: the reconciled outcome window is visible for Rejected too.
    expect(await screen.findByTestId("outcome-window")).toBeInTheDocument();
    expect(screen.getByTestId("outcome-result")).toHaveTextContent(faIR["outcomeResult.positive"]);
  });

  it("shows the outcome window with result + confidence and the audit trail for an accepted action, and offers NO Retry", async () => {
    server.use(http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execAccepted)));
    renderRoute(AT);

    expect(await screen.findByTestId("outcome-window")).toBeInTheDocument();
    expect(screen.getByTestId("outcome-result")).toHaveTextContent(faIR["outcomeResult.positive"]);
    expect(screen.getByTestId("outcome-confidence")).toHaveTextContent(
      faIR["outcomeConfidence.high"],
    );
    // Accepted is terminal: no Retry control.
    expect(screen.queryByTestId("action-retry")).toBeNull();
    // Audit trail carries the approval-card snapshot (price + versions at execution).
    expect(screen.getByTestId("audit-trail")).toBeInTheDocument();
    expect(screen.getByText(faIR["actions.audit.independentNote"])).toBeInTheDocument();
  });

  it("renders an OPEN outcome window (no result yet) without a result/confidence value", async () => {
    server.use(
      http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execAccepted)),
      http.get(`${BASE}/outcomes`, () => HttpResponse.json(outcomeOpen)),
    );
    renderRoute(AT);
    expect(await screen.findByTestId("outcome-window")).toBeInTheDocument();
    expect(screen.queryByTestId("outcome-result")).toBeNull();
  });

  it("keeps an outcome-query ERROR distinct from no-outcome-yet (never renders an error as absence)", async () => {
    server.use(
      http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execAccepted)),
      http.get(`${BASE}/outcomes`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
    );
    renderRoute(AT);
    // An explicit error node — NOT the "no outcome yet" absence/pending node, and
    // NOT the loaded window.
    expect(await screen.findByTestId("outcome-error")).toBeInTheDocument();
    expect(screen.queryByTestId("outcome-window")).toBeNull();
    expect(screen.queryByTestId("outcome-pending")).toBeNull();
  });

  it("shows the reassuring empty state without a deep-linked action", async () => {
    renderRoute("/actions");
    expect(await screen.findByText(faIR["state.empty.title"])).toBeInTheDocument();
  });
});
