import { DEFAULT_LOCALE, type LocaleId } from "@market-ops/locale";
import { QueryCache, QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { act, render } from "@testing-library/react";
import { Providers } from "../app/Providers";
import { handleQueryError } from "../app/query";
import { createAppRouter } from "../app/router";
import {
  type ConversationLocalePreparer,
  ConversationLocalePreparerProvider,
} from "../chat/conversationLocale";
import { ACCOUNT_ID } from "./msw/fixtures";

// Renders the FULL app at a given route (router + Providers) with an isolated
// QueryClient (no retries, no cache bleed) and a pinned marketplace account, so a
// screen test exercises routing + data hooks + MSW exactly as production does.
// The returned `navigate` pushes a raw path onto the same history, so a test can
// exercise a mid-session route change (e.g. the chat context binding, CHAT-007).
export function renderRoute(
  path: string,
  options?: {
    accountId?: string;
    locale?: LocaleId;
    conversationLocalePreparer?: ConversationLocalePreparer;
  },
) {
  const queryClient = new QueryClient({
    // Arm the SAME auth error boundary the production client has (issue #168), so
    // a mid-session 401 redirects to login in tests too; retries stay off for
    // deterministic error-state assertions.
    queryCache: new QueryCache({ onError: handleQueryError }),
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const history = createMemoryHistory({ initialEntries: [path] });
  // The router auth gate resolves the session through this SAME client, so the
  // gate and the screens share one cache entry (issue #168).
  const router = createAppRouter(queryClient, history);
  const app = (
    <Providers
      initialLocale={options?.locale ?? DEFAULT_LOCALE}
      queryClient={queryClient}
      marketplaceAccountId={options?.accountId ?? ACCOUNT_ID}
    >
      <RouterProvider router={router} />
    </Providers>
  );
  const result = render(
    options?.conversationLocalePreparer ? (
      <ConversationLocalePreparerProvider prepare={options.conversationLocalePreparer}>
        {app}
      </ConversationLocalePreparerProvider>
    ) : (
      app
    ),
  );
  const navigate = (to: string) => {
    act(() => {
      history.push(to);
    });
  };
  return { ...result, router, queryClient, navigate };
}
