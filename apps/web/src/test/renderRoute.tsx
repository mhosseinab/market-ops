import { DEFAULT_LOCALE } from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { act, render } from "@testing-library/react";
import { Providers } from "../app/Providers";
import { createAppRouter } from "../app/router";
import { ACCOUNT_ID } from "./msw/fixtures";

// Renders the FULL app at a given route (router + Providers) with an isolated
// QueryClient (no retries, no cache bleed) and a pinned marketplace account, so a
// screen test exercises routing + data hooks + MSW exactly as production does.
// The returned `navigate` pushes a raw path onto the same history, so a test can
// exercise a mid-session route change (e.g. the chat context binding, CHAT-007).
export function renderRoute(path: string, options?: { accountId?: string }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const history = createMemoryHistory({ initialEntries: [path] });
  const router = createAppRouter(history);
  const result = render(
    <Providers
      initialLocale={DEFAULT_LOCALE}
      queryClient={queryClient}
      marketplaceAccountId={options?.accountId ?? ACCOUNT_ID}
    >
      <RouterProvider router={router} />
    </Providers>,
  );
  const navigate = (to: string) => {
    act(() => {
      history.push(to);
    });
  };
  return { ...result, router, navigate };
}
