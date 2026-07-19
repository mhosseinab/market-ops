import { DEFAULT_LOCALE, faIR } from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { Providers } from "../app/Providers";
import { ACCOUNT_ID } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { NeedsReview } from "./NeedsReview";

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
});
