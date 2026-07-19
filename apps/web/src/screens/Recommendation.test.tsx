import { DEFAULT_LOCALE, faIR } from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Providers } from "../app/Providers";
import { ApprovalCard } from "../components/ApprovalCard";
import { ContributionBreakdown } from "../components/ContributionBreakdown";
import { StateMachineView } from "../components/StateMachineView";
import type { Contribution } from "../data/types";
import {
  ACCOUNT_ID,
  ACTION_ID,
  approvalCardAwaiting,
  approvalCardV2,
  CARD_ID,
  confirmApproved,
} from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

function wrap(ui: ReactNode): ReactNode {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <Providers
      initialLocale={DEFAULT_LOCALE}
      queryClient={queryClient}
      marketplaceAccountId={ACCOUNT_ID}
    >
      {ui}
    </Providers>
  );
}

// ── ApprovalCard: the only mutation control (§8 free-text containment) ───────
describe("ApprovalCard — structured control only", () => {
  it("confirms only via the structured button, bound to the card's exact binding", () => {
    const onConfirm = vi.fn();
    render(
      wrap(
        <ApprovalCard
          card={approvalCardAwaiting}
          baselineVersion={1}
          onConfirm={onConfirm}
          onRecalculate={() => {}}
        />,
      ),
    );

    // Free text / Enter cannot confirm: firing Enter never activates the control.
    fireEvent.keyDown(document.body, { key: "Enter", code: "Enter" });
    fireEvent.keyDown(screen.getByTestId("approval-card"), { key: "Enter", code: "Enter" });
    expect(onConfirm).not.toHaveBeenCalled();

    // The ONLY path is the structured button, carrying the exact bound versions.
    fireEvent.click(screen.getByTestId("confirm-approval"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onConfirm).toHaveBeenCalledWith(approvalCardAwaiting.binding);
  });

  it("disables the control and offers recalculate when the card version changes (APR-001)", () => {
    const onConfirm = vi.fn();
    const { rerender } = render(
      wrap(
        <ApprovalCard
          card={approvalCardAwaiting}
          baselineVersion={1}
          onConfirm={onConfirm}
          onRecalculate={() => {}}
        />,
      ),
    );
    expect(screen.getByTestId("confirm-approval")).toBeEnabled();

    // A NEW version was minted under the live control → the control is stale.
    rerender(
      wrap(
        <ApprovalCard
          card={approvalCardV2}
          baselineVersion={1}
          onConfirm={onConfirm}
          onRecalculate={() => {}}
        />,
      ) as never,
    );
    expect(screen.getByTestId("confirm-approval")).toBeDisabled();
    expect(screen.getByTestId("stale-card")).toBeInTheDocument();
    expect(screen.getByTestId("recalculate")).toBeInTheDocument();
  });

  it("voids the control the moment the proposed price is edited (APR-001)", () => {
    const onConfirm = vi.fn();
    render(
      wrap(
        <ApprovalCard
          card={approvalCardAwaiting}
          baselineVersion={1}
          onConfirm={onConfirm}
          onRecalculate={() => {}}
        />,
      ),
    );
    fireEvent.click(screen.getByLabelText(faIR["rec.price.increase"]));

    expect(screen.getByTestId("edited-flag")).toBeInTheDocument();
    expect(screen.getByTestId("edited-void")).toBeInTheDocument();
    expect(screen.getByTestId("confirm-approval")).toBeDisabled();
    expect(screen.getByTestId("recalculate")).toBeInTheDocument();
  });
});

// ── StateMachineView: §8.4 stages ────────────────────────────────────────────
describe("StateMachineView — §8.4 stages", () => {
  it("renders the eight revalidation gates while Revalidating", () => {
    render(wrap(<StateMachineView state="revalidating" />));
    expect(screen.getByText(faIR["sm.gates.title"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.identity"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.idempotency"])).toBeInTheDocument();
  });

  it("names the changed dimension when Invalidated", () => {
    render(wrap(<StateMachineView state="invalidated" reason="parameter_version_changed" />));
    expect(screen.getByTestId("invalidated")).toBeInTheDocument();
    expect(screen.getByText(faIR["approvalReason.parameter_version_changed"])).toBeInTheDocument();
  });

  it("renders the Expired branch", () => {
    render(wrap(<StateMachineView state="expired" />));
    expect(screen.getByTestId("expired")).toHaveTextContent(faIR["sm.expired.title"]);
  });

  it("renders permission-denied naming the required role", () => {
    render(wrap(<StateMachineView state="awaiting_confirmation" permissionDenied />));
    expect(screen.getByTestId("permission-denied")).toBeInTheDocument();
    expect(
      screen.getByText(
        faIR["sm.permission.body"].replace("{role}", faIR["sm.permission.role.owner"]),
      ),
    ).toBeInTheDocument();
  });

  it("renders the recommend-only terminal for an Approved card", () => {
    render(wrap(<StateMachineView state="approved" executionPending />));
    expect(screen.getByTestId("recommend-only")).toHaveTextContent(faIR["sm.recommendOnly.title"]);
  });
});

// ── ContributionBreakdown ────────────────────────────────────────────────────
describe("ContributionBreakdown", () => {
  it("renders the deductions, totals, readiness, and rounding rule verbatim", () => {
    const contribution: Contribution = {
      amount: { mantissa: "2000000", currency: "IRR", exponent: 0 },
      netProceeds: { mantissa: "12000000", currency: "IRR", exponent: 0 },
      deductions: [
        {
          component: "commission",
          amount: { mantissa: "1400000", currency: "IRR", exponent: 0 },
          kind: "rate",
          version: 2,
        },
      ],
      readiness: "complete",
      executable: true,
      roundingRule: "round-v1",
    };
    render(wrap(<ContributionBreakdown contribution={contribution} />));
    expect(screen.getByText(faIR["costComponent.commission"])).toBeInTheDocument();
    expect(screen.getByText(faIR["rec.contribution.total"])).toBeInTheDocument();
    expect(screen.getByText(faIR["readiness.complete"])).toBeInTheDocument();
    expect(screen.getByText("round-v1")).toBeInTheDocument();
  });
});

// ── Recommendation screen (integration through the gateway hooks) ─────────────
describe("Recommendation screen", () => {
  it("the confirm payload carries the card's bound version + expiry token (structured control)", async () => {
    let captured: { cardId?: string; binding?: Record<string, unknown> } | undefined;
    server.use(
      http.post(`${BASE}/approvals/confirm`, async ({ request }) => {
        captured = (await request.json()) as typeof captured;
        return HttpResponse.json(confirmApproved);
      }),
    );
    renderRoute(`/recommendation?cardId=${CARD_ID}`);

    fireEvent.click(await screen.findByTestId("confirm-approval"));

    await waitFor(() => expect(captured).toBeTruthy());
    expect(captured?.cardId).toBe(CARD_ID);
    // The bound parameter version + expiry token travel with the confirmation.
    expect(captured?.binding?.parameterVersion).toBe(approvalCardAwaiting.binding.parameterVersion);
    expect(captured?.binding?.expiresAt).toBe(approvalCardAwaiting.binding.expiresAt);
    expect(captured?.binding?.actionId).toBe(ACTION_ID);

    // Recommend-only terminal state becomes visible.
    expect(await screen.findByTestId("recommend-only")).toBeInTheDocument();
  });

  it("surfaces permission-denied from a confirm error without a silent downgrade", async () => {
    server.use(
      http.post(`${BASE}/approvals/confirm`, () =>
        HttpResponse.json(
          { code: "permission_denied", message: "requires owner" },
          { status: 403 },
        ),
      ),
    );
    renderRoute(`/recommendation?cardId=${CARD_ID}`);
    fireEvent.click(await screen.findByTestId("confirm-approval"));
    expect(await screen.findByTestId("permission-denied")).toBeInTheDocument();
  });

  it("presents every PRC-001 field either present or unavailable-with-reason", async () => {
    renderRoute(`/recommendation?cardId=${CARD_ID}`);
    // Present: proposed price + the versioned inputs.
    expect((await screen.findAllByText(faIR["rec.price.proposed"])).length).toBeGreaterThan(0);
    expect(
      screen.getByText(faIR["rec.inputs.parameterVersion"].replace("{version}", "4")),
    ).toBeInTheDocument();
    // Unavailable-with-reason (never blank): objective is not surfaced by the contract.
    const unavailable = faIR["rec.unavailable"].replace(
      "{reason}",
      faIR["rec.unavailable.reason.notSurfaced"],
    );
    expect(screen.getAllByText(unavailable).length).toBeGreaterThan(0);
  });

  it("shows the no-card empty state when no card is selected", async () => {
    renderRoute("/recommendation");
    expect(await screen.findByText(faIR["rec.noCard"])).toBeInTheDocument();
  });
});
