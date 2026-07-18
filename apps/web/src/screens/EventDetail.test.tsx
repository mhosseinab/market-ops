import { faIR } from "@market-ops/locale";
import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EVENT_ID } from "../test/msw/fixtures";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Event detail (EVT-001 / evidence separation)", () => {
  it("renders the four-way evidence separation with the labeled model-inference panel", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);

    expect(await screen.findByText(faIR["event.evidence.observed"])).toBeInTheDocument();
    expect(screen.getByText(faIR["event.evidence.config"])).toBeInTheDocument();
    expect(screen.getByText(faIR["event.evidence.inference"])).toBeInTheDocument();
    // Inference is explicitly labeled — never presented as an observed fact.
    expect(screen.getByText(faIR["event.evidence.inferenceNote"])).toBeInTheDocument();
  });

  it("shows the versioned materiality threshold and a deep link to the recommendation", async () => {
    renderRoute(`/event?eventId=${EVENT_ID}`);

    // EVT-002: the threshold version that fired the event is shown for reproducibility.
    // Version numbers interpolate raw (technical values), matching the repo
    // convention (see ProductDetail's cost/rule versions).
    expect(
      await screen.findByText(faIR["event.threshold"].replace("{version}", "3")),
    ).toBeInTheDocument();
    expect(screen.getByTestId("event-to-recommendation")).toHaveTextContent(
      faIR["event.cta.recommendation"],
    );
  });

  it("shows the not-found state when no event is selected", async () => {
    renderRoute("/event");
    expect(await screen.findByText(faIR["event.notFound"])).toBeInTheDocument();
  });
});
