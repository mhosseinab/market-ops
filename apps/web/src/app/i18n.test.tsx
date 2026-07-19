import { afterEach, describe, expect, it, vi } from "vitest";
import {
  type Catalog,
  createI18n,
  en,
  faIR,
  type MissingKeyEvent,
  translate,
} from "@market-ops/locale";
import type { MissingKeyReport, MissingKeySink } from "./missingKeyTelemetry";
import { reportMissingKey, resetMissingKeySink, setMissingKeySink } from "./i18n";

// Issue #14: the PRODUCTION missing-key fallback path must emit an observable
// telemetry signal (not only in DEV). These tests drive the real app sink
// (`reportMissingKey`) with production env stubbed, and assert BOTH the safe
// fallback string still renders AND the injected telemetry adapter fires.

const PERSIAN = /[؀-ۿ]/;

/** An fa-IR catalog with one key removed, so translate() must fall back to en. */
function gappedFaIR(key: string): Catalog {
  const gapped: Catalog = { ...faIR };
  delete (gapped as Record<string, string>)[key];
  return gapped;
}

afterEach(() => {
  resetMissingKeySink();
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

describe("reportMissingKey — production telemetry (issue #14)", () => {
  it("in production, a fallback renders the en value AND emits to the injected sink", () => {
    vi.stubEnv("DEV", false);
    vi.stubEnv("MODE", "production");
    const sink = vi.fn<MissingKeySink>();
    setMissingKeySink(sink);

    const key = "route.today.sub";
    const i18n = createI18n({ lng: "fa-IR", resources: { "fa-IR": gappedFaIR(key) } });
    const out = translate(i18n, key, undefined, reportMissingKey);

    // Safe fallback still renders (never a raw key / blank / crash).
    expect(out).toBe(en[key]);
    // The regression is now observable in prod.
    expect(sink).toHaveBeenCalledTimes(1);
    const report = sink.mock.calls[0]?.[0] as MissingKeyReport;
    expect(report.key).toBe(key);
    expect(report.requested).toBe("fa-IR");
    expect(report.servedBy).toBe("en");
  });

  it("in production, the emitted payload carries NO rendered copy / PII", () => {
    vi.stubEnv("DEV", false);
    vi.stubEnv("MODE", "production");
    const sink = vi.fn<MissingKeySink>();
    setMissingKeySink(sink);

    const key = "route.today.sub";
    const i18n = createI18n({ lng: "fa-IR", resources: { "fa-IR": gappedFaIR(key) } });
    translate(i18n, key, undefined, reportMissingKey);

    const report = sink.mock.calls[0]?.[0] as MissingKeyReport;
    // The rendered fa fallback copy is Persian; it must never appear in telemetry.
    expect(PERSIAN.test(JSON.stringify(report))).toBe(false);
    const record = report as unknown as Record<string, unknown>;
    for (const forbidden of ["value", "copy", "rendered", "text", "message"]) {
      expect(record[forbidden]).toBeUndefined();
    }
  });

  it("in production, a throwing telemetry adapter never breaks rendering", () => {
    vi.stubEnv("DEV", false);
    vi.stubEnv("MODE", "production");
    setMissingKeySink(() => {
      throw new Error("telemetry backend down");
    });

    const key = "route.today.sub";
    const i18n = createI18n({ lng: "fa-IR", resources: { "fa-IR": gappedFaIR(key) } });
    let out = "";
    expect(() => {
      out = translate(i18n, key, undefined, reportMissingKey);
    }).not.toThrow();
    expect(out).toBe(en[key]);
  });

  it("in development, keeps the console.warn breadcrumb", () => {
    vi.stubEnv("DEV", true);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const event: MissingKeyEvent = { key: "route.today.sub", requested: "fa-IR", servedBy: "en" };
    reportMissingKey(event);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn.mock.calls[0]?.[1]).toBe(event);
  });
});
