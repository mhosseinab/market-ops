import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { ConnectorStatus } from "../data/types";
import {
  connectorSupported,
  connectorSyncCompleted,
  connectorSyncFailed,
  connectorSyncQueued,
  connectorSyncRunning,
  connectorUnknown,
} from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

// The stepper renders each step as an <li data-state> carrying the step label.
// This reads the "sync catalog" step's state without depending on order.
function syncStepState(): string | null | undefined {
  const label = screen.getByText(faIR["onboarding.step.syncCatalog"], {
    selector: ".stepper__label",
  });
  return label.closest(".stepper__step")?.getAttribute("data-state");
}

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

describe("Onboarding / recovery routing (issue #77, ACC-003)", () => {
  // Disconnect purges the tokens /connector/refresh needs, so the disconnected
  // recovery action must route through the authorization-code CONNECT flow and
  // NEVER call refresh (which would reject with no stored refresh token).
  it("ACC-003: the disconnected banner's recovery routes to the authorization flow, never refresh", async () => {
    let refreshCalls = 0;
    server.use(
      http.post(`${BASE}/connector/refresh`, () => {
        refreshCalls += 1;
        return HttpResponse.json(connectorUnknown);
      }),
    );
    renderRoute("/onboarding");

    const banner = await screen.findByText(faIR["connector.disconnected.title"]);
    const reconnect = within(banner.closest(".banner") as HTMLElement).getByText(
      faIR["onboarding.action.reconnect"],
    );
    fireEvent.click(reconnect);

    // Routed to the connect (authorization-code) control, not the refresh endpoint.
    expect(screen.getByTestId("auth-code-input")).toHaveFocus();
    await new Promise((r) => setTimeout(r, 20));
    expect(refreshCalls).toBe(0);
  });

  it("never-connected: the re-probe (refresh) control is NOT offered without retained credentials", async () => {
    // Default handler = disconnected / never connected: refresh has no token to
    // rotate, so the control that would call it must not be shown.
    renderRoute("/onboarding");
    await screen.findByText(faIR["connector.disconnected.title"]);
    expect(screen.queryByText(faIR["onboarding.action.refresh"])).not.toBeInTheDocument();
  });

  it("re-probe (refresh) is offered ONLY for a connected account with retained credentials", async () => {
    server.use(http.get(`${BASE}/connector/status`, () => HttpResponse.json(connectorSupported)));
    renderRoute("/onboarding");
    expect(await screen.findByText(faIR["onboarding.action.refresh"])).toBeInTheDocument();
  });

  it("reconnect via the authorization code posts CONNECT, never refresh", async () => {
    let connectCalls = 0;
    let refreshCalls = 0;
    server.use(
      http.post(`${BASE}/connector/connect`, () => {
        connectCalls += 1;
        return HttpResponse.json(connectorSupported);
      }),
      http.post(`${BASE}/connector/refresh`, () => {
        refreshCalls += 1;
        return HttpResponse.json(connectorUnknown);
      }),
    );
    renderRoute("/onboarding");

    await screen.findByText(faIR["connector.disconnected.title"]);
    const input = screen.getByTestId("auth-code-input");
    fireEvent.change(input, { target: { value: "auth-xyz" } });
    fireEvent.click(screen.getByText(faIR["onboarding.connect.submit"]));

    await waitFor(() => expect(connectCalls).toBe(1));
    expect(refreshCalls).toBe(0);
  });

  it("recovery failure renders a localized actionable error (connect)", async () => {
    server.use(
      http.post(`${BASE}/connector/connect`, () =>
        HttpResponse.json({ code: "invalid_authorization_code" }, { status: 400 }),
      ),
    );
    renderRoute("/onboarding");

    await screen.findByText(faIR["connector.disconnected.title"]);
    fireEvent.change(screen.getByTestId("auth-code-input"), { target: { value: "bad-code" } });
    fireEvent.click(screen.getByText(faIR["onboarding.connect.submit"]));

    expect(await screen.findByText(faIR["onboarding.connect.error"])).toBeInTheDocument();
  });

  it("recovery failure renders a localized actionable error (re-probe)", async () => {
    server.use(
      http.get(`${BASE}/connector/status`, () => HttpResponse.json(connectorSupported)),
      http.post(`${BASE}/connector/refresh`, () =>
        HttpResponse.json({ code: "refresh_failed" }, { status: 400 }),
      ),
    );
    renderRoute("/onboarding");

    const reprobe = await screen.findByText(faIR["onboarding.action.refresh"]);
    fireEvent.click(reprobe);

    expect(await screen.findByText(faIR["onboarding.refresh.error"])).toBeInTheDocument();
  });
});

describe("Onboarding / catalog sync (issue #76, ACC-004/ACC-005)", () => {
  it("does NOT advance the sync step from capability support alone (no completed run)", async () => {
    // catalog_read Supported, but NO catalogSync run has completed.
    server.use(http.get(`${BASE}/connector/status`, () => HttpResponse.json(connectorSupported)));
    renderRoute("/onboarding");

    await screen.findByTestId("sync-catalog");
    // Durable evidence, not capability: the step is active (connected) but never done.
    await waitFor(() => expect(syncStepState()).toBe("active"));
    expect(syncStepState()).not.toBe("done");
    // The durable state reads "none" until a run exists.
    expect(screen.getByTestId("sync-state")).toHaveTextContent(faIR["onboarding.sync.state.none"]);
  });

  it("advances the sync step to done ONLY on durable completed evidence", async () => {
    server.use(
      http.get(`${BASE}/connector/status`, () => HttpResponse.json(connectorSyncCompleted)),
    );
    renderRoute("/onboarding");

    await screen.findByTestId("sync-catalog");
    await waitFor(() => expect(syncStepState()).toBe("done"));
    expect(screen.getByTestId("sync-state")).toHaveTextContent(
      faIR["onboarding.sync.state.completed"],
    );
  });

  it("clicking Sync issues EXACTLY ONE idempotent sync request", async () => {
    let syncRequests = 0;
    // The durable status reflects the transition: "none" before any sync, then
    // "running" once one has been enqueued (what the post-sync refetch observes).
    server.use(
      http.get(`${BASE}/connector/status`, () =>
        HttpResponse.json(syncRequests === 0 ? connectorSupported : connectorSyncRunning),
      ),
      http.post(`${BASE}/connector/catalog/sync`, () => {
        syncRequests += 1;
        return HttpResponse.json(connectorSyncRunning);
      }),
    );
    renderRoute("/onboarding");

    const syncBtn = await screen.findByTestId("sync-catalog");
    await waitFor(() => expect(syncBtn).toBeEnabled());
    fireEvent.click(syncBtn);

    // The post-sync refetch surfaces the running state; still exactly one POST.
    await waitFor(() =>
      expect(screen.getByTestId("sync-state")).toHaveTextContent(
        faIR["onboarding.sync.state.running"],
      ),
    );
    expect(syncRequests).toBe(1);
  });

  it("renders each durable queued/running/completed/failed state", async () => {
    const cases: Array<[ConnectorStatus, string]> = [
      [connectorSyncQueued, faIR["onboarding.sync.state.queued"]],
      [connectorSyncRunning, faIR["onboarding.sync.state.running"]],
      [connectorSyncCompleted, faIR["onboarding.sync.state.completed"]],
      [connectorSyncFailed, faIR["onboarding.sync.state.failed"]],
    ];
    for (const [status, label] of cases) {
      server.use(http.get(`${BASE}/connector/status`, () => HttpResponse.json(status)));
      const { unmount } = renderRoute("/onboarding");
      await waitFor(() => expect(screen.getByTestId("sync-state")).toHaveTextContent(label));
      unmount();
    }
  });

  it("NEGATIVE: a non-Supported catalog_read issues ZERO sync requests", async () => {
    let syncRequests = 0;
    // Connected but catalog_read Degraded — the gate must keep the control disabled.
    const degraded: ConnectorStatus = {
      ...connectorSupported,
      capabilities: connectorSupported.capabilities.map((c) =>
        c.capability === "catalog_read" ? { ...c, status: "degraded" } : c,
      ),
    };
    server.use(
      http.get(`${BASE}/connector/status`, () => HttpResponse.json(degraded)),
      http.post(`${BASE}/connector/catalog/sync`, () => {
        syncRequests += 1;
        return HttpResponse.json(degraded);
      }),
    );
    renderRoute("/onboarding");

    const syncBtn = await screen.findByTestId("sync-catalog");
    expect(syncBtn).toBeDisabled();
    fireEvent.click(syncBtn);
    // A disabled control never initiates a sync (Unknown/Unsupported/Degraded).
    await new Promise((r) => setTimeout(r, 20));
    expect(syncRequests).toBe(0);
  });
});
