import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { previewClean, previewWithDuplicate, target } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

const CSV = "SKU,COGS\nDKP-8842213,8900000\n";

describe("Cost import — preview before commit (CST-001)", () => {
  it("does not commit before a preview exists; a duplicate conflict blocks commit", async () => {
    const commitSpy = vi.fn();
    server.use(
      http.post(`${BASE}/cost/import/commit`, () => {
        commitSpy();
        return HttpResponse.json({
          batchId: previewWithDuplicate.batchId,
          status: "committed",
          committedRows: 1,
          affectedVariantIds: [target.variantId],
        });
      }),
    );

    renderRoute("/cost");

    // Before any preview there is NO commit control at all.
    const csvInput = await screen.findByTestId("cost-csv");
    expect(screen.queryByTestId("cost-commit")).toBeNull();

    // Provide CSV and request the preview (no commit call is possible yet).
    fireEvent.change(csvInput, { target: { value: CSV } });
    fireEvent.click(screen.getByTestId("cost-preview"));

    // Preview renders with per-row dispositions + stated reasons. "Matched"
    // appears as both a count-card label and a row badge, so match all.
    expect((await screen.findAllByText(faIR["disposition.accept"])).length).toBeGreaterThan(0);
    expect(screen.getByText(faIR["costReason.sku_not_found"])).toBeInTheDocument();
    expect(screen.getByText(faIR["costReason.duplicate_in_file"])).toBeInTheDocument();

    // A duplicate conflict blocks commit (button present but disabled).
    const commitBtn = await screen.findByTestId("cost-commit");
    expect(commitBtn).toBeDisabled();
    fireEvent.click(commitBtn);
    expect(commitSpy).not.toHaveBeenCalled();
  });

  it("commits the previewed batch once the preview is clean", async () => {
    server.use(http.post(`${BASE}/cost/import/preview`, () => HttpResponse.json(previewClean)));
    renderRoute("/cost");

    const csvInput = await screen.findByTestId("cost-csv");
    fireEvent.change(csvInput, { target: { value: CSV } });
    fireEvent.click(screen.getByTestId("cost-preview"));

    const commitBtn = await screen.findByTestId("cost-commit");
    await waitFor(() => expect(commitBtn).toBeEnabled());
    fireEvent.click(commitBtn);

    // Success note confirms the committed row count.
    await screen.findByText(faIR["cost.committed"].replace("{count}", "۱"));
  });

  // Regression for issue #79: a preview binds a commit control to one batch id.
  // If the CSV source changes after a preview, the previously-previewed batch is
  // stale and must not remain committable — CST-001 (preview before commit) plus
  // the §4.6 rule that a stale card never stays clickable while the source no
  // longer matches. Editing the CSV textarea (or picking a new file) invalidates
  // the preview.
  it("invalidates a stale preview when the CSV textarea changes (#79)", async () => {
    server.use(http.post(`${BASE}/cost/import/preview`, () => HttpResponse.json(previewClean)));
    renderRoute("/cost");

    const csvInput = await screen.findByTestId("cost-csv");
    fireEvent.change(csvInput, { target: { value: CSV } });
    fireEvent.click(screen.getByTestId("cost-preview"));

    // A clean preview yields an enabled commit control bound to that batch id.
    const commitBtn = await screen.findByTestId("cost-commit");
    await waitFor(() => expect(commitBtn).toBeEnabled());

    // Editing the CSV source changes what would be committed: the stale preview
    // (and its commit control) must disappear until a fresh preview is run.
    fireEvent.change(csvInput, { target: { value: `${CSV}DKP-9999999,4200000\n` } });

    await waitFor(() => expect(screen.queryByTestId("cost-commit")).toBeNull());
    expect(screen.queryByText(faIR["cost.count.accept"])).toBeNull();
  });

  it("invalidates a stale preview when a new file is chosen (#79)", async () => {
    server.use(http.post(`${BASE}/cost/import/preview`, () => HttpResponse.json(previewClean)));
    renderRoute("/cost");

    const csvInput = await screen.findByTestId("cost-csv");
    fireEvent.change(csvInput, { target: { value: CSV } });
    fireEvent.click(screen.getByTestId("cost-preview"));

    const commitBtn = await screen.findByTestId("cost-commit");
    await waitFor(() => expect(commitBtn).toBeEnabled());

    const file = new File([`${CSV}DKP-9999999,4200000\n`], "costs.csv", { type: "text/csv" });
    fireEvent.change(screen.getByTestId("cost-file"), { target: { files: [file] } });

    await waitFor(() => expect(screen.queryByTestId("cost-commit")).toBeNull());
    expect(screen.queryByText(faIR["cost.count.accept"])).toBeNull();
  });
});
