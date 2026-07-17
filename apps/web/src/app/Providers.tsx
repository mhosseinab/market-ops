import type { LocaleId } from "@market-ops/locale";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { AppStateProvider } from "./appState";
import { I18nProvider } from "./i18n";
import { queryClient } from "./query";

// Composed provider stack. Order: Query (data) → AppState (theme/density/chat) →
// i18n (owns dir/lang + telemetry-aware t). Reused by main.tsx and the tests so
// components always render in a faithful runtime.
export function Providers({
  children,
  initialLocale,
}: {
  children: ReactNode;
  initialLocale: LocaleId;
}) {
  return (
    <QueryClientProvider client={queryClient}>
      <AppStateProvider>
        <I18nProvider initialLocale={initialLocale}>{children}</I18nProvider>
      </AppStateProvider>
    </QueryClientProvider>
  );
}
