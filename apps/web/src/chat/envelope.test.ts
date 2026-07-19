import { describe, expect, it } from "vitest";
import { parseDeepLink, parseEnvelope } from "./envelope";

describe("parseDeepLink", () => {
  it("parses a path with typed deep-link search params (CHAT-006)", () => {
    expect(parseDeepLink("/event?eventId=e1")).toEqual({ to: "/event", search: { eventId: "e1" } });
    expect(parseDeepLink("/recommendation?cardId=c1")).toEqual({
      to: "/recommendation",
      search: { cardId: "c1" },
    });
    expect(parseDeepLink("/products")).toEqual({ to: "/products" });
  });

  it("rejects a non-path / non-string value (never fabricated)", () => {
    expect(parseDeepLink("https://evil.example")).toBeUndefined();
    expect(parseDeepLink(42)).toBeUndefined();
    expect(parseDeepLink(undefined)).toBeUndefined();
  });
});

describe("parseEnvelope (defensive, contract-gap tolerant)", () => {
  it("returns an empty envelope for a non-object / garbage envelope", () => {
    expect(parseEnvelope(undefined)).toEqual({
      envelope: { sections: [], evidence: [] },
      cards: [],
    });
    expect(parseEnvelope("nope")).toEqual({ envelope: { sections: [], evidence: [] }, cards: [] });
  });

  it("keeps only the seven known statement kinds and drops malformed sections", () => {
    const { envelope } = parseEnvelope({
      sections: [
        { kind: "observed", lines: ["a"] },
        { kind: "recommendation", lines: ["b"] },
        { kind: "not_a_kind", lines: ["x"] },
        { lines: ["no kind"] },
      ],
    });
    expect(envelope.sections.map((s) => s.kind)).toEqual(["observed", "recommendation"]);
  });

  it("drops evidence with no ref and keeps quality/capturedAt when present", () => {
    const { envelope } = parseEnvelope({
      evidence: [
        { ref: "obs-1", quality: "verified", capturedAt: "2026-07-17T09:00:00Z" },
        { quality: "verified" },
        { ref: "obs-2", quality: "not_a_quality" },
      ],
    });
    expect(envelope.evidence).toEqual([
      { ref: "obs-1", quality: "verified", capturedAt: "2026-07-17T09:00:00Z" },
      { ref: "obs-2" },
    ]);
  });

  it("caps an inline table at 20 rows and preserves the true total (CHAT-023)", () => {
    const rows = Array.from({ length: 45 }, (_, i) => [`sku-${i}`, `${i}`]);
    const { envelope } = parseEnvelope({
      table: { headers: ["sku", "n"], rows, totalRows: 45, deepLink: "/products" },
    });
    expect(envelope.table?.rows.length).toBe(20);
    expect(envelope.table?.totalRows).toBe(45);
    expect(envelope.table?.deepLink).toEqual({ to: "/products" });
  });

  it("parses picker / approval / level2 cards and drops unknown/invalid ones", () => {
    const { cards } = parseEnvelope({
      cards: [
        { kind: "picker", options: [{ id: "o1", label: "Sony", sku: "DKP-1" }] },
        { kind: "picker", options: [] },
        { kind: "approval", cardId: "card-1" },
        { kind: "approval" },
        { kind: "level2", proposal: { before: "10:00", after: "12:00" } },
        { kind: "level3" },
      ],
    });
    expect(cards).toEqual([
      { kind: "picker", options: [{ id: "o1", label: "Sony", sku: "DKP-1" }] },
      { kind: "approval", cardId: "card-1" },
      { kind: "level2", proposal: { before: "10:00", after: "12:00" } },
    ]);
  });

  it("an approval card carries ONLY its id — never a cached executable control (§8.1)", () => {
    const { cards } = parseEnvelope({
      cards: [{ kind: "approval", cardId: "card-1", hasControl: true, price: { mantissa: "1" } }],
    });
    expect(cards).toEqual([{ kind: "approval", cardId: "card-1" }]);
    // No control payload survives parsing — the host re-fetches the live card.
    expect(cards[0]).not.toHaveProperty("hasControl");
    expect(cards[0]).not.toHaveProperty("price");
  });
});
