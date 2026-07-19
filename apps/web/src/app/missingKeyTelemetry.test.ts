import { afterEach, describe, expect, it, vi } from "vitest";
import type { MissingKeyEvent } from "@market-ops/locale";
import {
  createMissingKeyReporter,
  type MissingKeyReport,
  type MissingKeySink,
} from "./missingKeyTelemetry";

// The missing-key telemetry adapter (issue #14): production fallbacks must emit
// an OBSERVABLE, bounded, failure-safe signal that carries technical identifiers
// only — never rendered Persian/fallback copy, never user data.

const EVENT: MissingKeyEvent = {
  key: "route.today.sub",
  requested: "fa-IR",
  servedBy: "en",
};

// Persian/Arabic script — a payload carrying any of this would be leaking copy.
const PERSIAN = /[؀-ۿ]/;

afterEach(() => vi.unstubAllEnvs());

describe("createMissingKeyReporter", () => {
  it("emits a report to the injected sink for a fallback event", () => {
    const sink = vi.fn<MissingKeySink>();
    const report = createMissingKeyReporter(sink);
    report(EVENT);
    expect(sink).toHaveBeenCalledTimes(1);
    const emitted = sink.mock.calls[0]?.[0] as MissingKeyReport;
    expect(emitted.key).toBe("route.today.sub");
    expect(emitted.requested).toBe("fa-IR");
    expect(emitted.servedBy).toBe("en");
  });

  it("carries ONLY the safe key set and no rendered copy / PII", () => {
    const sink = vi.fn<MissingKeySink>();
    createMissingKeyReporter(sink)(EVENT);
    const emitted = sink.mock.calls[0]?.[0] as MissingKeyReport;

    // Exactly the technical-identifier + safe-context fields — nothing else.
    expect(Object.keys(emitted).sort()).toEqual(
      ["key", "page", "release", "requested", "servedBy"].sort(),
    );
    // No field that could hold the rendered fallback string.
    const record = emitted as unknown as Record<string, unknown>;
    for (const forbidden of ["value", "copy", "rendered", "text", "message"]) {
      expect(record[forbidden]).toBeUndefined();
    }
    // The whole serialized payload is ASCII technical data — no locale copy.
    expect(PERSIAN.test(JSON.stringify(emitted))).toBe(false);
  });

  it("enriches with safe release + page (pathname only, no query/hash)", () => {
    vi.stubEnv("VITE_RELEASE", "web@1.2.3");
    const original = window.location.pathname;
    const sink = vi.fn<MissingKeySink>();
    createMissingKeyReporter(sink)(EVENT);
    const emitted = sink.mock.calls[0]?.[0] as MissingKeyReport;
    expect(emitted.release).toBe("web@1.2.3");
    expect(emitted.page).toBe(original);
    expect(emitted.page).not.toContain("?");
    expect(emitted.page).not.toContain("#");
  });

  it("deduplicates identical misses and stays bounded (evicts oldest)", () => {
    const sink = vi.fn<MissingKeySink>();
    const report = createMissingKeyReporter(sink, { dedupLimit: 2 });
    const a: MissingKeyEvent = { key: "a", requested: "fa-IR", servedBy: "en" };
    const b: MissingKeyEvent = { key: "b", requested: "fa-IR", servedBy: "en" };
    const c: MissingKeyEvent = { key: "c", requested: "fa-IR", servedBy: "en" };

    report(a); // new  -> emit (1)
    report(b); // new  -> emit (2)
    report(a); // seen -> deduped, no emit
    report(a); // seen -> deduped, no emit
    report(c); // new, over limit -> evicts oldest (a), emit (3)
    report(a); // a was evicted -> emitted again (4), proving bounded memory

    expect(sink).toHaveBeenCalledTimes(4);
  });

  it("distinguishes events by requested/servedBy, not just key", () => {
    const sink = vi.fn<MissingKeySink>();
    const report = createMissingKeyReporter(sink);
    report({ key: "k", requested: "fa-IR", servedBy: "en" });
    report({ key: "k", requested: "ps-Pseudo", servedBy: "en" });
    expect(sink).toHaveBeenCalledTimes(2);
  });

  it("is failure-safe: a throwing sink never propagates", () => {
    const throwing: MissingKeySink = () => {
      throw new Error("telemetry backend down");
    };
    const report = createMissingKeyReporter(throwing);
    expect(() => report(EVENT)).not.toThrow();
  });

  it("is failure-safe: a throwing sink does not block later distinct events", () => {
    let calls = 0;
    const flaky: MissingKeySink = () => {
      calls += 1;
      if (calls === 1) throw new Error("boom");
    };
    const report = createMissingKeyReporter(flaky);
    expect(() => report({ key: "x", requested: "fa-IR", servedBy: "en" })).not.toThrow();
    expect(() => report({ key: "y", requested: "fa-IR", servedBy: "en" })).not.toThrow();
    expect(calls).toBe(2);
  });
});
