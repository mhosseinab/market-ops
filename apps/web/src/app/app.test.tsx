import { DEFAULT_LOCALE, formatInteger, toOutputDigits } from "@market-ops/locale";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Providers } from "./Providers";
import { createAppRouter } from "./router";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

function renderApp(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  return render(
    <Providers initialLocale={DEFAULT_LOCALE}>
      <RouterProvider router={router} />
    </Providers>,
  );
}

describe("app shell boots RTL, fa-IR", () => {
  it("drives dir=rtl / lang=fa onto the document from the locale pack", async () => {
    renderApp("/today");
    await waitFor(() => expect(document.documentElement.getAttribute("dir")).toBe("rtl"));
    expect(document.documentElement.getAttribute("lang")).toBe("fa");
  });

  it("renders Persian nav labels from the catalog", async () => {
    renderApp("/today");
    // "امروز" (Today) and "محصولات" (Products) come from the fa-IR catalog.
    await waitFor(() => expect(screen.getAllByText("امروز").length).toBeGreaterThan(0));
    expect(screen.getByText("محصولات")).toBeInTheDocument();
  });

  it("formats numbers in Persian output digits", () => {
    // The formatter (used everywhere numbers render) yields the fa-IR digit
    // family; grouped values use the Persian thousands separator (٬).
    expect(toOutputDigits("1403", "fa-IR")).toBe("۱۴۰۳");
    expect(formatInteger(1234567, "fa-IR")).toBe("۱٬۲۳۴٬۵۶۷");
  });

  it("matches the RTL shell snapshot", async () => {
    const { container } = renderApp("/today");
    await waitFor(() => expect(screen.getAllByText("امروز").length).toBeGreaterThan(0));
    // Today fetches its feed; the default handler is an empty feed, so await the
    // settled no-action state to keep the shell snapshot deterministic.
    await screen.findByTestId("today-no-action");
    expect(container.querySelector(".app-shell")).toMatchSnapshot();
  });
});
