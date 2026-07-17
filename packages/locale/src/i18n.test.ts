import { describe, expect, it, vi } from "vitest";
import { en } from "./catalog/en";
import { faIR } from "./catalog/fa-IR";
import type { Catalog } from "./catalog/keys";
import { createI18n, type MissingKeyEvent, translate } from "./i18n";

describe("i18next + ICU factory", () => {
  it("resolves fa-IR ICU messages with named slots", () => {
    const i18n = createI18n({ lng: "fa-IR" });
    expect(translate(i18n, "state.accepted", { marketplace: faIR["marketplace.name"] })).toBe(
      "تاییدشده توسط دیجی‌کالا",
    );
  });

  it("applies ICU plural rules per locale", () => {
    const i18n = createI18n({ lng: "en" });
    expect(translate(i18n, "readiness.missingCount", { count: 1 })).toBe("1 item missing cost");
    expect(translate(i18n, "readiness.missingCount", { count: 3 })).toBe("3 items missing cost");
  });

  it("falls back to English and emits telemetry on a fa-IR gap (LOC-004)", () => {
    // A deliberately incomplete fa-IR pack: drop one key.
    const gapped: Catalog = { ...faIR, "route.today.sub": undefined as unknown as string };
    delete (gapped as Record<string, string>)["route.today.sub"];
    const telemetry = vi.fn<(e: MissingKeyEvent) => void>();
    const i18n = createI18n({ lng: "fa-IR", resources: { "fa-IR": gapped } });

    const out = translate(i18n, "route.today.sub", undefined, telemetry);
    // No raw key, no blank: the English authoring value is served.
    expect(out).toBe(en["route.today.sub"]);
    expect(telemetry).toHaveBeenCalledWith({
      key: "route.today.sub",
      requested: "fa-IR",
      servedBy: "en",
    });
  });

  it("does not emit telemetry when the active locale has the key", () => {
    const telemetry = vi.fn();
    const i18n = createI18n({ lng: "fa-IR" });
    translate(i18n, "nav.today", undefined, telemetry);
    expect(telemetry).not.toHaveBeenCalled();
  });
});
