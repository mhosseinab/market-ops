import { faIR } from "@market-ops/locale";
import { screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import {
  ACTION_ID,
  execAccepted,
  execFailed,
  execPendingReconciliation,
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
    // NEGATIVE: no retry control exists for an unreconciled action.
    expect(screen.queryByTestId("action-retry")).toBeNull();
    // The only offered action is to read the current DK state (reconcile-first).
    expect(screen.getByTestId("action-reconcile-read")).toBeInTheDocument();
  });

  it("offers retry ONLY for a definitively failed action", async () => {
    server.use(http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execFailed)));
    renderRoute(AT);
    expect(await screen.findByTestId("action-retry")).toBeInTheDocument();
  });

  it("shows the outcome window with result + confidence and the audit trail for an accepted action", async () => {
    server.use(http.get(`${BASE}/actions/execution`, () => HttpResponse.json(execAccepted)));
    renderRoute(AT);

    expect(await screen.findByTestId("outcome-window")).toBeInTheDocument();
    expect(screen.getByTestId("outcome-result")).toHaveTextContent(faIR["outcomeResult.positive"]);
    expect(screen.getByTestId("outcome-confidence")).toHaveTextContent(
      faIR["outcomeConfidence.high"],
    );
    // Audit trail carries the approval-card snapshot (price + versions at execution).
    expect(screen.getByTestId("audit-trail")).toBeInTheDocument();
    expect(screen.getByText(faIR["actions.audit.independentNote"])).toBeInTheDocument();
  });

  it("shows the reassuring empty state without a deep-linked action", async () => {
    renderRoute("/actions");
    expect(await screen.findByText(faIR["state.empty.title"])).toBeInTheDocument();
  });
});
