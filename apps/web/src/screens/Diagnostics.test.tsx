import { faIR } from "@market-ops/locale";
import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { productDiagnostics, VARIANT_ID } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Diagnostics screen (LST-001, read-only)", () => {
  it("renders REAL contract data: title/description/image field + rule provenance with pass/warn and evidence", async () => {
    renderRoute(`/diagnostics?variantId=${VARIANT_ID}`);

    // Field labels for all three diagnostics render (real contract data, not a
    // generic EmptyState scaffold).
    await screen.findByText(faIR["diagnostics.field.title"]);
    expect(screen.getByText(faIR["diagnostics.field.description"])).toBeInTheDocument();
    expect(screen.getByText(faIR["diagnostics.field.image"])).toBeInTheDocument();

    // Rule provenance is the NAMED listing rule id/version (LST-001), rendered raw.
    expect(screen.getByText("listing.title.present")).toBeInTheDocument();
    expect(screen.getByText("listing.description.present")).toBeInTheDocument();
    expect(screen.getByText("listing.image.present")).toBeInTheDocument();

    // Pass and warn results are both visible (title passes; description/image warn).
    expect(screen.getByText(faIR["diagnostics.result.pass"])).toBeInTheDocument();
    expect(screen.getAllByText(faIR["diagnostics.result.warn"]).length).toBeGreaterThanOrEqual(2);

    // Observed metadata distinguishes present-with-length from not_observed.
    expect(screen.getByText(faIR["diagnostics.observed.present"])).toBeInTheDocument();
    expect(screen.getAllByText(faIR["diagnostics.observed.notObserved"]).length).toBe(2);

    // Evidence references are visible (a reference, never listing content).
    expect(screen.getByText("catalog/variant/7719004")).toBeInTheDocument();
    expect(screen.getAllByText("catalog/listing/8842213").length).toBe(2);
  });

  it("exposes NO write/generate/publish control in the report (LST-001 read-only)", async () => {
    renderRoute(`/diagnostics?variantId=${VARIANT_ID}`);
    const field = await screen.findByText(faIR["diagnostics.field.title"]);

    // The diagnostics report itself carries NO actionable control that could
    // generate, publish, or auto-fix content — the list is purely informational.
    // (Shell navigation buttons live outside the report and are not asserted here.)
    const list = field.closest(".diagnostics-list");
    if (!list) throw new Error("no diagnostics list rendered");
    expect(within(list as HTMLElement).queryAllByRole("button")).toHaveLength(0);
    expect(within(list as HTMLElement).queryAllByRole("link")).toHaveLength(0);
    expect(within(list as HTMLElement).queryAllByRole("textbox")).toHaveLength(0);
    // The read-only posture is stated to the user.
    expect(screen.getByText(faIR["diagnostics.readOnlyNote"])).toBeInTheDocument();
  });

  it("shows a DISTINCT empty state for a successful empty report (empty != error)", async () => {
    server.use(
      http.get(`${BASE}/catalog/product-diagnostics`, () =>
        HttpResponse.json({ ...productDiagnostics, items: [] }),
      ),
    );
    renderRoute(`/diagnostics?variantId=${VARIANT_ID}`);

    expect(await screen.findByText(faIR["state.empty.title"])).toBeInTheDocument();
    // Not the error state, and no diagnostic rows.
    expect(screen.queryByText(faIR["state.error.title"])).toBeNull();
    expect(screen.queryByText("listing.title.present")).toBeNull();
  });

  it("shows a DISTINCT transport-error state with retry (error != empty)", async () => {
    server.use(
      http.get(
        `${BASE}/catalog/product-diagnostics`,
        () => new HttpResponse(null, { status: 500 }),
      ),
    );
    renderRoute(`/diagnostics?variantId=${VARIANT_ID}`);

    const err = await screen.findByText(faIR["state.error.title"]);
    expect(err).toBeInTheDocument();
    expect(screen.getByRole("button", { name: faIR["action.retry"] })).toBeInTheDocument();
    // Never the reassuring empty state on a transport failure.
    expect(screen.queryByText(faIR["state.empty.title"])).toBeNull();
  });

  it("prompts to open from a product when no variant is deep-linked (never a fabricated report)", async () => {
    renderRoute("/diagnostics");
    expect(await screen.findByText(faIR["diagnostics.noVariant"])).toBeInTheDocument();
    expect(screen.queryByText("listing.title.present")).toBeNull();
  });
});
