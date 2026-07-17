import { faIR } from "@market-ops/locale";
import { screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { connectorSupported } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Onboarding / connection (ACC-001, ACC-003)", () => {
  it("NEGATIVE: an Unknown capability never enables the dependent control", async () => {
    // Default handler = disconnected, every capability Unknown.
    renderRoute("/onboarding");

    const syncBtn = await screen.findByTestId("sync-catalog");
    // ACC-001: Unknown must not enable dependent UI.
    expect(syncBtn).toBeDisabled();
    const gate = syncBtn.closest(".capability-gate");
    expect(gate?.getAttribute("data-capability-enabled")).toBe("false");
    // The gated reason is surfaced (not silently hidden).
    expect(screen.getByText(faIR["capability.gatedNote"])).toBeInTheDocument();
  });

  it("enables the dependent control only once the capability is Supported", async () => {
    server.use(http.get(`${BASE}/connector/status`, () => HttpResponse.json(connectorSupported)));
    renderRoute("/onboarding");

    const syncBtn = await screen.findByTestId("sync-catalog");
    await waitFor(() => expect(syncBtn).toBeEnabled());
    expect(syncBtn.closest(".capability-gate")?.getAttribute("data-capability-enabled")).toBe(
      "true",
    );
  });

  it("ACC-003: a disconnected connector shows the recovery banner", async () => {
    renderRoute("/onboarding");
    const banner = await screen.findByText(faIR["connector.disconnected.title"]);
    const container = banner.closest(".banner");
    expect(container).not.toBeNull();
    expect(
      within(container as HTMLElement).getByText(faIR["onboarding.action.reconnect"]),
    ).toBeInTheDocument();
  });
});
