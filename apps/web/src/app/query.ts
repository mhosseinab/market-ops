import { createGatewayClient, type GatewayClient } from "@market-ops/gen-ts";
import { QueryClient } from "@tanstack/react-query";

// TanStack Query over the GENERATED gateway client (read-only artifact). The web
// app never recomputes money/policy/approval — it renders what the API returns.
// A shape mismatch is escalated to api_data_contracts, not hand-patched here.

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
});

export const gateway: GatewayClient = createGatewayClient({
  baseUrl: import.meta.env.VITE_GATEWAY_BASE_URL ?? "/api",
  // Late-bind `fetch` instead of capturing it at module load, so a runtime swap
  // of `globalThis.fetch` (e.g. the MSW test server) is honored by the singleton.
  fetch: (input: RequestInfo | URL, init?: RequestInit) => globalThis.fetch(input, init),
});
