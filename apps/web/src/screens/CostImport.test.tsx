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

  // Acceptance criterion 4 (issue #79): a COMPLETED import followed by a new
  // preview on the SAME source (no textarea edit) must get a FRESH confirmation
  // control. A previously-completed commit is bound to its own batch id; once a
  // new preview mints a new batch, the stale success note must give way to a
  // fresh, enabled commit control (§4.6 — a stale card is never left as the only
  // surface; the new batch stays committable).
  it("issues a fresh commit control when a new preview runs on the same source after a commit (#79)", async () => {
    server.use(http.post(`${BASE}/cost/import/preview`, () => HttpResponse.json(previewClean)));
    renderRoute("/cost");

    const csvInput = await screen.findByTestId("cost-csv");
    fireEvent.change(csvInput, { target: { value: CSV } });
    fireEvent.click(screen.getByTestId("cost-preview"));

    const commitBtn = await screen.findByTestId("cost-commit");
    await waitFor(() => expect(commitBtn).toBeEnabled());
    fireEvent.click(commitBtn);

    // Success note replaces the commit control for the committed batch.
    await screen.findByText(faIR["cost.committed"].replace("{count}", "۱"));
    expect(screen.queryByTestId("cost-commit")).toBeNull();

    // Re-preview the SAME source (no textarea edit): the completed commit must
    // not linger — a fresh enabled commit control renders for the new batch.
    fireEvent.click(screen.getByTestId("cost-preview"));

    const freshCommit = await screen.findByTestId("cost-commit");
    await waitFor(() => expect(freshCommit).toBeEnabled());
    expect(screen.queryByText(faIR["cost.committed"].replace("{count}", "۱"))).toBeNull();
  });

  // Acceptance criterion 3 (issue #79): the preview request body's filename
  // follows the CURRENT source — present after a file pick, cleared once the
  // seller edits the textarea (the source is no longer that file).
  it("carries the filename with the preview after a file pick and clears it after a textarea edit (#79)", async () => {
    const bodies: Array<{ filename?: string }> = [];
    server.use(
      http.post(`${BASE}/cost/import/preview`, async ({ request }) => {
        bodies.push((await request.json()) as { filename?: string });
        return HttpResponse.json(previewClean);
      }),
    );
    renderRoute("/cost");

    await screen.findByTestId("cost-csv");
    const file = new File([CSV], "costs.csv", { type: "text/csv" });
    fireEvent.change(screen.getByTestId("cost-file"), { target: { files: [file] } });

    // The file text populates the textarea; previewing sends its filename.
    await waitFor(() =>
      expect((screen.getByTestId("cost-csv") as HTMLTextAreaElement).value).toBe(CSV),
    );
    fireEvent.click(screen.getByTestId("cost-preview"));
    await screen.findByTestId("cost-commit");
    expect(bodies.at(-1)?.filename).toBe("costs.csv");

    // Editing the textarea drops the filename from the next preview request.
    fireEvent.change(screen.getByTestId("cost-csv"), {
      target: { value: `${CSV}DKP-9999999,4200000\n` },
    });
    fireEvent.click(screen.getByTestId("cost-preview"));
    await waitFor(() => expect(bodies.length).toBe(2));
    expect(bodies.at(-1)?.filename).toBeUndefined();
  });

  // ── issue #82: mutation-error surfaces ─────────────────────────────────────

  it("surfaces a retryable preview error and keeps the entered CSV (#82)", async () => {
    let previewCalls = 0;
    server.use(
      http.post(`${BASE}/cost/import/preview`, () => {
        previewCalls += 1;
        return previewCalls === 1
          ? HttpResponse.json({ code: "PREVIEW_FAILED", requestId: "req-prev" }, { status: 500 })
          : HttpResponse.json(previewClean);
      }),
    );
    renderRoute("/cost");

    const csvInput = await screen.findByTestId("cost-csv");
    fireEvent.change(csvInput, { target: { value: CSV } });
    fireEvent.click(screen.getByTestId("cost-preview"));

    await screen.findByTestId("preview-error");
    expect(screen.getByText(faIR["cost.preview.error"])).toBeInTheDocument();
    // Input preserved: the entered CSV survives the failure.
    expect((screen.getByTestId("cost-csv") as HTMLTextAreaElement).value).toBe(CSV);

    // Preview mutates no state → a direct retry re-runs and now succeeds.
    fireEvent.click(screen.getByTestId("preview-error-retry"));
    const commitBtn = await screen.findByTestId("cost-commit");
    await waitFor(() => expect(commitBtn).toBeEnabled());
  });

  it("does NOT offer a naive retry for an ambiguous commit failure; re-preview is the only path (#82)", async () => {
    server.use(
      http.post(`${BASE}/cost/import/preview`, () => HttpResponse.json(previewClean)),
      http.post(`${BASE}/cost/import/commit`, () =>
        HttpResponse.json({ code: "COMMIT_UNKNOWN", requestId: "req-commit" }, { status: 500 }),
      ),
    );
    renderRoute("/cost");

    const csvInput = await screen.findByTestId("cost-csv");
    fireEvent.change(csvInput, { target: { value: CSV } });
    fireEvent.click(screen.getByTestId("cost-preview"));

    const commitBtn = await screen.findByTestId("cost-commit");
    await waitFor(() => expect(commitBtn).toBeEnabled());
    fireEvent.click(commitBtn);

    // The ambiguous outcome is surfaced with NO retry control and the commit
    // button is withdrawn (acceptance 3 — retry absent until state re-fetched).
    await screen.findByTestId("commit-error");
    expect(screen.getByText(faIR["cost.commit.error"])).toBeInTheDocument();
    expect(screen.queryByTestId("commit-error-retry")).toBeNull();
    expect(screen.queryByTestId("cost-commit")).toBeNull();

    // Dismiss clears the stale preview: the only way back to a commit control is a
    // fresh preview (a re-fetch of current state).
    fireEvent.click(screen.getByTestId("commit-error-dismiss"));
    await waitFor(() => expect(screen.queryByTestId("commit-error")).toBeNull());
    expect(screen.queryByText(faIR["cost.count.accept"])).toBeNull();
  });

  it("surfaces a single-value entry error while preserving the entered value (#82)", async () => {
    server.use(
      http.post(`${BASE}/cost/value`, () =>
        HttpResponse.json({ code: "SKU_NOT_FOUND" }, { status: 400 }),
      ),
    );
    renderRoute("/cost");

    await screen.findByTestId("cost-csv");
    fireEvent.change(screen.getByTestId("single-value"), { target: { value: "8900000" } });
    // The variant field is required to enable submit.
    const variant = screen
      .getByText(faIR["cost.single.variant"])
      .closest("label")
      ?.querySelector("input") as HTMLInputElement;
    fireEvent.change(variant, { target: { value: "v-1" } });
    fireEvent.click(screen.getByTestId("single-submit"));

    await screen.findByTestId("single-error");
    expect(screen.getByText(faIR["cost.single.error"])).toBeInTheDocument();
    // Input preserved: the entered amount survives the failure.
    expect((screen.getByTestId("single-value") as HTMLInputElement).value).toBe("8900000");
  });

  // Acceptance criterion 5 (issue #79): reverting the textarea to the original
  // value does NOT revive a stale committable batch. Any source change clears
  // the preview; a committable control returns only through an explicit preview.
  it("does not revive a stale committable batch when the textarea reverts to the original value (#79)", async () => {
    server.use(http.post(`${BASE}/cost/import/preview`, () => HttpResponse.json(previewClean)));
    renderRoute("/cost");

    const csvInput = await screen.findByTestId("cost-csv");
    fireEvent.change(csvInput, { target: { value: CSV } });
    fireEvent.click(screen.getByTestId("cost-preview"));

    const commitBtn = await screen.findByTestId("cost-commit");
    await waitFor(() => expect(commitBtn).toBeEnabled());
    fireEvent.click(commitBtn);
    await screen.findByText(faIR["cost.committed"].replace("{count}", "۱"));

    // Edit the source, then revert it to the EXACT original value.
    fireEvent.change(csvInput, { target: { value: `${CSV}DKP-9999999,4200000\n` } });
    fireEvent.change(csvInput, { target: { value: CSV } });

    // No preview section and no commit control until an explicit new preview.
    await waitFor(() => expect(screen.queryByTestId("cost-commit")).toBeNull());
    expect(screen.queryByText(faIR["cost.count.accept"])).toBeNull();
  });
});
