import { afterEach, describe, expect, it, vi } from "vitest";

import { gateway } from "../app/query";
import { ACCOUNT_ID } from "./msw/fixtures";

describe("local dev session bootstrap", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("creates a dev session after a 401 and retries the protected request once", async () => {
    const calls: string[] = [];
    let todayAttempts = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = input instanceof Request ? input.url : String(input);
        const parsed = new URL(url, "http://localhost");
        calls.push(parsed.pathname + parsed.search);
        if (url.includes("/api/dev/session")) {
          return new Response(null, { status: 204 });
        }
        todayAttempts += 1;
        if (todayAttempts === 1) {
          return Response.json(
            { code: "UNAUTHENTICATED", message: "authentication required" },
            { status: 401 },
          );
        }
        return Response.json({ items: [] });
      }),
    );

    const result = await gateway.GET("/today", {
      params: { query: { marketplaceAccountId: ACCOUNT_ID } },
    });

    expect(result.response.status).toBe(200);
    expect(calls).toEqual([
      `/api/today?marketplaceAccountId=${ACCOUNT_ID}`,
      "/api/dev/session",
      `/api/today?marketplaceAccountId=${ACCOUNT_ID}`,
    ]);
  });
});
