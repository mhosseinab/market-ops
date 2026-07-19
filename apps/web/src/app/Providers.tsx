import type { LocaleId } from "@market-ops/locale";
import { type QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { AccountProvider } from "../data/account";
import { AppStateProvider } from "./appState";
import { I18nProvider } from "./i18n";
import { queryClient as defaultQueryClient } from "./query";

// Composed provider stack. Order: Query (data) → Account (active marketplace
// account) → AppState (theme/density/chat) → i18n (owns dir/lang + telemetry-aware
// t). Reused by main.tsx and the tests so components always render in a faithful
// runtime; tests may pin the account via `marketplaceAccountId`.
export function Providers({
  children,
  initialLocale,
  marketplaceAccountId,
  queryClient = defaultQueryClient,
  pseudo = false,
}: {
  children: ReactNode;
  initialLocale: LocaleId;
  marketplaceAccountId?: string;
  queryClient?: QueryClient;
  /** Render the whole stack under the pseudo-locale pack (LOC-011 visual gate). */
  pseudo?: boolean;
}) {
  return (
    <QueryClientProvider client={queryClient}>
      <AccountProvider marketplaceAccountId={marketplaceAccountId}>
        <AppStateProvider>
          <I18nProvider initialLocale={initialLocale} pseudo={pseudo}>
            {children}
          </I18nProvider>
        </AppStateProvider>
      </AccountProvider>
    </QueryClientProvider>
  );
}
