import { DEFAULT_LOCALE } from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { setupWorker } from "msw/browser";
import { createRoot } from "react-dom/client";
import { Providers } from "../app/Providers";
import { createAppRouter } from "../app/router";
import { handlers } from "../test/msw/handlers";
import "../styles/tokens.css";
import "../styles/base.css";
import "../styles/badges.css";
import "../styles/screens.css";
import "../styles/chat.css";

// Pseudo-locale VISUAL harness entry (LOC-011, issue #15). This boots the REAL
// app shell + routes in a real browser under the pseudo pack, so the Playwright
// gate (tests/pseudo) can measure browser layout — horizontal overflow, clipped
// copy, and forced direction — which jsdom cannot. It never talks to the core:
// MSW's browser worker serves the same fixtures the component suite uses, so the
// harness runs in the fast gate environment (no compose stack), unlike the
// journey e2e smokes. Served only in dev (`vite --mode pseudo`); it is not part
// of the production bundle.

// The target route is taken from the hash so each Playwright navigation reloads
// the shell from scratch (fresh dir/lang + boot), and the app's own browser
// history is never engaged (which would fall through to the production entry).
const route = window.location.hash.replace(/^#/, "") || "/today";

async function boot(): Promise<void> {
  const worker = setupWorker(...handlers);
  await worker.start({
    serviceWorker: { url: "/mockServiceWorker.js" },
    onUnhandledRequest: "bypass",
    quiet: true,
  });

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [route] }));

  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("root element missing");

  createRoot(rootEl).render(
    <Providers initialLocale={DEFAULT_LOCALE} queryClient={queryClient} pseudo>
      <RouterProvider router={router} />
    </Providers>,
  );

  // A ready flag the Playwright gate waits on before measuring layout.
  document.documentElement.dataset.pseudoReady = "1";
}

void boot();
