import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { formatCount } from "../data/format";
import type { ObservationTarget, ObservedOffer } from "../data/types";
import {
  awaitingActions,
  bulkValid,
  offer,
  RECOMMENDATION_ID,
  readinessComplete,
  selectionPreview,
  target,
  VARIANT_ID,
} from "../test/msw/fixtures";
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

// The bulk CANDIDATE source. GET /actions is shared with the Actions screen, whose
// default handler serves the issue #106 multi-mode queue; this suite needs the
// control-bearing (AwaitingConfirmation) queue that carries the (variantId,
// recommendationId) pairs a selection member is built from, so it installs its own
// handler rather than making a screen-specific queue the global default. A per-test
// `server.use` still overrides this one (MSW resolves the most recently added
// matching handler first).
beforeEach(() => {
  server.use(http.get(`${BASE}/actions`, () => HttpResponse.json(awaitingActions)));
});

function withExecutableCandidate() {
  // Complete readiness + the default Verified offer → an executable candidate.
  server.use(http.get(`${BASE}/cost/readiness`, () => HttpResponse.json(readinessComplete)));
}

describe("Bulk approval (journey 3 — SERVER-minted selection set, APR-001 at set level)", () => {
  it("preview mints the set SERVER-side and the confirmation binds to exactly that lineage + version", async () => {
    withExecutableCandidate();
    // The server's minted identity. If the screen ever submits anything else — a
    // client-generated lineage, a locally counted version — the confirm handler
    // rejects it exactly as a real backend would (unknown lineage → 404), and this
    // test fails.
    const SERVER_LINEAGE = "30000000-0000-0000-0000-000000000003";
    const SERVER_VERSION = 7;
    let previewBody: {
      marketplaceAccountId: string;
      lineageId?: string;
      members: { variantId: string; recommendationId: string }[];
      criteria?: Record<string, string>;
    } | null = null;
    let confirmBody: { selectionSetLineage: string; boundVersion: number } | null = null;
    let previewCalls = 0;

    server.use(
      http.post(`${BASE}/selection-sets/preview`, async ({ request }) => {
        previewCalls += 1;
        previewBody = (await request.json()) as typeof previewBody;
        return HttpResponse.json({
          ...selectionPreview,
          lineageId: SERVER_LINEAGE,
          version: SERVER_VERSION,
        });
      }),
      http.post(`${BASE}/approvals/bulk/confirm`, async ({ request }) => {
        confirmBody = (await request.json()) as typeof confirmBody;
        // A real backend resolves the lineage: an unknown (client-minted) one 404s.
        if (confirmBody?.selectionSetLineage !== SERVER_LINEAGE) {
          return HttpResponse.json(
            { code: "APPROVAL_ERROR", message: "selection set not found" },
            { status: 404 },
          );
        }
        return HttpResponse.json({
          ...bulkValid,
          selectionSetLineage: SERVER_LINEAGE,
          boundVersion: confirmBody.boundVersion,
        });
      }),
    );
    renderRoute("/bulk");

    // Before any preview there is NO server-minted identity, so the structured
    // control is disabled — there is nothing it could bind to.
    const approve = await screen.findByTestId("bulk-approve", undefined, { timeout: 5000 });
    expect(approve).toBeDisabled();

    // Preview POSTs the selected membership and binds to the server's response.
    fireEvent.click(screen.getByTestId("bulk-preview"));
    await waitFor(() => expect(screen.getByTestId("bulk-approve")).not.toBeDisabled(), {
      timeout: 5000,
    });
    expect(previewCalls).toBe(1);
    const sentPreview = previewBody as unknown as {
      members: { variantId: string; recommendationId: string }[];
      lineageId?: string;
    };
    expect(sentPreview.members).toEqual([
      { variantId: VARIANT_ID, recommendationId: RECOMMENDATION_ID },
    ]);
    // The FIRST preview starts a new lineage: the client never proposes one.
    expect(sentPreview.lineageId).toBeUndefined();
    // The rendered selection identity is the SERVER's.
    expect(screen.getByTestId("selection-set")).toHaveTextContent(
      `${SERVER_LINEAGE}·v${SERVER_VERSION}`,
    );

    // Mutate the set (a filter change) → the previewed selection is stale and the
    // control disables until a fresh server preview is taken.
    fireEvent.click(screen.getByText(faIR["readiness.complete"]));
    expect(
      await screen.findByTestId("bulk-invalidated", undefined, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("bulk-approve")).toBeDisabled();

    // A fresh preview REFRESHES the same lineage; the server mints the next version.
    fireEvent.click(screen.getByTestId("bulk-preview"));
    await waitFor(() => expect(screen.getByTestId("bulk-approve")).not.toBeDisabled(), {
      timeout: 5000,
    });
    expect((previewBody as unknown as { lineageId?: string }).lineageId).toBe(SERVER_LINEAGE);

    fireEvent.click(screen.getByTestId("bulk-approve"));
    await screen.findByTestId("bulk-recommend-only", undefined, { timeout: 5000 });

    const sentConfirm = confirmBody as unknown as {
      selectionSetLineage: string;
      boundVersion: number;
    };
    expect(sentConfirm.selectionSetLineage).toBe(SERVER_LINEAGE);
    expect(sentConfirm.boundVersion).toBe(SERVER_VERSION);
  });

  it("renders the SERVER's per-item results, not a client reconstruction", async () => {
    withExecutableCandidate();
    server.use(
      http.post(`${BASE}/approvals/bulk/confirm`, () =>
        HttpResponse.json({
          // A state the SERVER can actually produce (issue #90 fix cycle 2, C6):
          // the only member failed to authorize, so NOTHING is in flight and
          // `executionPending` is false. Pinning `true` beside a single `failed`
          // item exercised an impossible wire state.
          ...bulkValid,
          executionPending: false,
          items: [
            {
              variantId: VARIANT_ID,
              recommendationId: RECOMMENDATION_ID,
              disposition: "executable",
              state: "failed",
              reason: "authorize_failed",
            },
          ],
        }),
      ),
    );
    renderRoute("/bulk");

    fireEvent.click(await screen.findByTestId("bulk-preview", undefined, { timeout: 5000 }));
    await waitFor(() => expect(screen.getByTestId("bulk-approve")).not.toBeDisabled(), {
      timeout: 5000,
    });
    fireEvent.click(screen.getByTestId("bulk-approve"));

    // The row renders the server's `failed` state — the client would have shown a
    // pending/authorized outcome from its own candidate state. The copy is the
    // CANONICAL glossary term, not a bulk-specific duplicate of it (F7).
    const failed = await screen.findByTestId("result-failed", undefined, { timeout: 5000 });
    expect(failed).toHaveTextContent(faIR["state.failed"]);
    expect(screen.queryByTestId("result-authorized")).toBeNull();

    // The post-confirm summary counts the SERVER's authorized items (F8). This
    // confirmation authorized NOTHING, so no aggregate approval note is announced at
    // all — and above all not the overstated "1 approved" the local candidate state
    // would have produced. The per-row `failed` badge above carries the outcome.
    expect(screen.queryByTestId("bulk-recommend-only")).toBeNull();
  });

  it("announces the settled summary when every authorization has finished executing (C4)", async () => {
    // The state M1 made reachable: a resume whose members all reached a terminal
    // external result — items are `already_authorized`, nothing is in flight. The
    // aggregate note must still render (design/STATE_MATRIX.md: every reachable
    // state has a rendering), carrying the SETTLED copy rather than the in-flight one.
    withExecutableCandidate();
    server.use(
      http.post(`${BASE}/approvals/bulk/confirm`, () =>
        HttpResponse.json({
          ...bulkValid,
          executionPending: false,
          items: [
            {
              variantId: VARIANT_ID,
              recommendationId: RECOMMENDATION_ID,
              disposition: "executable",
              state: "already_authorized",
              reason: "already_authorized",
            },
          ],
        }),
      ),
    );
    renderRoute("/bulk");

    fireEvent.click(await screen.findByTestId("bulk-preview", undefined, { timeout: 5000 }));
    await waitFor(() => expect(screen.getByTestId("bulk-approve")).not.toBeDisabled(), {
      timeout: 5000,
    });
    fireEvent.click(screen.getByTestId("bulk-approve"));

    const summary = await screen.findByTestId("bulk-recommend-only", undefined, { timeout: 5000 });
    expect(summary).toHaveTextContent(formatCount(1, "fa-IR"));
    // The settled copy, not the "awaiting external execution" one.
    expect(summary.textContent).toContain(
      faIR["bulk.result.settled"].replace("{count}", formatCount(1, "fa-IR")),
    );
    // Reconciliation invariant (PRD §4.6; design/README.md:181 "unknown write result
    // is NEVER shown as success/failure"; design/README.md:190 the UI "never infers
    // external results"). `executionPending: false` proves only that nothing is in
    // flight — a member may sit in pending_reconciliation with an UNKNOWN result. The
    // summary must therefore not claim external execution finished or succeeded.
    for (const forbidden of ["به پایان رسید", "با موفقیت", "اجرا شد", "انجام شد"]) {
      expect(summary.textContent).not.toContain(forbidden);
    }
  });

  it("counts ONLY the server-authorized members in a mixed partial-failure set (F8/C6)", async () => {
    // A realistic partial failure the server can produce: one member authorized, one
    // failed. The summary announces ONE — never the set size, never the local
    // executable count.
    withExecutableCandidate();
    const OTHER_RECOMMENDATION = "ffffffff-ffff-ffff-ffff-fffffffffff0";
    const OTHER_VARIANT = "11111111-1111-1111-1111-111111111110";
    server.use(
      http.post(`${BASE}/approvals/bulk/confirm`, () =>
        HttpResponse.json({
          ...bulkValid,
          executionPending: true,
          items: [
            {
              variantId: VARIANT_ID,
              recommendationId: RECOMMENDATION_ID,
              disposition: "executable",
              state: "authorized",
              reason: "authorized",
            },
            {
              variantId: OTHER_VARIANT,
              recommendationId: OTHER_RECOMMENDATION,
              disposition: "executable",
              state: "failed",
              reason: "authorize_failed",
            },
          ],
        }),
      ),
    );
    renderRoute("/bulk");

    fireEvent.click(await screen.findByTestId("bulk-preview", undefined, { timeout: 5000 }));
    await waitFor(() => expect(screen.getByTestId("bulk-approve")).not.toBeDisabled(), {
      timeout: 5000,
    });
    fireEvent.click(screen.getByTestId("bulk-approve"));

    const summary = await screen.findByTestId("bulk-recommend-only", undefined, { timeout: 5000 });
    expect(summary).toHaveTextContent(formatCount(1, "fa-IR"));
    expect(summary).not.toHaveTextContent(formatCount(2, "fa-IR"));
    // In-flight ⇒ the awaiting-external copy, not the settled one.
    expect(summary.textContent).toContain(
      faIR["bulk.result.recommendOnly"].replace("{count}", formatCount(1, "fa-IR")),
    );
  });

  it("surfaces the incomplete-candidate notice when more actions exist beyond the page", async () => {
    // The completeness signal the server now returns (issue #90 blocker 3): the
    // candidate set is a PAGE of the queue, so the operator must never read it as
    // the whole queue. Deleting this notice previously broke no test at all.
    withExecutableCandidate();
    server.use(
      http.get(`${BASE}/actions`, () =>
        HttpResponse.json({ ...awaitingActions, hasMore: true, nextCursor: "c1" }),
      ),
    );
    renderRoute("/bulk");

    const notice = await screen.findByTestId("bulk-candidates-incomplete", undefined, {
      timeout: 5000,
    });
    expect(notice).toHaveTextContent(faIR["bulk.candidates.incomplete"]);
  });

  it("does NOT claim the candidate set is incomplete when the queue fits in one page", async () => {
    // The negative half: the default fixture reports hasMore false, so no
    // incompleteness notice may be shown (a permanent notice teaches operators to
    // ignore it).
    withExecutableCandidate();
    renderRoute("/bulk");

    await screen.findByTestId("bulk-toolbar", undefined, { timeout: 5000 });
    expect(screen.queryByTestId("bulk-candidates-incomplete")).toBeNull();
  });

  it("surfaces a failed preview and approves NOTHING (no client-minted fallback identity)", async () => {
    withExecutableCandidate();
    let confirmCalls = 0;
    server.use(
      http.post(`${BASE}/selection-sets/preview`, () =>
        HttpResponse.json(
          { code: "APPROVAL_ERROR", message: "selection set not found" },
          {
            status: 404,
          },
        ),
      ),
      http.post(`${BASE}/approvals/bulk/confirm`, () => {
        confirmCalls += 1;
        return HttpResponse.json(bulkValid);
      }),
    );
    renderRoute("/bulk");

    fireEvent.click(await screen.findByTestId("bulk-preview", undefined, { timeout: 5000 }));

    const error = await screen.findByTestId("bulk-preview-error", undefined, { timeout: 5000 });
    expect(error).toHaveTextContent(faIR["bulk.preview.error.title"]);
    // No server identity ⇒ the approve control stays disabled and nothing is sent.
    expect(screen.getByTestId("bulk-approve")).toBeDisabled();
    fireEvent.click(screen.getByTestId("bulk-approve"));
    expect(confirmCalls).toBe(0);
  });

  it("never force-includes a blocked candidate (no include control, not executable)", async () => {
    // Default readiness is Missing → the candidate is blocked (unique reason text).
    renderRoute("/bulk");
    expect(
      await screen.findByText(faIR["bulk.reason.missingCost"], undefined, { timeout: 5000 }),
    ).toBeInTheDocument();
    // A blocked candidate carries no include control.
    expect(screen.queryByTestId("bulk-include-8842213")).toBeNull();
    // …and with no includable member the empty-membership notice is rendered, so the
    // operator is told WHY nothing can be previewed rather than facing a silent
    // disabled control.
    expect(
      await screen.findByTestId("bulk-preview-empty", undefined, { timeout: 5000 }),
    ).toHaveTextContent(faIR["bulk.preview.empty"]);
    // With zero executable candidates the control stays disabled even after preview.
    fireEvent.click(await screen.findByTestId("bulk-preview", undefined, { timeout: 5000 }));
    expect(screen.getByTestId("bulk-approve")).toBeDisabled();
  });

  it("confirms only through the structured control (free-text containment footnote present)", async () => {
    withExecutableCandidate();
    renderRoute("/bulk");
    const approve = await screen.findByTestId("bulk-approve", undefined, { timeout: 5000 });
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
    await screen.findByTestId("bulk-toolbar", undefined, { timeout: 5000 });
    await waitFor(() => expect(readinessCalls).toBeGreaterThan(0), { timeout: 5000 });
    // Let any in-flight readiness settle, then assert the hard bound holds.
    await waitFor(() => expect(screen.getByTestId("bulk-page-indicator")).toBeInTheDocument(), {
      timeout: 5000,
    });
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
    const sectionError = await screen.findByTestId("bulk-readiness-error", undefined, {
      timeout: 5000,
    });
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
