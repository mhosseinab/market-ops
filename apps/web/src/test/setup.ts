import "@testing-library/jest-dom/vitest";
import { afterAll, afterEach, beforeAll } from "vitest";
import { server } from "./msw/server";

// jsdom does not implement scrollTo; the router calls it during scroll
// restoration. No-op it so test output stays clean.
if (!window.scrollTo) {
  Object.defineProperty(window, "scrollTo", { value: () => {}, writable: true });
} else {
  window.scrollTo = () => {};
}

// MSW node server: mock the gateway for every component test. Unhandled requests
// bypass (a screen with no fetch, like the shell snapshot, must not error);
// per-test `server.use(...)` overrides specific endpoints.
beforeAll(() => server.listen({ onUnhandledRequest: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
