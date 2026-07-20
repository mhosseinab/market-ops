import "@testing-library/jest-dom/vitest";
import { configure } from "@testing-library/react";
import { afterAll, afterEach, beforeAll } from "vitest";
import { server } from "./msw/server";

// Testing Library's async utilities (`findBy*`, `waitFor`) default to a 1000ms
// timeout. Under the parallel-project vitest run on a contended CI host, a real
// re-render can legitimately land after that window, red-ing the `ci` gate on a
// correct assertion (issue #332). Raise the default to 5000ms suite-wide so every
// async assertion tolerates CPU contention. This preserves WHAT is asserted — it
// only extends how long a still-true assertion may take to become observable.
configure({ asyncUtilTimeout: 5000 });

// jsdom does not implement scrollTo; the router calls it during scroll
// restoration. No-op it so test output stays clean.
if (!window.scrollTo) {
  Object.defineProperty(window, "scrollTo", { value: () => {}, writable: true });
} else {
  window.scrollTo = () => {};
}

// jsdom Elements implement neither scrollTo nor scrollIntoView; assistant-ui's
// thread-viewport auto-scroll calls them on the message container. No-op both so
// the primitives' scroll effects do not throw under the test DOM.
for (const method of ["scrollTo", "scrollIntoView"] as const) {
  if (!(method in Element.prototype)) {
    Object.defineProperty(Element.prototype, method, { value: () => {}, writable: true });
  }
}

// jsdom implements neither ResizeObserver (assistant-ui's headless Thread/Composer
// primitives observe layout) nor matchMedia — polyfill both as inert stubs so the
// chat-dock primitives mount under the test DOM.
if (!("ResizeObserver" in globalThis)) {
  class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  Object.defineProperty(globalThis, "ResizeObserver", {
    value: ResizeObserverStub,
    writable: true,
  });
}
if (!window.matchMedia) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

// MSW node server: mock the gateway for every component test. Unhandled requests
// bypass (a screen with no fetch, like the shell snapshot, must not error);
// per-test `server.use(...)` overrides specific endpoints.
beforeAll(() => server.listen({ onUnhandledRequest: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
