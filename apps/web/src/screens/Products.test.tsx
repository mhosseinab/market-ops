import { faIR } from "@market-ops/locale";
import { screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Products workspace", () => {
  it("renders readiness + quality badges and the bulk entry point", async () => {
    renderRoute("/products");

    // Market quality (Verified) from the observed offer.
    expect(await screen.findByText(faIR["state.verified"])).toBeInTheDocument();
    // Margin readiness (Missing) — scoped to the table (it is also a filter chip).
    const table = document.querySelector(".data-table") as HTMLElement;
    expect(within(table).getByText(faIR["readiness.missing"])).toBeInTheDocument();
    // Bulk entry stub deep-links to the (S28) bulk screen.
    expect(screen.getByTestId("bulk-entry")).toBeInTheDocument();
  });
});
