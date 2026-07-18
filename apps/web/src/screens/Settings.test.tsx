import { faIR } from "@market-ops/locale";
import { fireEvent, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { sessionOperator } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Settings (admin levels / L3 Owner-only)", () => {
  it("shows the L3 edit controls, tag, and stricter-only rule for an Owner", async () => {
    renderRoute("/settings"); // default session is Owner
    expect(await screen.findByTestId("l3-edit-controls")).toBeInTheDocument();
    expect(screen.getByTestId("l3-tag")).toHaveTextContent(faIR["settings.level.l3"]);
    expect(screen.getByTestId("stricter-only-note")).toHaveTextContent(
      faIR["settings.guardrails.stricterOnly"],
    );
    expect(screen.getByTestId("l2-tag")).toHaveTextContent(faIR["settings.level.l2"]);
  });

  it("NEVER shows L3 edit controls to an Operator (role-gated render, §8.3)", async () => {
    server.use(http.get(`${BASE}/auth/me`, () => HttpResponse.json(sessionOperator)));
    renderRoute("/settings");

    // The L3 tag + rule are still visible (read-only), but the edit controls are not.
    expect(await screen.findByTestId("l3-tag")).toBeInTheDocument();
    expect(screen.queryByTestId("l3-edit-controls")).toBeNull();
    expect(screen.queryByTestId("l3-edit-maxMovement")).toBeNull();
    expect(screen.getByTestId("l3-owner-only-note")).toBeInTheDocument();
  });

  it("surfaces stricter-only validation: a loosening edit is rejected client-side", async () => {
    renderRoute("/settings");
    const input = await screen.findByTestId("l3-edit-maxMovement");
    // First value becomes the session baseline; a higher (looser) cap is rejected.
    fireEvent.change(input, { target: { value: "5" } });
    fireEvent.change(input, { target: { value: "9" } });
    expect(await screen.findByTestId("stricter-only-error")).toHaveTextContent(
      faIR["settings.guardrails.stricterOnlyError"],
    );
  });
});
