import { DEFAULT_LOCALE } from "@market-ops/locale";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { initObservability } from "./app/observability";
import { Providers } from "./app/Providers";
import { router } from "./app/router";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/badges.css";
import "./styles/screens.css";
import "./styles/chat.css";

// Dev-only observability; a no-op in the production bundle (see observability.ts).
void initObservability();

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("root element missing");

createRoot(rootEl).render(
  <StrictMode>
    <Providers initialLocale={DEFAULT_LOCALE}>
      <RouterProvider router={router} />
    </Providers>
  </StrictMode>,
);
