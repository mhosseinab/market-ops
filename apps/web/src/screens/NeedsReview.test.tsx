import { DEFAULT_LOCALE, faIR } from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { Providers } from "../app/Providers";
import type { NeedsReviewItem } from "../data/types";
import { ACCOUNT_ID, needsReviewQueue } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { NeedsReview } from "./NeedsReview";

// Candidate A is the seeded queue head; candidate B is a second, distinct
// candidate used to prove notes stay bound to the acted-on identity (#83).
const candidateA = needsReviewQueue.items[0] as NeedsReviewItem;
const candidateB: NeedsReviewItem = {
  identityId: "44444444-4444-4444-4444-444444444444",
  variantId: "22222222-2222-2222-2222-222222222222",
  nativeVariantId: 5560001,
  nativeProductId: 4410002,
  supplierCode: "DKP-5560001",
  variantTitle: "کیبورد بی‌سیم",
  productTitle: "K380",
  candidateSource: "title_match",
  version: 1,
};

/** Serve a two-candidate review queue (A then B). */
function withTwoCandidates() {
  server.use(
    http.get(`${BASE}/identity/needs-review`, () =>
      HttpResponse.json({ items: [candidateA, candidateB] }),
    ),
  );
}

/** The decision button (by catalog label) inside a specific candidate's row. */
function rowButton(supplierCode: string, label: string): HTMLButtonElement {
  const row = screen.getByText(supplierCode).closest("tr") as HTMLElement;
  return within(row).getByText(label).closest("button") as HTMLButtonElement;
}

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

function renderNeedsReview() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <Providers
      initialLocale={DEFAULT_LOCALE}
      queryClient={queryClient}
      marketplaceAccountId={ACCOUNT_ID}
    >
      <NeedsReview />
    </Providers>,
  );
}

describe("Needs Review queue (journey 4)", () => {
  it("renders native IDs in the fa-IR evidence panel as RAW LTR identifiers", async () => {
    renderNeedsReview();

    // Open the pending candidate's evidence panel.
    const skuCell = await screen.findByText("DKP-8842213");
    fireEvent.click(skuCell);

    const aside = document.querySelector(".split__aside") as HTMLElement;
    // Native variant/product IDs: raw, ungrouped, Latin digits (fa-IR default).
    expect(within(aside).getByText("8842213")).toBeInTheDocument();
    expect(within(aside).getByText("7719004")).toBeInTheDocument();
    // Never grouped, never Persian-digit converted.
    expect(within(aside).queryByText("۸٬۸۴۲٬۲۱۳")).toBeNull();
    expect(within(aside).queryByText("8,842,213")).toBeNull();
    // The evidence panel title resolved through the catalog.
    expect(within(aside).getByText(faIR["needsReview.evidence.title"])).toBeInTheDocument();
  });

  it("surfaces a decision failure and PRESERVES the reviewer note (#82)", async () => {
    server.use(
      http.post(`${BASE}/identity/confirm`, () =>
        HttpResponse.json({ code: "CONFLICT", requestId: "req-id" }, { status: 409 }),
      ),
    );
    renderNeedsReview();

    const skuCell = await screen.findByText("DKP-8842213");
    fireEvent.click(skuCell);

    // Type a reviewer note, then confirm (which fails).
    const aside = document.querySelector(".split__aside") as HTMLElement;
    const note = within(aside).getByRole("textbox") as HTMLTextAreaElement;
    fireEvent.change(note, { target: { value: "matches the DK listing" } });
    fireEvent.click(screen.getByText(faIR["needsReview.confirm"]));

    // The failure is surfaced (not silent) with the 409 title + guidance.
    await screen.findByTestId("decision-error");
    expect(screen.getByText(faIR["mutationError.title.conflict"])).toBeInTheDocument();
    expect(screen.getByText(faIR["needsReview.decision.error"])).toBeInTheDocument();

    // Input preserved (acceptance): the note is NOT cleared on failure.
    expect((within(aside).getByRole("textbox") as HTMLTextAreaElement).value).toBe(
      "matches the DK listing",
    );

    // Dismiss clears just this error; the row controls remain the recovery path.
    fireEvent.click(screen.getByTestId("decision-error-dismiss"));
    await waitFor(() => expect(screen.queryByTestId("decision-error")).toBeNull());
  });

  // ── #83: notes stay bound to the acted-on identity ─────────────────────────
  it("prevents candidate A's note from being submitted with candidate B", async () => {
    withTwoCandidates();
    const bodies: { identityId: string; note?: string }[] = [];
    for (const path of ["/identity/confirm", "/identity/reject", "/identity/defer"]) {
      server.use(
        http.post(`${BASE}${path}`, async ({ request }) => {
          bodies.push((await request.json()) as { identityId: string; note?: string });
          return HttpResponse.json({ ok: true });
        }),
      );
    }
    renderNeedsReview();

    // Select candidate A and type a note that belongs to A's evidence.
    fireEvent.click(await screen.findByText(candidateA.supplierCode));
    const aside = document.querySelector(".split__aside") as HTMLElement;
    fireEvent.change(within(aside).getByRole("textbox"), {
      target: { value: "evidence for A" },
    });

    // B is NOT the visible/selected candidate → its decision controls are inert.
    const bConfirm = rowButton(candidateB.supplierCode, faIR["needsReview.confirm"]);
    expect(bConfirm).toBeDisabled();
    // A's own confirm control is the only enabled action target.
    expect(rowButton(candidateA.supplierCode, faIR["needsReview.confirm"])).not.toBeDisabled();

    // Attempting B's confirm must not fire a request carrying A's note (or B's id).
    fireEvent.click(bConfirm);
    expect(bodies).toHaveLength(0);
  });

  it("submits the decision with the VISIBLE evidence panel's identity + its note", async () => {
    withTwoCandidates();
    let body: { identityId: string; note?: string } | null = null;
    server.use(
      http.post(`${BASE}/identity/confirm`, async ({ request }) => {
        body = (await request.json()) as typeof body;
        return HttpResponse.json({ ok: true });
      }),
    );
    renderNeedsReview();

    fireEvent.click(await screen.findByText(candidateA.supplierCode));
    const aside = document.querySelector(".split__aside") as HTMLElement;
    fireEvent.change(within(aside).getByRole("textbox"), {
      target: { value: "evidence for A" },
    });
    fireEvent.click(rowButton(candidateA.supplierCode, faIR["needsReview.confirm"]));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toEqual({ identityId: candidateA.identityId, note: "evidence for A" });
  });

  it("clears the note only after a SUCCESSFUL decision (retained on the visible panel)", async () => {
    renderNeedsReview();

    fireEvent.click(await screen.findByText(candidateA.supplierCode));
    const aside = document.querySelector(".split__aside") as HTMLElement;
    const note = within(aside).getByRole("textbox") as HTMLTextAreaElement;
    fireEvent.change(note, { target: { value: "matches the DK listing" } });
    fireEvent.click(screen.getByText(faIR["needsReview.confirm"]));

    // The default handler confirms successfully → the note is cleared.
    await waitFor(() =>
      expect((within(aside).getByRole("textbox") as HTMLTextAreaElement).value).toBe(""),
    );
  });

  it("disables EVERY decision control while a decision is pending", async () => {
    withTwoCandidates();
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    server.use(
      http.post(`${BASE}/identity/confirm`, async () => {
        await gate;
        return HttpResponse.json({ ok: true });
      }),
    );
    renderNeedsReview();

    fireEvent.click(await screen.findByText(candidateA.supplierCode));
    fireEvent.click(rowButton(candidateA.supplierCode, faIR["needsReview.confirm"]));

    // While A's confirm is in flight, no other decision control is actionable —
    // not on A, not on B (no concurrent confirm/reject/defer).
    await waitFor(() =>
      expect(rowButton(candidateA.supplierCode, faIR["needsReview.reject"])).toBeDisabled(),
    );
    expect(rowButton(candidateA.supplierCode, faIR["needsReview.defer"])).toBeDisabled();
    expect(rowButton(candidateB.supplierCode, faIR["needsReview.confirm"])).toBeDisabled();
    expect(rowButton(candidateB.supplierCode, faIR["needsReview.reject"])).toBeDisabled();

    // Once the decision settles, the selected candidate's controls re-enable.
    release();
    await waitFor(() =>
      expect(rowButton(candidateA.supplierCode, faIR["needsReview.reject"])).not.toBeDisabled(),
    );
  });
});
