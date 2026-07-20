import { DEFAULT_LOCALE, faIR } from "@market-ops/locale";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { delay, HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { AuthGateLoading } from "../screens/Login";
import { ACCOUNT_ID, sessionOwner } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";
import { Providers } from "./Providers";

// The browser auth lifecycle (issue #168). The core already exposes POST
// /auth/login, GET /auth/me, and cookie-backed session middleware; these tests
// exercise the SPA seam: the /auth/me gate before any protected query, the login
// screen, the invalid-credentials surface, session expiry, logout, and the
// preserved destination — all against the real router + Providers + MSW.

/** Override /auth/me to fail closed (no valid session). */
function unauthenticated() {
  server.use(
    http.get(`${BASE}/auth/me`, () =>
      HttpResponse.json({ code: "UNAUTHENTICATED", message: "no session" }, { status: 401 }),
    ),
  );
}

/** Fill the login form and submit (default handler returns sessionOwner + cookie). */
async function submitLogin(email = "owner@example.com", password = "correct-horse") {
  const emailInput = await screen.findByTestId("login-email");
  fireEvent.change(emailInput, { target: { value: email } });
  fireEvent.change(screen.getByTestId("login-password"), { target: { value: password } });
  fireEvent.click(screen.getByTestId("login-submit"));
}

afterEach(() => {
  server.events.removeAllListeners();
  window.localStorage.clear();
  window.sessionStorage.clear();
});

describe("auth lifecycle (issue #168)", () => {
  it("fresh browser with no session is routed to login, then reaches the protected screen after login", async () => {
    unauthenticated();
    const { router } = renderRoute("/today");

    // The gate bounces the unauthenticated request to the login screen — the
    // protected Today screen never mounts.
    await screen.findByTestId("login-submit");
    expect(router.state.location.pathname).toBe("/login");
    expect(screen.queryByTestId("today-no-action")).not.toBeInTheDocument();

    await submitLogin();

    // Login primes the session cache, so the destination gate passes and Today
    // renders — without a second /auth/me round trip.
    await waitFor(() => expect(router.state.location.pathname).toBe("/today"));
    await screen.findByTestId("today-no-action");
  });

  it("shows a NON-enumerating error and stays on login when credentials are rejected (401)", async () => {
    server.use(
      http.post(`${BASE}/auth/login`, () =>
        HttpResponse.json({ code: "UNAUTHENTICATED" }, { status: 401 }),
      ),
    );
    const { router } = renderRoute("/login");

    await submitLogin("owner@example.com", "wrong");

    const error = await screen.findByTestId("login-error");
    // The copy names neither field — it never leaks which was wrong (PRD §8).
    expect(error).toHaveTextContent(faIR["auth.login.error.invalidCredentials"]);
    expect(router.state.location.pathname).toBe("/login");
  });

  it("redirects to login when a protected query fails unauthenticated mid-session (expiry)", async () => {
    // The gate passes (valid session), but a protected resource then rejects — the
    // session expired between the gate and the data fetch.
    server.use(
      http.get(`${BASE}/today`, () =>
        HttpResponse.json({ code: "UNAUTHENTICATED" }, { status: 401 }),
      ),
    );
    const { router } = renderRoute("/today");

    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    // The destination is preserved so re-login returns the user where they were.
    expect((router.state.location.search as { redirect?: string }).redirect).toContain("/today");
  });

  it("logout closes the session, clears the protected cache, and returns to login", async () => {
    const { router, queryClient } = renderRoute("/today");
    await screen.findByTestId("today-no-action");
    // The gate cached the resolved principal.
    expect(queryClient.getQueryData(["session"])).toBeTruthy();

    fireEvent.click(screen.getByTestId("logout"));

    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    // No protected/account-scoped data outlives the session — the cache was
    // cleared, so both the principal and the account-scoped Today feed are gone.
    expect(queryClient.getQueryData(["session"])).toBeUndefined();
    expect(queryClient.getQueryData(["today", ACCOUNT_ID])).toBeUndefined();
  });

  it("preserves the intended destination through the login round trip", async () => {
    unauthenticated();
    const { router } = renderRoute("/market");

    await screen.findByTestId("login-submit");
    expect((router.state.location.search as { redirect?: string }).redirect).toContain("/market");

    await submitLogin();

    await waitFor(() => expect(router.state.location.pathname).toBe("/market"));
  });

  it("resolves /auth/me BEFORE issuing any protected request", async () => {
    const order: string[] = [];
    server.events.on("request:start", ({ request }) => {
      order.push(new URL(request.url).pathname);
    });

    renderRoute("/today");
    await screen.findByTestId("today-no-action");

    const meIndex = order.findIndex((p) => p.endsWith("/auth/me"));
    const todayIndex = order.findIndex((p) => p.endsWith("/today"));
    expect(meIndex).toBeGreaterThanOrEqual(0);
    expect(todayIndex).toBeGreaterThan(meIndex);
    // Nothing but the auth check goes out before the session resolves.
    const beforeAuth = order.slice(0, meIndex).filter((p) => !p.endsWith("/auth/me"));
    expect(beforeAuth).toEqual([]);
  });

  it("never persists a token or password to client-readable storage", async () => {
    const { router } = renderRoute("/login");
    await submitLogin("owner@example.com", "super-secret");
    await waitFor(() => expect(router.state.location.pathname).toBe("/today"));

    expect(window.localStorage.length).toBe(0);
    expect(window.sessionStorage.length).toBe(0);
    // The session is an httpOnly cookie — never readable from JS (empty in jsdom).
    expect(document.cookie).toBe("");
  });

  it("shows the submitting state while the login request is in flight (STATE_MATRIX)", async () => {
    server.use(
      http.post(`${BASE}/auth/login`, async () => {
        await delay("infinite");
        return HttpResponse.json(sessionOwner);
      }),
    );
    renderRoute("/login");
    await submitLogin();

    await screen.findByText(faIR["auth.login.submitting"]);
    expect(screen.getByTestId("login-submit")).toBeDisabled();
  });

  it("renders the auth-gate loading state with catalog copy", () => {
    render(
      <Providers initialLocale={DEFAULT_LOCALE}>
        <AuthGateLoading />
      </Providers>,
    );
    expect(screen.getByText(faIR["auth.gate.loading"])).toBeInTheDocument();
  });
});
