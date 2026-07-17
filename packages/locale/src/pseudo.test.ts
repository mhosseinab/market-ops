import { describe, expect, it } from "vitest";
import { en } from "./catalog/en";
import { MESSAGE_KEYS } from "./catalog/keys";
import { createI18n, translate } from "./i18n";
import { buildPseudoCatalog, isPseudoTranslated, PSEUDO_CLOSE, PSEUDO_OPEN } from "./pseudo";

describe("pseudo-locale pack (LOC-011)", () => {
  const pseudo = buildPseudoCatalog(en);

  it("brackets and expands every key", () => {
    for (const key of MESSAGE_KEYS) {
      const value = pseudo[key];
      expect(value.includes(PSEUDO_OPEN) && value.includes(PSEUDO_CLOSE), key).toBe(true);
      expect(value.length).toBeGreaterThan(en[key].length);
    }
  });

  it("keeps ICU argument blocks intact so messages still parse", () => {
    const i18n = createI18n({ lng: "pseudo", resources: { pseudo } });
    const out = translate(i18n, "readiness.missingCount", { count: 2 });
    expect(out).toContain("2");
    expect(isPseudoTranslated(out)).toBe(true);
  });

  it("resolves the marketplace slot under the pseudo locale", () => {
    const i18n = createI18n({ lng: "pseudo", resources: { pseudo } });
    const out = translate(i18n, "state.accepted", { marketplace: "X" });
    expect(out).toContain("X");
    expect(isPseudoTranslated(out)).toBe(true);
  });

  it("detects an untranslated (non-bracketed) string", () => {
    expect(isPseudoTranslated("Today")).toBe(false);
    expect(isPseudoTranslated(`${PSEUDO_OPEN}Tódáý${PSEUDO_CLOSE}`)).toBe(true);
  });
});
