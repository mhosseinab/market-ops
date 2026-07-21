import {
  DEFAULT_LOCALE,
  en,
  faIR,
  type LocaleId,
  PSEUDO_CLOSE,
  PSEUDO_OPEN,
} from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Providers } from "../../app/Providers";
import {
  resetUnsupportedValueSink,
  setUnsupportedValueSink,
  type UnsupportedValueReport,
} from "../../app/unsupportedTelemetry";
import { formatInstant } from "../../data/format";
import { ACCOUNT_ID, dailyBriefing } from "../../test/msw/fixtures";
import { BASE } from "../../test/msw/handlers";
import { server } from "../../test/msw/server";
import { BriefingPanel } from "./BriefingPanel";

// Issue #119 (evidence-quality never-cut — provenance): a FAILED briefing fetch
// must never fabricate a "last briefing" date from the REQUESTED business day.
// Error ≠ absence (the #81 pattern): a request date is not observed history, so on
// failure the dock shows an explicit unknown/unavailable provenance state with NO
// date. A successful briefing renders ONLY its authoritative generatedAt.

// The success path renders a deep-link (TanStack <Link>), which needs a router.
function renderPanel(locale: LocaleId = DEFAULT_LOCALE, pseudo = false) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const rootRoute = createRootRoute({
    component: () => (
      <Providers
        initialLocale={locale}
        marketplaceAccountId={ACCOUNT_ID}
        queryClient={queryClient}
        pseudo={pseudo}
      >
        <BriefingPanel />
      </Providers>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return render(<RouterProvider router={router as never} />);
}

// A fixed "today" so the requested businessDay is deterministic and its formatted
// form can be asserted ABSENT from any failure render.
const FIXED_NOW = new Date("2026-07-20T08:30:00Z");
const DIGIT = /[0-9۰-۹]/;

beforeEach(() => {
  // Advance the mocked clock with real time so RTL's async polling still runs,
  // while `new Date()` stays pinned to FIXED_NOW's business day (2026-07-20).
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.setSystemTime(FIXED_NOW);
});

afterEach(() => {
  vi.useRealTimers();
  resetUnsupportedValueSink();
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

const requestedDay = "2026-07-20";

describe("BriefingPanel — #119 no fabricated last-briefing date on failure", () => {
  it("failure with a stored prior briefing shows that briefing's authoritative date", async () => {
    const priorBriefing = {
      ...dailyBriefing,
      businessDay: "2026-07-18",
      generatedAt: "2026-07-18T06:15:00Z",
    };
    server.use(
      http.get(`${BASE}/briefing`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({
          state: "available",
          provenance: "stored_briefing",
          briefing: priorBriefing,
        }),
      ),
    );

    const { container } = renderPanel();

    const failure = await screen.findByTestId("briefing-failure");
    const known = await screen.findByTestId("briefing-failure-known");
    const authoritative = formatInstant(priorBriefing.generatedAt, DEFAULT_LOCALE);
    expect(known.textContent ?? "").toContain(authoritative);
    expect(container.textContent ?? "").not.toContain(
      formatInstant(`${requestedDay}T00:00:00Z`, DEFAULT_LOCALE),
    );
    expect(failure).toContainElement(known);
    expect(screen.queryByTestId("briefing-failure-unknown")).toBeNull();
  });

  it("failure with NO stored prior briefing shows an unknown/unavailable state WITHOUT a date", async () => {
    server.use(
      http.get(`${BASE}/briefing`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({ state: "never_generated", provenance: "none" }),
      ),
    );

    const { container } = renderPanel();

    const failure = await screen.findByTestId("briefing-failure");
    const unknown = await screen.findByTestId("briefing-failure-unknown");
    // The unknown-state copy carries NO date slot: no digits of any family.
    expect(DIGIT.test(unknown.textContent ?? "")).toBe(false);
    // The requested day is never synthesized as provenance.
    const requestedFormatted = formatInstant(`${requestedDay}T00:00:00Z`, DEFAULT_LOCALE);
    expect(container.textContent ?? "").not.toContain(requestedFormatted);
    expect(container.textContent ?? "").not.toContain("2026");
    expect(failure).toContainElement(unknown);
  });

  it.each([
    ["404 not-found", HttpResponse.json({ code: "not_found", message: "none" }, { status: 404 })],
    ["500 server error", HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 })],
    ["network failure", HttpResponse.error()],
  ])("%s cannot synthesize the requested date as history", async (_label, response) => {
    server.use(
      http.get(`${BASE}/briefing`, () => response),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({ state: "never_generated", provenance: "none" }),
      ),
    );

    const { container } = renderPanel();

    await screen.findByTestId("briefing-failure-unknown");
    expect(container.textContent ?? "").not.toContain("2026");
    expect(screen.queryByTestId("briefing-generatedAt")).toBeNull();
  });

  it("a successful current briefing renders only its authoritative generatedAt", async () => {
    server.use(http.get(`${BASE}/briefing`, () => HttpResponse.json(dailyBriefing)));

    renderPanel();

    const generatedAt = await screen.findByTestId("briefing-generatedAt");
    const authoritative = formatInstant(dailyBriefing.generatedAt, DEFAULT_LOCALE);
    expect(generatedAt.textContent ?? "").toContain(authoritative);
    expect(screen.getAllByTestId("briefing-row")).toHaveLength(dailyBriefing.events.length);
    expect(screen.queryByTestId("briefing-failure")).toBeNull();
    expect(screen.queryByTestId("briefing-failure-unknown")).toBeNull();
  });

  it("preserves the unknown-vs-known distinction under Persian (fa-IR)", async () => {
    server.use(
      http.get(`${BASE}/briefing`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({ state: "never_generated", provenance: "none" }),
      ),
    );

    const { container } = renderPanel("fa-IR");

    const unknown = await screen.findByTestId("briefing-failure-unknown");
    // No date in ANY digit family (Latin or Persian) — provenance is not fabricated.
    expect(DIGIT.test(unknown.textContent ?? "")).toBe(false);
    await waitFor(() => expect(document.documentElement.getAttribute("dir")).toBe("rtl"));
    expect(container.textContent ?? "").not.toContain("2026");
  });

  it("resolves the unknown-state copy through the catalog under the pseudo pack (LOC-011)", async () => {
    server.use(
      http.get(`${BASE}/briefing`, () =>
        HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 }),
      ),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({ state: "never_generated", provenance: "none" }),
      ),
    );

    renderPanel(DEFAULT_LOCALE, true);

    const unknown = await screen.findByTestId("briefing-failure-unknown");
    // The new key resolves via the catalog (bracketed) — not a hardcoded literal —
    // and carries NO date, so the unknown-vs-known distinction survives pseudo.
    expect(unknown.textContent ?? "").toContain(PSEUDO_OPEN);
    expect(unknown.textContent ?? "").toContain(PSEUDO_CLOSE);
    expect(DIGIT.test(unknown.textContent ?? "")).toBe(false);
  });

  it("renders the authoritative prior date under Persian RTL", async () => {
    const priorBriefing = {
      ...dailyBriefing,
      businessDay: "2026-07-18",
      generatedAt: "2026-07-18T06:15:00Z",
    };
    server.use(
      http.get(`${BASE}/briefing`, () => HttpResponse.error()),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({
          state: "available",
          provenance: "stored_briefing",
          briefing: priorBriefing,
        }),
      ),
    );

    renderPanel("fa-IR");

    const known = await screen.findByTestId("briefing-failure-known");
    expect(known.textContent ?? "").toContain(formatInstant(priorBriefing.generatedAt, "fa-IR"));
    await waitFor(() => expect(document.documentElement.getAttribute("dir")).toBe("rtl"));
  });

  it("preserves the known prior-date state under the pseudo pack", async () => {
    const priorBriefing = {
      ...dailyBriefing,
      businessDay: "2026-07-18",
      generatedAt: "2026-07-18T06:15:00Z",
    };
    server.use(
      http.get(`${BASE}/briefing`, () => HttpResponse.error()),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({
          state: "available",
          provenance: "stored_briefing",
          briefing: priorBriefing,
        }),
      ),
    );

    renderPanel(DEFAULT_LOCALE, true);

    const known = await screen.findByTestId("briefing-failure-known");
    expect(known.textContent ?? "").toContain(PSEUDO_OPEN);
    expect(known.textContent ?? "").toContain(PSEUDO_CLOSE);
    expect(known.textContent ?? "").toContain(
      formatInstant(priorBriefing.generatedAt, DEFAULT_LOCALE),
    );
    expect(screen.queryByTestId("briefing-failure-unknown")).toBeNull();
  });

  it("fails closed when latest-briefing provenance is malformed", async () => {
    server.use(
      http.get(`${BASE}/briefing`, () => HttpResponse.error()),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({ state: "available", provenance: "stored_briefing" }),
      ),
    );

    const { container } = renderPanel("en");

    const unknown = await screen.findByTestId("briefing-failure-unknown");
    expect(unknown).toHaveAttribute("data-provenance-state", "unavailable");
    expect(container.textContent ?? "").not.toContain("2026");
    expect(screen.getByRole("button", { name: en["action.retry"] })).toBeInTheDocument();
  });

  it.each([
    ["null response", null],
    ["empty object", {}],
    ["null briefing", { state: "available", provenance: "stored_briefing", briefing: null }],
    [
      "invalid business day",
      {
        state: "available",
        provenance: "stored_briefing",
        briefing: { ...dailyBriefing, businessDay: "not-a-date" },
      },
    ],
    [
      "invalid generated timestamp",
      {
        state: "available",
        provenance: "stored_briefing",
        briefing: { ...dailyBriefing, generatedAt: "not-an-instant" },
      },
    ],
    [
      "impossible generated timestamp",
      {
        state: "available",
        provenance: "stored_briefing",
        briefing: { ...dailyBriefing, generatedAt: "2026-02-30T06:15:00Z" },
      },
    ],
    [
      "mismatched marketplace account",
      {
        state: "available",
        provenance: "stored_briefing",
        briefing: {
          ...dailyBriefing,
          marketplaceAccountId: "99999999-9999-4999-8999-999999999999",
        },
      },
    ],
    [
      "same business day as the exclusive bound",
      {
        state: "available",
        provenance: "stored_briefing",
        briefing: { ...dailyBriefing, businessDay: requestedDay },
      },
    ],
    [
      "business day after the exclusive bound",
      {
        state: "available",
        provenance: "stored_briefing",
        briefing: { ...dailyBriefing, businessDay: "2026-07-21" },
      },
    ],
    [
      "available state with none provenance",
      { state: "available", provenance: "none", briefing: dailyBriefing },
    ],
    [
      "never-generated state with stored provenance",
      { state: "never_generated", provenance: "stored_briefing" },
    ],
    [
      "never-generated state carrying a briefing",
      { state: "never_generated", provenance: "none", briefing: dailyBriefing },
    ],
  ])("%s maps malformed latest provenance to lookup unavailable", async (_label, payload) => {
    server.use(
      http.get(`${BASE}/briefing`, () => HttpResponse.error()),
      http.get(`${BASE}/briefing/latest`, () => HttpResponse.json(payload)),
    );

    const { container } = renderPanel("en");

    const unknown = await screen.findByTestId("briefing-failure-unknown");
    expect(unknown).toHaveAttribute("data-provenance-state", "unavailable");
    expect(unknown).toHaveTextContent(en["chat.briefing.failure.lookupUnavailable"]);
    expect(container.textContent ?? "").not.toContain("2026");
    expect(screen.queryByTestId("briefing-failure-known")).toBeNull();
  });

  it("retries the current briefing read without replacing failure with empty data", async () => {
    let attempts = 0;
    server.use(
      http.get(`${BASE}/briefing`, () => {
        attempts += 1;
        return attempts === 1
          ? HttpResponse.json({ code: "internal", message: "boom" }, { status: 500 })
          : HttpResponse.json(dailyBriefing);
      }),
      http.get(`${BASE}/briefing/latest`, () =>
        HttpResponse.json({ state: "never_generated", provenance: "none" }),
      ),
    );

    renderPanel("en");

    await screen.findByTestId("briefing-failure-unknown");
    fireEvent.click(screen.getByRole("button", { name: en["action.retry"] }));
    expect(await screen.findByTestId("briefing-generatedAt")).toHaveTextContent(
      formatInstant(dailyBriefing.generatedAt, "en"),
    );
    expect(attempts).toBe(2);
    expect(screen.queryByTestId("briefing-failure")).toBeNull();
  });
});

// LOC-002 (#121): `BriefingEvent.eventType` is an unconstrained string in the
// contract; the web edge maps it to a CLOSED catalog label. A supported type
// renders its glossary label (never the raw snake_case value); an unknown type
// renders the localized unavailable label + PII-free drift telemetry. Severity
// stays independently catalog-mapped.
describe("BriefingPanel — #121 closed eventType localization", () => {
  it("renders the localized event label + severity (both catalog-mapped), never the raw eventType", async () => {
    server.use(http.get(`${BASE}/briefing`, () => HttpResponse.json(dailyBriefing)));

    renderPanel("en");

    const rows = await screen.findAllByTestId("briefing-eventType");
    // competitor_price → glossary label; winning_state → glossary label.
    expect(rows[0]?.textContent).toBe(en["eventType.competitorOffer"]);
    expect(rows[1]?.textContent).toBe(en["eventType.buyBox"]);
    // The raw machine values never reach the DOM.
    const text = document.body.textContent ?? "";
    expect(text).not.toContain("competitor_price");
    expect(text).not.toContain("winning_state");
    // Severity is independently localized and present alongside the event label.
    const severities = screen.getAllByText((_c, el) => el?.className === "briefing__severity");
    expect(severities).toHaveLength(2);
    expect(severities[0]?.textContent).toBe(en["event.severity.warning"]);
  });

  it("localizes the event label independently of severity under fa-IR", async () => {
    server.use(http.get(`${BASE}/briefing`, () => HttpResponse.json(dailyBriefing)));

    renderPanel("fa-IR");

    const rows = await screen.findAllByTestId("briefing-eventType");
    expect(rows[0]?.textContent).toBe(faIR["eventType.competitorOffer"]);
    const text = document.body.textContent ?? "";
    expect(text).not.toContain("competitor_price");
    expect(text).toContain(faIR["event.severity.warning"]);
  });

  it("an unknown eventType renders the localized unavailable label + telemetry, never the raw value", async () => {
    const reports: UnsupportedValueReport[] = [];
    setUnsupportedValueSink((r) => reports.push(r));
    server.use(
      http.get(`${BASE}/briefing`, () =>
        HttpResponse.json({
          ...dailyBriefing,
          events: [
            {
              rank: 1,
              eventId: dailyBriefing.events[0]?.eventId ?? "",
              eventType: "mystery_signal",
              severity: "info",
            },
          ],
        }),
      ),
    );

    renderPanel("en");

    const row = await screen.findByTestId("briefing-eventType");
    expect(row.textContent).toBe(en["chat.briefing.eventTypeUnknown"]);
    expect(document.body.textContent ?? "").not.toContain("mystery_signal");
    expect(reports).toHaveLength(1);
    expect(reports[0]).toMatchObject({ kind: "briefing_event_type", value: "mystery_signal" });
  });
});
