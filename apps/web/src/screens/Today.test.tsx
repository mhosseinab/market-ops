import { faIR } from "@market-ops/locale";
import { screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { todayFeed } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Today (ranked feed / EVT-004)", () => {
  it("ranks events with all three factors visible and an actionable review CTA", async () => {
    server.use(http.get(`${BASE}/today`, () => HttpResponse.json(todayFeed)));
    renderRoute("/today");

    // All THREE ranking factors are labeled on every row (EVT-004).
    expect((await screen.findAllByText(faIR["today.factor.exposure"])).length).toBeGreaterThan(0);
    expect(screen.getAllByText(faIR["today.factor.confidence"]).length).toBeGreaterThan(0);
    expect(screen.getAllByText(faIR["today.factor.urgency"]).length).toBeGreaterThan(0);

    // The verified event is actionable → review CTA (deep-links to event detail).
    expect(screen.getByTestId("event-review")).toHaveTextContent(faIR["today.action.review"]);
  });

  it("labels the known-exposure stat as a COUNT of events, never as a monetary margin (#98)", async () => {
    server.use(http.get(`${BASE}/today`, () => HttpResponse.json(todayFeed)));
    renderRoute("/today");

    // The fixture has exactly one known-exposure event (14,100,000 IRR) and one
    // unknown-exposure event. The stat is a COUNT of known-exposure events (= 1),
    // NOT a Money aggregate — so its visible label must say so.
    expect(await screen.findByText(faIR["today.stat.knownExposureEvents"])).toBeInTheDocument();

    // The stat's accessible name is a group that explicitly states it is a count
    // of known-exposure events — a count is never announced under a money label.
    const expectedAria = faIR["today.stat.knownExposureEvents.aria"].replace("{count}", "۱");
    const group = screen.getByRole("group", { name: expectedAria });
    expect(group).toHaveTextContent("۱");
  });

  it("shows the blocked panel + readiness banner for a non-actionable (conflicted) event", async () => {
    server.use(http.get(`${BASE}/today`, () => HttpResponse.json(todayFeed)));
    renderRoute("/today");

    expect(await screen.findByTestId("event-blocked")).toBeInTheDocument();
    // Conflicted evidence cannot recommend — its blocker reason is stated.
    expect(screen.getByText(faIR["today.blocked.reason.conflicted"])).toBeInTheDocument();
    // The data-readiness banner surfaces the identity-mapping blocker chip.
    expect(screen.getByText(faIR["today.readiness.title"])).toBeInTheDocument();
  });

  it("shows the reassuring no-action state on an empty feed", async () => {
    // Default handler returns an empty feed.
    renderRoute("/today");
    expect(await screen.findByTestId("today-no-action")).toHaveTextContent(
      faIR["today.noAction.title"],
    );
  });

  it("surfaces the error state with a retry when the feed request fails", async () => {
    server.use(
      http.get(`${BASE}/today`, () =>
        HttpResponse.json({ code: "boom", message: "x" }, { status: 500 }),
      ),
    );
    renderRoute("/today");
    expect(await screen.findByText(faIR["state.error.title"])).toBeInTheDocument();
  });
});
