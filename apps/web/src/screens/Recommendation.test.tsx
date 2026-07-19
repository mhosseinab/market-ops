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
import { renderAmount } from "../data/format";
import type { Contribution, MoneyAmount } from "../data/types";
import {
  ACCOUNT_ID,
  ACTION_ID,
  approvalCardAwaiting,
  approvalCardV2,
  CARD_ID,
  confirmApproved,
  recommendationDetail,
  recommendationDetailBlocked,
} from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

const rial = faIR["unit.rial"];

/** The exact grouped amount MoneyView renders for a payload money value. */
const grouped = (m: MoneyAmount | undefined) =>
  renderAmount(m as MoneyAmount, DEFAULT_LOCALE).amount;

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

  it("shows the no-card empty state when no card is selected", async () => {
    renderRoute("/recommendation");
    expect(await screen.findByText(faIR["rec.noCard"])).toBeInTheDocument();
  });
});

// ── Authoritative PRC-001 binding (issue #89 / getRecommendationDetail) ───────
describe("Recommendation screen — authoritative RecommendationDetail binding", () => {
  it("renders every PRC-001 field from the server detail payload, not a placeholder", async () => {
    renderRoute(`/recommendation?cardId=${CARD_ID}`);

    // Objective from the payload (not "unavailable").
    expect(
      await screen.findByText(faIR["rec.objective.maximize_contribution"]),
    ).toBeInTheDocument();

    // Current price rendered through MoneyView — exact grouped amount + unit key.
    expect(screen.getByTestId("prc-currentPrice")).toHaveTextContent(
      grouped(recommendationDetail.currentPrice),
    );
    expect(screen.getByTestId("prc-currentPrice")).toHaveTextContent(rial);

    // Proposed price + both contributions come from the detail, not the card only.
    expect(screen.getByTestId("prc-proposedPrice")).toHaveTextContent(
      grouped(recommendationDetail.proposedPrice),
    );
    expect(screen.getByTestId("prc-currentContribution")).toHaveTextContent(
      grouped(recommendationDetail.currentContribution),
    );

    // Allowed range bounds, both through MoneyView.
    expect(screen.getByTestId("prc-allowedRange")).toHaveTextContent(
      grouped(recommendationDetail.allowedRange?.min),
    );
    expect(screen.getByTestId("prc-allowedRange")).toHaveTextContent(
      grouped(recommendationDetail.allowedRange?.max),
    );

    // Evidence quality + margin readiness rendered as their canonical badges.
    expect(screen.getByTestId("prc-quality")).toHaveTextContent(faIR["state.verified"]);
    expect(screen.getByTestId("prc-readiness")).toHaveTextContent(faIR["readiness.complete"]);

    // Assumptions rendered verbatim from the payload.
    expect(screen.getByText("commission_rate_stable")).toBeInTheDocument();

    // §9.2 contribution deductions rendered via ContributionBreakdown.
    expect(screen.getByTestId("contribution-breakdown")).toBeInTheDocument();
    expect(screen.getByText(faIR["costComponent.commission"])).toBeInTheDocument();

    // No blockers → the explicit "none" state (never blank).
    expect(screen.getByTestId("prc-blockers")).toHaveTextContent(faIR["rec.blockers.none"]);
  });

  it("renders blockers in the payload's policy order and never reorders them", async () => {
    server.use(
      http.get(`${BASE}/recommendations/detail`, () =>
        HttpResponse.json(recommendationDetailBlocked),
      ),
    );
    renderRoute(`/recommendation?cardId=${CARD_ID}`);

    const list = await screen.findByTestId("rec-blockers");
    const items = list.querySelectorAll("li");
    expect(items).toHaveLength(2);
    // Exact order preserved: boundary_unknown before cost_missing.
    expect(items[0]).toHaveTextContent("boundary_unknown");
    expect(items[1]).toHaveTextContent("cost_missing");
  });

  it("renders optional-absent fields as structured unavailable-with-reason (never blank)", async () => {
    server.use(
      http.get(`${BASE}/recommendations/detail`, () =>
        HttpResponse.json(recommendationDetailBlocked),
      ),
    );
    renderRoute(`/recommendation?cardId=${CARD_ID}`);

    // proposedPrice is absent in this payload → unavailable-with-reason "absent".
    const absent = faIR["rec.unavailable"].replace(
      "{reason}",
      faIR["rec.unavailable.reason.absent"],
    );
    expect((await screen.findAllByText(absent)).length).toBeGreaterThan(0);

    // An UNKNOWN allowed range gets its own boundary reason, not a fabricated value.
    const boundary = faIR["rec.unavailable"].replace(
      "{reason}",
      faIR["rec.unavailable.reason.boundaryUnknown"],
    );
    expect(screen.getByTestId("prc-allowedRange")).toHaveTextContent(boundary);

    // Simulation-only records say so explicitly (never executed).
    expect(screen.getByTestId("prc-simulation")).toHaveTextContent(faIR["rec.simulation.on"]);
  });

  it("degrades to the approval card when the detail read fails (screens-only fallback)", async () => {
    server.use(
      http.get(`${BASE}/recommendations/detail`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
    );
    renderRoute(`/recommendation?cardId=${CARD_ID}`);

    // The authoritative fields degrade with a retry, but the control still works.
    expect(await screen.findByTestId("rec-detail-degraded")).toBeInTheDocument();
    expect(screen.getByTestId("confirm-approval")).toBeInTheDocument();
    expect(screen.getByTestId("rec-detail-retry")).toBeInTheDocument();
  });
});
