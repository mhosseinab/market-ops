import { faIR } from "@market-ops/locale";
import { screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { sessionInternal } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Operations (internal-role gate / queues)", () => {
  it("gates the queues behind the Internal role — a non-internal principal is refused", async () => {
    renderRoute("/operations"); // default session is Owner
    expect(await screen.findByTestId("operations-internal-only")).toHaveTextContent(
      faIR["operations.internalOnly.title"],
    );
    expect(screen.queryByTestId("operations-queues")).toBeNull();
  });

  it("renders the internal queues with runbook links for an Internal user", async () => {
    server.use(http.get(`${BASE}/auth/me`, () => HttpResponse.json(sessionInternal)));
    renderRoute("/operations");
    expect(await screen.findByTestId("operations-queues")).toBeInTheDocument();
    expect(screen.getAllByTestId("queue-card").length).toBeGreaterThan(0);
    expect(screen.getAllByTestId("runbook-link").length).toBeGreaterThan(0);
  });
});
