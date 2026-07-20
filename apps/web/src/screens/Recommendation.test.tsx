import { DEFAULT_LOCALE, faIR } from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Providers } from "../app/Providers";
import { createAppRouter } from "../app/router";
import { ApprovalCard } from "../components/ApprovalCard";
import { ContributionBreakdown } from "../components/ContributionBreakdown";
import { StateMachineView } from "../components/StateMachineView";
import { renderAmount } from "../data/format";
import type { ApprovalCardView, Contribution, MoneyAmount } from "../data/types";
import {
  ACCOUNT_ID,
  ACTION_ID,
  approvalCardAwaiting,
  approvalCardV2,
  CARD_ID,
  confirmApproved,
  RECOMMENDATION_ID,
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
    // All eight gate labels still render, in order.
    expect(screen.getByText(faIR["sm.gate.identity"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.cost"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.price"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.evidence"])).toBeInTheDocument();
    expect(screen.getByText(faIR["rec.guardrail.floor"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.movement"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.version"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.idempotency"])).toBeInTheDocument();
  });

  it("fails closed: a bare Revalidating renders every gate as pending, never passed", () => {
    // NEGATIVE (primary): with no authoritative per-gate result, the parent
    // lifecycle state must NEVER be inferred as a pass. No ✓ may appear.
    render(wrap(<StateMachineView state="revalidating" />));
    // The pass glyph must not be present anywhere.
    expect(screen.queryByText("✓")).not.toBeInTheDocument();
    // Every one of the eight gates shows a pending accessible status.
    expect(screen.getAllByText(faIR["sm.gate.status.pending"])).toHaveLength(8);
    // Neither passed nor failed accessible status is present.
    expect(screen.queryByText(faIR["sm.gate.status.passed"])).not.toBeInTheDocument();
    expect(screen.queryByText(faIR["sm.gate.status.failed"])).not.toBeInTheDocument();
  });

  it("renders an authoritative FAILED gate as failed while others stay pending", () => {
    render(
      wrap(
        <StateMachineView state="revalidating" gateResults={{ "rec.guardrail.floor": "failed" }} />,
      ),
    );
    expect(screen.getByText(faIR["sm.gate.status.failed"])).toBeInTheDocument();
    // The other seven unresolved gates remain pending; still no pass.
    expect(screen.getAllByText(faIR["sm.gate.status.pending"])).toHaveLength(7);
    expect(screen.queryByText(faIR["sm.gate.status.passed"])).not.toBeInTheDocument();
    expect(screen.queryByText("✓")).not.toBeInTheDocument();
  });

  it("renders PASSED only from an explicit server-provided pass result", () => {
    render(
      wrap(
        <StateMachineView state="revalidating" gateResults={{ "sm.gate.identity": "passed" }} />,
      ),
    );
    // Exactly one gate is passed; the ✓ appears only for it.
    expect(screen.getByText(faIR["sm.gate.status.passed"])).toBeInTheDocument();
    expect(screen.getAllByText("✓")).toHaveLength(1);
    // The remaining seven gates without a result stay pending.
    expect(screen.getAllByText(faIR["sm.gate.status.pending"])).toHaveLength(7);
  });

  it("gives pending, passed, and failed distinct accessible status labels", () => {
    render(
      wrap(
        <StateMachineView
          state="revalidating"
          gateResults={{ "sm.gate.identity": "passed", "sm.gate.cost": "failed" }}
        />,
      ),
    );
    // Queried by accessible text, not by the decorative glyph.
    expect(screen.getByText(faIR["sm.gate.status.passed"])).toBeInTheDocument();
    expect(screen.getByText(faIR["sm.gate.status.failed"])).toBeInTheDocument();
    expect(screen.getAllByText(faIR["sm.gate.status.pending"])).toHaveLength(6);
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

// ── Card-ID state reset (issue #95 / APR-001 approval-versioning adjacency) ────
// The route must NEVER carry card A's edited price, confirmation result, or
// version baseline onto card B. Every approval surface stays bound to the EXACT
// current card; the confirm request keeps the ACTIVE card's full binding.
describe("Recommendation screen — card-ID state reset (issue #95)", () => {
  // A second live card: different id, price, version, and bound parameter version.
  const CARD_B_ID = "abababab-abab-abab-abab-ababababab01";
  const cardB: ApprovalCardView = {
    ...approvalCardAwaiting,
    id: CARD_B_ID,
    version: 3,
    price: { mantissa: "16000000", currency: "IRR", exponent: 0 },
    recommendationId: RECOMMENDATION_ID,
    idempotencyKey: "idem-abababab",
    binding: { ...approvalCardAwaiting.binding, parameterVersion: 9 },
  };

  /** Serve the card that matches the requested cardId (A vs B). */
  function serveCardsById() {
    server.use(
      http.get(`${BASE}/approvals/card`, ({ request }) => {
        const id = new URL(request.url).searchParams.get("cardId");
        return HttpResponse.json(id === CARD_B_ID ? cardB : approvalCardAwaiting);
      }),
    );
  }

  /** Render the real router so the route can be re-navigated between card ids. */
  function renderAt(cardId: string) {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const router = createAppRouter(
      createMemoryHistory({ initialEntries: [`/recommendation?cardId=${cardId}`] }),
    );
    render(
      <Providers
        initialLocale={DEFAULT_LOCALE}
        queryClient={queryClient}
        marketplaceAccountId={ACCOUNT_ID}
      >
        <RouterProvider router={router} />
      </Providers>,
    );
    return router;
  }

  /** Navigate the route to a different card id (search-param only change). The
   *  router's typed `navigate` is cast the same way the app's index redirect is. */
  function goToCard(router: ReturnType<typeof createAppRouter>, cardId: string) {
    return router.navigate({ to: "/recommendation", search: { cardId } } as never);
  }

  const priceB = grouped(cardB.price);
  const priceA = grouped(approvalCardAwaiting.price);

  it("switching from an EDITED card A to card B shows B's exact price and an unedited control", async () => {
    serveCardsById();
    const router = renderAt(CARD_ID);

    // Edit A's proposed price → A's control voids and shows the edited flag.
    fireEvent.click(await screen.findByLabelText(faIR["rec.price.increase"]));
    expect(screen.getByTestId("edited-flag")).toBeInTheDocument();
    expect(screen.getByTestId("confirm-approval")).toBeDisabled();

    await goToCard(router, CARD_B_ID);

    // B renders its OWN authoritative price, with no A edit bleeding through.
    await waitFor(() => expect(screen.getByTestId("approval-card")).toHaveTextContent(priceB));
    expect(screen.getByTestId("approval-card")).not.toHaveTextContent(priceA);
    expect(screen.queryByTestId("edited-flag")).not.toBeInTheDocument();
    expect(screen.getByTestId("confirm-approval")).toBeEnabled();
  });

  it("switching AFTER card A is confirmed does not show A's Approved result on card B", async () => {
    serveCardsById();
    const router = renderAt(CARD_ID);

    // Confirm A → its recommend-only terminal state renders.
    fireEvent.click(await screen.findByTestId("confirm-approval"));
    expect(await screen.findByTestId("recommend-only")).toBeInTheDocument();

    await goToCard(router, CARD_B_ID);

    // B is AwaitingConfirmation — A's Approved terminal must not carry over.
    await waitFor(() => expect(screen.getByTestId("approval-card")).toHaveTextContent(priceB));
    expect(screen.queryByTestId("recommend-only")).not.toBeInTheDocument();
    expect(screen.getByTestId("confirm-approval")).toBeEnabled();
  });

  it("different card versions do NOT share a version baseline (no false stale on B)", async () => {
    serveCardsById();
    const router = renderAt(CARD_ID);
    await screen.findByTestId("approval-card");

    await goToCard(router, CARD_B_ID);

    // B's baseline is B's own version → not stale, control live.
    await waitFor(() => expect(screen.getByTestId("approval-card")).toHaveTextContent(priceB));
    expect(screen.queryByTestId("stale-card")).not.toBeInTheDocument();
    expect(screen.getByTestId("confirm-approval")).toBeEnabled();
  });

  it("IGNORES a late confirmation response for A that resolves after navigating to B", async () => {
    let releaseA: (() => void) | undefined;
    const gate = new Promise<void>((resolve) => {
      releaseA = resolve;
    });
    server.use(
      http.get(`${BASE}/approvals/card`, ({ request }) => {
        const id = new URL(request.url).searchParams.get("cardId");
        return HttpResponse.json(id === CARD_B_ID ? cardB : approvalCardAwaiting);
      }),
      http.post(`${BASE}/approvals/confirm`, async ({ request }) => {
        const body = (await request.json()) as { cardId?: string };
        if (body.cardId === CARD_ID) {
          await gate; // A's confirm stays in-flight until we release it.
          return HttpResponse.json(confirmApproved);
        }
        return HttpResponse.json({ ...confirmApproved, cardId: CARD_B_ID });
      }),
    );
    const router = renderAt(CARD_ID);

    // Fire A's confirm, then navigate to B before A resolves.
    fireEvent.click(await screen.findByTestId("confirm-approval"));
    await goToCard(router, CARD_B_ID);
    await waitFor(() => expect(screen.getByTestId("approval-card")).toHaveTextContent(priceB));

    // Release A's now-stale response: it must NOT render on B.
    releaseA?.();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByTestId("recommend-only")).not.toBeInTheDocument();
    expect(screen.getByTestId("confirm-approval")).toBeEnabled();
  });

  it("the confirm request after navigation carries the ACTIVE card B's full binding", async () => {
    let captured: { cardId?: string; binding?: Record<string, unknown> } | undefined;
    server.use(
      http.get(`${BASE}/approvals/card`, ({ request }) => {
        const id = new URL(request.url).searchParams.get("cardId");
        return HttpResponse.json(id === CARD_B_ID ? cardB : approvalCardAwaiting);
      }),
      http.post(`${BASE}/approvals/confirm`, async ({ request }) => {
        captured = (await request.json()) as typeof captured;
        return HttpResponse.json({ ...confirmApproved, cardId: captured?.cardId });
      }),
    );
    const router = renderAt(CARD_ID);
    await screen.findByTestId("approval-card");

    await goToCard(router, CARD_B_ID);
    await waitFor(() => expect(screen.getByTestId("approval-card")).toHaveTextContent(priceB));

    fireEvent.click(screen.getByTestId("confirm-approval"));

    await waitFor(() => expect(captured).toBeTruthy());
    // The ACTIVE card's id + full server binding travel — never card A's.
    expect(captured?.cardId).toBe(CARD_B_ID);
    expect(captured?.binding?.parameterVersion).toBe(cardB.binding.parameterVersion);
    expect(captured?.binding?.actionId).toBe(ACTION_ID);
    expect(captured?.binding?.expiresAt).toBe(cardB.binding.expiresAt);
  });
});
