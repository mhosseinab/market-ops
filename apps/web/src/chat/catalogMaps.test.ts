import { buildPseudoCatalog, en, faIR, MESSAGE_KEYS } from "@market-ops/locale";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  resetUnsupportedValueSink,
  setUnsupportedValueSink,
  type UnsupportedValueReport,
} from "../app/unsupportedTelemetry";
import {
  BRIEFING_EVENT_TYPE_KEY,
  briefingEventTypeKey,
  EVENT_TYPE_UNKNOWN_KEY,
  FAILURE_CODE_KEY,
  FAILURE_UNKNOWN_KEY,
  failureMessageKey,
} from "./catalogMaps";

// LOC-002 (#121): the web edge maps a stable server `failure.code` / briefing
// `eventType` to CLOSED catalog keys. Supported values resolve to en/fa-IR/pseudo
// catalog copy (never the raw string). An unknown value resolves to a
// catalog-backed unavailable label AND emits PII-free drift telemetry carrying
// only the machine value — never rendered copy.

const pseudo = buildPseudoCatalog();

afterEach(() => {
  resetUnsupportedValueSink();
  vi.restoreAllMocks();
});

describe("closed catalog maps — every mapped value has en/fa-IR/pseudo labels (parity)", () => {
  const mapped = [
    ...Object.values(FAILURE_CODE_KEY),
    ...Object.values(BRIEFING_EVENT_TYPE_KEY),
    FAILURE_UNKNOWN_KEY,
    EVENT_TYPE_UNKNOWN_KEY,
  ];

  it.each(mapped)("mapped key %s is a declared MessageKey with en/fa-IR/pseudo copy", (key) => {
    // A mapped key missing from the closed set / a catalog would fail the parity
    // gate (catalog.test.ts) too — this asserts the map cannot reference a
    // label-less key.
    expect(MESSAGE_KEYS).toContain(key);
    expect(en[key]).toBeTruthy();
    expect(faIR[key]).toBeTruthy();
    expect(pseudo[key]).toBeTruthy();
  });
});

describe("failureMessageKey — closed failure-code map", () => {
  // The stable §12.4 codes the LLM plane emits on the `failure` frame.
  const SUPPORTED = [
    "TURN_RECURSION_LIMIT",
    "TOOL_CALL_LIMIT",
    "TOOL_TIMEOUT",
    "TOKEN_CEILING",
    "MODEL_PROVIDER_ERROR",
    "MODEL_TRANSIENT_FAILURE",
  ] as const;

  it.each(SUPPORTED)("%s resolves to a catalog key, not the raw code", (code) => {
    const reports: UnsupportedValueReport[] = [];
    setUnsupportedValueSink((r) => reports.push(r));
    const key = failureMessageKey(code);
    expect(MESSAGE_KEYS).toContain(key);
    // The resolved copy is localized catalog text — never the machine code.
    expect(en[key]).not.toBe(code);
    expect(en[key]).toBeTruthy();
    expect(faIR[key]).toBeTruthy();
    // A supported code is NOT drift — no telemetry.
    expect(reports).toHaveLength(0);
  });

  it("an unknown code resolves to the unavailable label + emits telemetry with only the machine code", () => {
    const reports: UnsupportedValueReport[] = [];
    setUnsupportedValueSink((r) => reports.push(r));

    const key = failureMessageKey("BUDGET_EXCEEDED");

    expect(key).toBe(FAILURE_UNKNOWN_KEY);
    // The localized fallback is catalog copy — the raw code never becomes copy.
    expect(en[key]).toBeTruthy();
    expect(en[key]).not.toContain("BUDGET_EXCEEDED");
    expect(reports).toHaveLength(1);
    expect(reports[0]).toMatchObject({ kind: "chat_failure_code", value: "BUDGET_EXCEEDED" });
    // Telemetry carries ONLY the technical identifier — no rendered copy.
    expect(reports[0]?.value).toBe("BUDGET_EXCEEDED");
    expect(JSON.stringify(reports[0])).not.toContain(en[key]);
    expect(JSON.stringify(reports[0])).not.toContain(faIR[key]);
  });

  it("dedupes repeated unknown codes so a hot render loop cannot flood telemetry", () => {
    const reports: UnsupportedValueReport[] = [];
    setUnsupportedValueSink((r) => reports.push(r));
    failureMessageKey("NOVEL_CODE");
    failureMessageKey("NOVEL_CODE");
    failureMessageKey("NOVEL_CODE");
    expect(reports).toHaveLength(1);
  });
});

describe("briefingEventTypeKey — closed event-type map", () => {
  const SUPPORTED: Array<[string, string]> = [
    ["winning_state", "eventType.buyBox"],
    ["competitor_price", "eventType.competitorOffer"],
    ["seller_count", "eventType.sellerCount"],
    ["suppression_boundary", "eventType.priceBoundary"],
    ["contribution_floor", "eventType.marginFloor"],
  ];

  it.each(SUPPORTED)("%s resolves to its glossary label %s, not the raw type", (type, expected) => {
    const reports: UnsupportedValueReport[] = [];
    setUnsupportedValueSink((r) => reports.push(r));
    const key = briefingEventTypeKey(type);
    expect(key).toBe(expected);
    expect(en[key]).not.toBe(type);
    expect(reports).toHaveLength(0);
  });

  it("an unknown eventType resolves to the unavailable label + emits telemetry with only the machine type", () => {
    const reports: UnsupportedValueReport[] = [];
    setUnsupportedValueSink((r) => reports.push(r));

    const key = briefingEventTypeKey("mystery_signal");

    expect(key).toBe(EVENT_TYPE_UNKNOWN_KEY);
    expect(en[key]).toBeTruthy();
    expect(en[key]).not.toContain("mystery_signal");
    expect(reports).toHaveLength(1);
    expect(reports[0]).toMatchObject({ kind: "briefing_event_type", value: "mystery_signal" });
    expect(JSON.stringify(reports[0])).not.toContain(en[key]);
    expect(JSON.stringify(reports[0])).not.toContain(faIR[key]);
  });
});
