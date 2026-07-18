import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { bulkValid, readinessComplete } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

function withExecutableCandidate() {
  // Complete readiness + the default Verified offer → an executable candidate.
  server.use(http.get(`${BASE}/cost/readiness`, () => HttpResponse.json(readinessComplete)));
}

describe("Bulk approval (journey 3 — versioned selection set, APR-001 at set level)", () => {
  it("preview → mutate set → preview invalidated → re-preview → confirm bound to the set version", async () => {
    withExecutableCandidate();
    let captured: { selectionSetLineage: string; boundVersion: number } | null = null;
    server.use(
      http.post(`${BASE}/approvals/bulk/confirm`, async ({ request }) => {
        captured = (await request.json()) as typeof captured;
        return HttpResponse.json({ ...bulkValid, boundVersion: captured?.boundVersion ?? 0 });
      }),
    );
    renderRoute("/bulk");

    // Before any preview the structured control is DISABLED (no bound version).
    const approve = await screen.findByTestId("bulk-approve");
    expect(approve).toBeDisabled();

    // Preview binds the control to the current selection-set version.
    fireEvent.click(screen.getByTestId("bulk-preview"));
    await waitFor(() => expect(screen.getByTestId("bulk-approve")).not.toBeDisabled());

    // Mutate the set (a filter change mints a new version) → preview INVALIDATED.
    fireEvent.click(screen.getByText(faIR["readiness.complete"]));
    expect(await screen.findByTestId("bulk-invalidated")).toBeInTheDocument();
    expect(screen.getByTestId("bulk-approve")).toBeDisabled();

    // A fresh preview re-binds to the current version; confirm carries THAT version.
    fireEvent.click(screen.getByTestId("bulk-preview"));
    await waitFor(() => expect(screen.getByTestId("bulk-approve")).not.toBeDisabled());
    fireEvent.click(screen.getByTestId("bulk-approve"));

    await screen.findByTestId("bulk-recommend-only");
    expect(captured).not.toBeNull();
    expect((captured as unknown as { boundVersion: number }).boundVersion).toBe(2);
    expect(
      (captured as unknown as { selectionSetLineage: string }).selectionSetLineage,
    ).toBeTruthy();
  });

  it("never force-includes a blocked candidate (no include control, not executable)", async () => {
    // Default readiness is Missing → the candidate is blocked (unique reason text).
    renderRoute("/bulk");
    expect(await screen.findByText(faIR["bulk.reason.missingCost"])).toBeInTheDocument();
    // A blocked candidate carries no include control.
    expect(screen.queryByTestId("bulk-include-8842213")).toBeNull();
    // With zero executable candidates the control stays disabled even after preview.
    fireEvent.click(await screen.findByTestId("bulk-preview"));
    expect(screen.getByTestId("bulk-approve")).toBeDisabled();
  });

  it("confirms only through the structured control (free-text containment footnote present)", async () => {
    withExecutableCandidate();
    renderRoute("/bulk");
    const approve = await screen.findByTestId("bulk-approve");
    expect(approve.tagName).toBe("BUTTON");
    expect(screen.getByTestId("bulk-footnote")).toHaveTextContent(faIR["bulk.footnote"]);
    // No <form> wraps the surface, so Enter cannot submit-confirm a bulk set.
    expect(document.querySelector("form")).toBeNull();
  });
});
