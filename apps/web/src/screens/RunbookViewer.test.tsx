import { faIR } from "@market-ops/locale";
import { fireEvent, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { RUNBOOKS, runbookTo } from "../app/runbooks";
import { sessionInternal } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

// A stable substring from each backing runbook file's title. Proves the viewer
// serves the INTENDED content (not a 404/empty) for every registry slug.
const EXPECTED_CONTENT: Record<string, string> = {
  "connector-sync": "Connector / sync failure",
  "observation-freshness": "Observation quality",
  "identity-mapping": "Observation quality", // shares observation.md
  "observation-conflict": "Observation quality", // shares observation.md
  "parser-drift": "Route C parser drift",
  reconciliation: "Action reconciliation backlog",
};

function asInternal() {
  server.use(http.get(`${BASE}/auth/me`, () => HttpResponse.json(sessionInternal)));
}

describe("RunbookViewer (OPS-002 runbook deep links)", () => {
  for (const entry of Object.values(RUNBOOKS)) {
    it(`serves runbook content for slug '${entry.slug}' to an Internal user`, async () => {
      asInternal();
      renderRoute(runbookTo(entry.slug));
      const body = await screen.findByTestId("runbook-content");
      expect(body).toHaveTextContent(EXPECTED_CONTENT[entry.slug] as string);
    });
  }

  it("refuses a non-internal principal (same gate as Operations) — no runbook body", async () => {
    renderRoute(runbookTo(RUNBOOKS.failedSync.slug)); // default session is Owner
    expect(await screen.findByTestId("operations-internal-only")).toHaveTextContent(
      faIR["operations.internalOnly.title"],
    );
    expect(screen.queryByTestId("runbook-content")).toBeNull();
  });

  it("shows a not-found EmptyState for an unknown slug", async () => {
    asInternal();
    renderRoute(runbookTo("no-such-runbook"));
    expect(await screen.findByTestId("runbook-not-found")).toBeInTheDocument();
    expect(screen.queryByTestId("runbook-content")).toBeNull();
  });

  it("navigates from an Operations runbook link to the rendered runbook content", async () => {
    asInternal();
    renderRoute("/operations");
    const links = await screen.findAllByTestId("runbook-link");
    // Every link points at the in-SPA viewer route, never a dead /docs/* path.
    for (const link of links) {
      expect(link.getAttribute("href")).toMatch(/^\/operations\/runbooks\//);
      expect(link.getAttribute("href")).not.toMatch(/\/docs\//);
    }
    fireEvent.click(links[0] as Element);
    expect(await screen.findByTestId("runbook-content")).toHaveTextContent(
      EXPECTED_CONTENT["connector-sync"] as string,
    );
  });
});
