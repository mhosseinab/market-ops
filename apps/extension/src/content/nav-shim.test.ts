import { beforeEach, describe, expect, it, vi } from "vitest";

// The nav shim patches the GLOBAL history object as a side effect of being
// loaded (it runs in the MAIN world with no exports) — so this test imports it
// fresh per case and asserts on the resulting behavior, never on its internals.
// history.pushState/replaceState are restored to PRISTINE natives before every
// case so wrapping from a previous test can never stack onto the next one.
const NATIVE_PUSH = history.pushState.bind(history);
const NATIVE_REPLACE = history.replaceState.bind(history);

describe("nav-shim — MAIN-world diagnostic shim (S31 carry-forward fix, docs/09)", () => {
  beforeEach(() => {
    vi.resetModules();
    history.pushState = NATIVE_PUSH;
    history.replaceState = NATIVE_REPLACE;
    (
      window as unknown as { __marketOpsNavShimInstalled__?: boolean }
    ).__marketOpsNavShimInstalled__ = undefined;
  });

  it("calls the ORIGINAL pushState UNCHANGED — never alters page traffic (diagnostic-only)", async () => {
    const original = history.pushState.bind(history);
    let calledWith: unknown;
    history.pushState = ((...args: Parameters<typeof history.pushState>) => {
      calledWith = args;
      return original(...args);
    }) as typeof history.pushState;

    await import("./nav-shim");

    history.pushState({ a: 1 }, "", "/product/dkp-123/x/");
    expect(calledWith).toEqual([{ a: 1 }, "", "/product/dkp-123/x/"]);
  });

  it("dispatches a market-ops:navigation CustomEvent on window after pushState/replaceState", async () => {
    await import("./nav-shim");
    const seen = vi.fn();
    window.addEventListener("market-ops:navigation", seen);

    history.pushState({}, "", "/product/dkp-1/a/");
    expect(seen).toHaveBeenCalledTimes(1);

    history.replaceState({}, "", "/product/dkp-2/b/");
    expect(seen).toHaveBeenCalledTimes(2);

    window.removeEventListener("market-ops:navigation", seen);
  });

  it("is idempotent — importing twice never double-patches (no double-fired event)", async () => {
    await import("./nav-shim");
    vi.resetModules();
    await import("./nav-shim");

    const seen = vi.fn();
    window.addEventListener("market-ops:navigation", seen);
    history.pushState({}, "", "/product/dkp-9/z/");
    expect(seen).toHaveBeenCalledTimes(1);
    window.removeEventListener("market-ops:navigation", seen);
  });
});
