import { DEFAULT_LOCALE, faIR } from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Providers } from "../app/Providers";
import { ACCOUNT_ID } from "../test/msw/fixtures";
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
});
