import { createGatewayClient, type GatewayClient } from "@market-ops/gen-ts";
import { QueryCache, QueryClient } from "@tanstack/react-query";
import { isUnauthenticated } from "../data/errors";
import { notifyUnauthenticated } from "./authEvents";

// TanStack Query over the GENERATED gateway client (read-only artifact). The web
// app never recomputes money/policy/approval — it renders what the API returns.
// A shape mismatch is escalated to api_data_contracts, not hand-patched here.

// The single global query-error boundary (issue #168): any protected query that
// fails UNAUTHENTICATED (401 — session absent/expired) routes the browser to the
// login screen exactly once (the storm guard in authEvents collapses the burst of
// concurrent 401s a session expiry produces). Mutations are excluded on purpose: a
// login 401 is an EXPECTED invalid-credentials result handled by the login screen,
// and must never redirect away from the login form. Exported so a fresh test
// client is armed with the SAME auth behavior the production singleton has.
export function handleQueryError(error: unknown): void {
  if (isUnauthenticated(error)) notifyUnauthenticated();
}

/** Never auto-retry an auth failure (a 401 won't resolve by retrying and would
 *  amplify the storm); other failures keep one retry. */
export function retryUnlessUnauthenticated(failureCount: number, error: unknown): boolean {
  return isUnauthenticated(error) ? false : failureCount < 1;
}

export const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: handleQueryError }),
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: retryUnlessUnauthenticated,
    },
  },
});

// The single gateway origin+prefix. The chat SSE transport (which openapi-fetch
// cannot decode as a byte stream) reuses this exact base so it hits the same
// cookie-authenticated gateway — the browser never learns the LLM plane exists.
export const GATEWAY_BASE_URL: string = import.meta.env.VITE_GATEWAY_BASE_URL ?? "/api";

let devSessionBootstrap: Promise<boolean> | null = null;

async function ensureDevSession(): Promise<boolean> {
  if (devSessionBootstrap === null) {
    devSessionBootstrap = globalThis
      .fetch(`${GATEWAY_BASE_URL}/dev/session`, {
        method: "POST",
        credentials: "same-origin",
      })
      .then((response) => response.ok)
      .catch(() => false)
      .finally(() => {
        devSessionBootstrap = null;
      });
  }
  return devSessionBootstrap;
}

async function fetchGateway(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const retryInput = input instanceof Request ? input.clone() : input;
  const response = await globalThis.fetch(input, init);
  if (!import.meta.env.DEV || response.status !== 401) return response;
  if (!(await ensureDevSession())) return response;
  return globalThis.fetch(retryInput, init);
}

export const gateway: GatewayClient = createGatewayClient({
  baseUrl: GATEWAY_BASE_URL,
  // Late-bind `fetch` instead of capturing it at module load, so a runtime swap
  // of `globalThis.fetch` (e.g. the MSW test server) is honored by the singleton.
  fetch: fetchGateway,
});
