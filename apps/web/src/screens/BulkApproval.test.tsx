import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { ObservationTarget, ObservedOffer } from "../data/types";
import { bulkValid, offer, readinessComplete, target } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";
import { BULK_READINESS_PAGE_SIZE } from "./BulkApproval";

/** N observation targets with distinct ids and native identifiers. */
function makeTargets(n: number): ObservationTarget[] {
  return Array.from({ length: n }, (_, i) => ({
    ...target,
    id: `t-${i}`,
    variantId: `00000000-0000-0000-0000-${String(i).padStart(12, "0")}`,
    nativeVariantId: 6000000 + i,
    nativeProductId: 7000000 + i,
  }));
}

/** One Verified offer per target so quality never blocks the classification. */
function offersFor(targets: ObservationTarget[]): ObservedOffer[] {
  return targets.map((tg, i) => ({
    ...offer,
    id: `o-${i}`,
    targetId: tg.id,
    nativeVariantId: tg.nativeVariantId,
    offerIdentity: `${tg.nativeVariantId}:seller-1`,
  }));
}

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

  it("does not hide a conflicted sibling behind a verified offer on the same target (OBS-004)", async () => {
    // ONE target, TWO offer identities: Verified + Conflicted. With Complete
    // readiness the verified offer classifies Executable, but the conflicted
    // sibling must NOT be hidden — it is its OWN blocked row, never executable.
    withExecutableCandidate();
    const verified: ObservedOffer = {
      ...offer,
      id: "o-verified",
      offerIdentity: "8842213:seller-1",
      quality: "verified",
    };
    const conflicted: ObservedOffer = {
      ...offer,
      id: "o-conflicted",
      offerIdentity: "8842213:seller-2",
      quality: "conflicted",
    };
    server.use(
      http.get(`${BASE}/observation/observed-offers`, () =>
        HttpResponse.json({ items: [conflicted, verified] }),
      ),
    );
    renderRoute("/bulk");

    const table = (await screen.findByText(faIR["bulk.table.title"])).closest(
      ".panel",
    ) as HTMLElement;
    const dataTable = table.querySelector(".data-table") as HTMLElement;
    // Both dispositions coexist: the verified offer is executable, the conflicted
    // sibling is blocked with its own reason — neither stands in for the other.
    expect(within(dataTable).getByText(faIR["bulk.status.executable"])).toBeInTheDocument();
    expect(within(dataTable).getByText(faIR["bulk.status.blocked"])).toBeInTheDocument();
    expect(within(dataTable).getByText(faIR["bulk.reason.conflicted"])).toBeInTheDocument();
    // The conflicted offer carries no include control (never force-executable);
    // the verified sibling does.
    expect(screen.queryByTestId("bulk-include-8842213:seller-2")).toBeNull();
    expect(screen.getByTestId("bulk-include-8842213:seller-1")).toBeInTheDocument();
  });

  it("bounds the readiness fan-out to one page regardless of target count (§17.2, #245)", async () => {
    const targets = makeTargets(BULK_READINESS_PAGE_SIZE + 6);
    let readinessCalls = 0;
    server.use(
      http.get(`${BASE}/observation/targets`, () => HttpResponse.json({ items: targets })),
      http.get(`${BASE}/observation/observed-offers`, () =>
        HttpResponse.json({ items: offersFor(targets) }),
      ),
      http.get(`${BASE}/cost/readiness`, ({ request }) => {
        readinessCalls += 1;
        const variantId = new URL(request.url).searchParams.get("variantId") ?? "";
        return HttpResponse.json({ ...readinessComplete, variantId });
      }),
    );
    renderRoute("/bulk");

    // The toolbar renders once targets resolve; readiness then fans out — but only
    // for the current page, never all 31 targets.
    await screen.findByTestId("bulk-toolbar");
    await waitFor(() => expect(readinessCalls).toBeGreaterThan(0));
    // Let any in-flight readiness settle, then assert the hard bound holds.
    await waitFor(() => expect(screen.getByTestId("bulk-page-indicator")).toBeInTheDocument());
    expect(readinessCalls).toBeLessThanOrEqual(BULK_READINESS_PAGE_SIZE);
    expect(readinessCalls).toBeLessThan(targets.length);
    // The next page is reachable (more targets exist beyond this page).
    expect(screen.getByTestId("bulk-next-page")).not.toBeDisabled();
    expect(screen.getByTestId("bulk-prev-page")).toBeDisabled();
  });

  it("degrades on partial readiness failure: keeps rows, scoped retry, no fabricated verdict (#81/#245)", async () => {
    const targets = makeTargets(2);
    const [good, bad] = [targets[1], targets[0]] as [ObservationTarget, ObservationTarget];
    server.use(
      http.get(`${BASE}/observation/targets`, () => HttpResponse.json({ items: targets })),
      http.get(`${BASE}/observation/observed-offers`, () =>
        HttpResponse.json({ items: offersFor(targets) }),
      ),
      http.get(`${BASE}/cost/readiness`, ({ request }) => {
        const variantId = new URL(request.url).searchParams.get("variantId") ?? "";
        // The FIRST target's readiness fails; the SECOND resolves Complete.
        if (variantId === bad.variantId) return new HttpResponse(null, { status: 500 });
        return HttpResponse.json({ ...readinessComplete, variantId });
      }),
    );
    renderRoute("/bulk");

    // The scoped section error appears with an actionable retry.
    const sectionError = await screen.findByTestId("bulk-readiness-error");
    expect(sectionError).toHaveTextContent(faIR["bulk.readiness.error.title"]);
    expect(sectionError.querySelector("button")).not.toBeNull();

    // BOTH rows still render — the failed row is not dropped.
    expect(screen.getByText(String(good.nativeVariantId))).toBeInTheDocument();
    expect(screen.getByText(String(bad.nativeVariantId))).toBeInTheDocument();

    // The successful row classifies as Executable (scoped to the table — the
    // toolbar stat card shares the same glossary word).
    const table = document.querySelector(".data-table") as HTMLElement;
    expect(within(table).getByText(faIR["bulk.status.executable"])).toBeInTheDocument();

    // The FAILED row is NOT fabricated into a "missing cost" blocked verdict —
    // error is not absence. No missing-cost reason is surfaced IN THE TABLE
    // (scoped: the "Missing" readiness filter chip shares the glossary phrase).
    expect(within(table).queryByText(faIR["bulk.reason.missingCost"])).toBeNull();
    // The failed row carries no include control (an unknown verdict is never executable).
    expect(screen.queryByTestId(`bulk-include-${bad.nativeVariantId}`)).toBeNull();
  });
});
