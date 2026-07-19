import { faIR } from "@market-ops/locale";
import { screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EVENT_ID } from "../test/msw/fixtures";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

// The threshold copy the fixture (thresholdVersion=3) renders.
const THRESHOLD_3 = faIR["event.threshold"].replace("{version}", "3");
// The deterministic ranking rule — never model inference (issue #97).
const RANKING_RULE = faIR["today.rationale.body"];

function panel(kind: "observed" | "dk" | "config" | "inference"): HTMLElement {
  const el = document.querySelector<HTMLElement>(`[data-kind="${kind}"]`);
  if (!el) throw new Error(`evidence panel [data-kind="${kind}"] not found`);
  return el;
}

describe("Event detail (EVT-001 / evidence separation)", () => {
  it("renders the four-way evidence separation with the labeled model-inference panel", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);

    expect(await screen.findByText(faIR["event.evidence.observed"])).toBeInTheDocument();
    expect(screen.getByText(faIR["event.evidence.config"])).toBeInTheDocument();
    expect(screen.getByText(faIR["event.evidence.inference"])).toBeInTheDocument();
    // Inference is explicitly labeled — never presented as an observed fact.
    expect(screen.getByText(faIR["event.evidence.inferenceNote"])).toBeInTheDocument();
  });

  it("shows the versioned materiality threshold only under seller configuration", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);
    // EVT-002: the threshold version that fired the event is shown for reproducibility,
    // but as governing seller configuration — not a marketplace signal (issue #97).
    await screen.findByText(faIR["event.evidence.observed"]);

    expect(within(panel("config")).getByText(THRESHOLD_3)).toBeInTheDocument();
    // A materiality threshold is configuration, never a DK signal or observed fact.
    expect(within(panel("dk")).queryByText(THRESHOLD_3)).not.toBeInTheDocument();
    expect(within(panel("observed")).queryByText(THRESHOLD_3)).not.toBeInTheDocument();
  });

  it("renders DK/marketplace evidence only from the DK-sourced reference", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);
    await screen.findByText(faIR["event.evidence.observed"]);

    // The Route C evidence reference is a DK-sourced signal — it belongs to the DK
    // panel, never the observed-facts panel (issue #97).
    expect(within(panel("dk")).getByText("obs:route_c:8842213")).toBeInTheDocument();
    expect(within(panel("observed")).queryByText("obs:route_c:8842213")).not.toBeInTheDocument();
  });

  it("renders the observed fact from the observed event condition", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);
    await screen.findByText(faIR["event.evidence.observed"]);

    // The observed condition (event type) is the authoritative observed fact — not a
    // provenance label alone.
    expect(
      within(panel("observed")).getByText(faIR["eventType.competitorOffer"]),
    ).toBeInTheDocument();
  });

  it("never renders model-inference content when the response carries no inference", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);
    await screen.findByText(faIR["event.evidence.observed"]);

    // The MarketEvent contract has no inference field — the panel stays explicitly
    // unavailable, with no fabricated filler.
    expect(within(panel("inference")).getByText(faIR["common.notAvailable"])).toBeInTheDocument();
    // The deterministic ranking rule must NOT be presented as model inference.
    expect(within(panel("inference")).queryByText(RANKING_RULE)).not.toBeInTheDocument();
  });

  it("labels the deterministic ranking rule as ranking logic, not inference", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);
    await screen.findByText(faIR["event.evidence.observed"]);

    // The ranking rationale lives with the ranking factors, labeled "why this priority".
    expect(screen.getByText(faIR["today.rationale"])).toBeInTheDocument();
    expect(screen.getByText(RANKING_RULE)).toBeInTheDocument();
  });

  it("keeps the deep link to the recommendation", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);
    expect(await screen.findByTestId("event-to-recommendation")).toHaveTextContent(
      faIR["event.cta.recommendation"],
    );
  });

  it("shows the not-found state when no event is selected", async () => {
    renderRoute("/event");
    expect(await screen.findByText(faIR["event.notFound"])).toBeInTheDocument();
  });
});
