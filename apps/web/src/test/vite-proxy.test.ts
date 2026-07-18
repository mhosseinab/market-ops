import { afterEach, describe, expect, it, vi } from "vitest";

import viteConfig from "../../vite.config";

describe("local Vite gateway proxy", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    delete process.env.MARKET_OPS_DEV_OWNER_EMAIL;
    delete process.env.MARKET_OPS_DEV_OWNER_PASSWORD;
  });

  it("keeps browser API requests same-origin and strips the /api prefix", () => {
    const config = viteConfig as {
      server?: {
        proxy?: Record<string, { target?: string; rewrite?: (path: string) => string }>;
      };
    };
    const apiProxy = config.server?.proxy?.["/api"];

    expect(apiProxy?.target).toBe("http://127.0.0.1:8080");
    expect(apiProxy?.rewrite?.("/api/healthz")).toBe("/healthz");
  });

  it("logs in server-side and forwards the dev session cookie", async () => {
    const plugins = (viteConfig as { plugins?: unknown[] }).plugins ?? [];
    const plugin = plugins.flat().find((candidate) => {
      return (candidate as { name?: string } | null)?.name === "market-ops-dev-session";
    }) as
      | {
          configureServer?: (server: {
            middlewares: { use: (handler: DevMiddleware) => void };
          }) => void;
        }
      | undefined;
    expect(plugin).toBeDefined();

    let middleware: DevMiddleware | undefined;
    plugin?.configureServer?.({
      middlewares: {
        use: (handler) => {
          middleware = handler;
        },
      },
    });
    expect(middleware).toBeDefined();

    process.env.MARKET_OPS_DEV_OWNER_EMAIL = "owner@dev.local";
    process.env.MARKET_OPS_DEV_OWNER_PASSWORD = "local-password";
    const upstreamFetch = vi.fn(async () => {
      return new Response(JSON.stringify({ role: "owner" }), {
        status: 200,
        headers: {
          "content-type": "application/json",
          "set-cookie": "market_ops_session=session-id; Path=/; HttpOnly; SameSite=Lax",
        },
      });
    });
    vi.stubGlobal("fetch", upstreamFetch);

    const headers = new Map<string, string>();
    const response = {
      statusCode: 0,
      setHeader: (name: string, value: string) => headers.set(name.toLowerCase(), value),
      end: vi.fn(),
    };
    const next = vi.fn();
    await middleware?.({ method: "POST", url: "/api/dev/session" }, response, next);

    expect(next).not.toHaveBeenCalled();
    expect(upstreamFetch).toHaveBeenCalledWith(
      "http://127.0.0.1:8080/auth/login",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ email: "owner@dev.local", password: "local-password" }),
      }),
    );
    expect(response.statusCode).toBe(200);
    expect(headers.get("set-cookie")).toContain("market_ops_session=session-id");
  });
});

type DevMiddleware = (
  request: { method?: string; url?: string },
  response: {
    statusCode: number;
    setHeader: (name: string, value: string) => void;
    end: (body?: string) => void;
  },
  next: () => void,
) => void | Promise<void>;
