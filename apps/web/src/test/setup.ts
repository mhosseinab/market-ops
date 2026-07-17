import "@testing-library/jest-dom/vitest";

// jsdom does not implement scrollTo; the router calls it during scroll
// restoration. No-op it so test output stays clean.
if (!window.scrollTo) {
  Object.defineProperty(window, "scrollTo", { value: () => {}, writable: true });
} else {
  window.scrollTo = () => {};
}
