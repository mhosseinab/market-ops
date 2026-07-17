import { describe, expect, it } from "vitest";
import { en } from "./en";
import { faIR } from "./fa-IR";
import { MESSAGE_KEYS } from "./keys";

// The canonical observation/execution state terms — VERBATIM from PRD §11.4 /
// design/README.md §"Canonical State Glossary". Any drift here is a glossary
// violation, not a copy tweak.
const CANONICAL_FA: Record<string, string> = {
  "state.verified": "تاییدشده",
  "state.supported": "پشتیبانی‌شده",
  "state.unverified": "تاییدنشده",
  "state.conflicted": "متناقض",
  "state.stale": "قدیمی‌شده",
  "state.unavailable": "در دسترس نیست",
  "state.blocked": "مسدود",
  "state.awaitingConfirmation": "در انتظار تایید نهایی",
  "state.executing": "در حال اجرا",
  "state.accepted": "تاییدشده توسط {marketplace}",
  "state.rejected": "رد شده",
  "state.pendingReconciliation": "در انتظار تطبیق",
  "state.failed": "ناموفق",
  "state.expired": "منقضی‌شده",
  "state.simulation": "شبیه‌سازی",
  "readiness.complete": "کامل",
  "readiness.partial": "جزئی",
  "readiness.stale": "قدیمی‌شده",
  "readiness.missing": "فاقد بها",
};

describe("catalog coverage + canonical glossary", () => {
  it("fa-IR and en cover every declared key (no gaps, no extras)", () => {
    for (const key of MESSAGE_KEYS) {
      expect(faIR[key], `fa-IR missing ${key}`).toBeTruthy();
      expect(en[key], `en missing ${key}`).toBeTruthy();
    }
    expect(Object.keys(faIR).sort()).toEqual([...MESSAGE_KEYS].sort());
    expect(Object.keys(en).sort()).toEqual([...MESSAGE_KEYS].sort());
  });

  it("renders canonical Persian state terms VERBATIM (PRD §11.4)", () => {
    for (const [key, term] of Object.entries(CANONICAL_FA)) {
      expect(faIR[key as keyof typeof faIR]).toBe(term);
    }
  });
});
