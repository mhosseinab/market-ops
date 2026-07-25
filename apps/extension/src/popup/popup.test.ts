import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ExtMessage, ExtResponse } from "../lib/messages";
import type { PopupState } from "../lib/storage";

// Popup renders EXCLUSIVELY through the shared fa-IR catalog (LOC boundary /
// S31 carry-forward fix). This test proves the visible copy is the translated
// Persian catalog value, not the old hardcoded English literal, and that the
// kill switch (EXT-009) produces a real, locale-neutrally-addressable state.

function unknownState(): PopupState {
  return {
    capability: "unknown",
    marketplaceAccountId: null,
    lastUploadAt: null,
    queuedCount: 0,
    degradation: "not_paired",
    scheduleEnabled: false,
    deadLetter: [],
    revocationUnconfirmed: false,
  };
}

function readyState(): PopupState {
  return {
    capability: "ready",
    marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
    lastUploadAt: "2026-07-18T10:00:00Z",
    queuedCount: 3,
    degradation: null,
    scheduleEnabled: false,
    deadLetter: [],
    revocationUnconfirmed: false,
  };
}

async function loadPopup(sendMessage: (msg: ExtMessage) => Promise<ExtResponse>): Promise<void> {
  document.body.innerHTML = '<div id="root"></div>';
  (globalThis as unknown as { chrome: unknown }).chrome = {
    runtime: { sendMessage: vi.fn((m: ExtMessage) => Promise.resolve(sendMessage(m))) },
  };
  vi.resetModules();
  await import("./popup");
  // Flush the module's top-level `void refresh()` microtask.
  await Promise.resolve();
  await Promise.resolve();
}

describe("popup — fa-IR catalog copy, no inline literals (S31 carry-forward)", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the Persian degradation copy for an unknown (never-paired) capability", async () => {
    await loadPopup(async () => ({ ok: true, state: unknownState() }));
    const note = document.querySelector('[data-role="degradation"]');
    expect(note?.getAttribute("data-reason")).toBe("not_paired");
    // Verbatim catalog value, not the old hardcoded English string.
    expect(note?.textContent).toBe("جفت نشده — کد جفت‌سازی را وارد کنید.");
    expect(note?.textContent).not.toMatch(/Not paired/);
  });

  it("renders a stable, locale-neutral field id alongside translated Persian labels", async () => {
    await loadPopup(async () => ({ ok: true, state: readyState() }));
    const captureRow = document.querySelector('[data-field="capture"]');
    expect(captureRow?.textContent).toBe("ضبط: روشن");
    const toggle = document.querySelector('[data-role="capture-toggle"]');
    expect(toggle?.textContent).toBe("غیرفعال‌سازی ضبط");
  });

  it("shows the pairing input with Persian placeholder + submit copy when unpaired", async () => {
    await loadPopup(async () => ({ ok: true, state: unknownState() }));
    const input = document.querySelector<HTMLInputElement>("#pairing-code");
    expect(input?.placeholder).toBe("کد جفت‌سازی");
    const buttons = Array.from(document.querySelectorAll("button")).map((b) => b.textContent);
    expect(buttons).toContain("جفت‌سازی");
  });

  it("surfaces the durable dead-letter count + a per-item retry/discard affordance (issue #150, EXT-009)", async () => {
    const sent: ExtMessage[] = [];
    const state = readyState();
    state.deadLetter = [{ dedupKey: "abc12345", failureReason: "max_attempts_exhausted" }];
    await loadPopup(async (m) => {
      sent.push(m);
      return { ok: true, state };
    });

    // Visible count row + the localized failure reason (never inline English).
    // The count uses the fa-IR digit family (LOC-005) — Persian ۱, not ASCII 1.
    expect(document.querySelector('[data-field="dead-letter"]')?.textContent).toBe(
      "بارگذاری‌های ناموفق: ۱",
    );
    const item = document.querySelector('[data-role="dead-letter-item"]');
    expect(item?.getAttribute("data-dedup-key")).toBe("abc12345");
    expect(item?.querySelector('[data-role="dead-letter-reason"]')?.textContent).toBe(
      "تلاش‌ها به پایان رسید",
    );

    // Retry and discard are real operator actions addressed by the stable key.
    const retry = document.querySelector<HTMLButtonElement>('[data-role="dead-letter-retry"]');
    const discard = document.querySelector<HTMLButtonElement>('[data-role="dead-letter-discard"]');
    expect(retry?.textContent).toBe("تلاش دوباره");
    expect(discard?.textContent).toBe("دور انداختن");
    retry?.click();
    discard?.click();
    await Promise.resolve();
    expect(sent).toContainEqual({ kind: "retryDeadLetter", dedupKey: "abc12345" });
    expect(sent).toContainEqual({ kind: "discardDeadLetter", dedupKey: "abc12345" });
  });
});

// Dynamic-value localization + bidi isolation (issue #160, LOC-005). Catalog
// translation covers the LABELS; these tests cover the VALUES — Persian digits,
// the Persian display calendar, and LTR-isolation of technical identifiers so
// the RTL popup never bidi-scrambles a UUID/ISO fragment.
describe("popup — dynamic value locale formatting + bidi isolation (issue #160)", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  function value(field: string): Element | null | undefined {
    return document.querySelector(`[data-field="${field}"] [data-role="row-value"]`);
  }

  it("renders the queued count in the fa-IR digit family (not ASCII), keeping the raw value for tooling", async () => {
    const state = readyState();
    state.queuedCount = 12;
    await loadPopup(async () => ({ ok: true, state }));
    const queued = value("queued");
    expect(queued?.textContent).toBe("۱۲");
    expect(queued?.getAttribute("data-raw")).toBe("12");
    // No ASCII digit leaks onto the Persian surface.
    expect(queued?.textContent).not.toMatch(/[0-9]/);
  });

  it("renders the account UUID as an LTR-isolated token (dir=ltr + isolate class) with the raw value preserved", async () => {
    await loadPopup(async () => ({ ok: true, state: readyState() }));
    const account = value("account");
    expect(account?.textContent).toBe("11111111-1111-1111-1111-111111111111");
    expect(account?.getAttribute("data-raw")).toBe("11111111-1111-1111-1111-111111111111");
    expect(account?.getAttribute("dir")).toBe("ltr");
    expect(account?.classList.contains("market-ops-ltr")).toBe(true);
  });

  it("renders the last-upload time in the approved Persian calendar (Persian digits, not the raw ISO), keeping the ISO in data-raw", async () => {
    const state = readyState();
    state.lastUploadAt = "2026-07-18T10:30:00Z";
    await loadPopup(async () => ({ ok: true, state }));
    const upload = value("last-upload");
    // Stable raw ISO retained for tests/tooling.
    expect(upload?.getAttribute("data-raw")).toBe("2026-07-18T10:30:00Z");
    // Not the raw Gregorian ISO string on the Persian surface.
    expect(upload?.textContent).not.toContain("2026-07-18T");
    expect(upload?.textContent).not.toContain("2026");
    // Persian display calendar renders Persian digits.
    expect(upload?.textContent ?? "").toMatch(/[۰-۹]/);
    // A user-facing localized date is RTL Persian copy, NOT an LTR token.
    expect(upload?.classList.contains("market-ops-ltr")).toBe(false);
  });

  it("renders the catalog unavailable state for a missing last-upload timestamp", async () => {
    await loadPopup(async () => ({ ok: true, state: unknownState() }));
    const upload = value("last-upload");
    expect(upload?.textContent).toBe("در دسترس نیست");
    expect(upload?.getAttribute("data-raw")).toBe("");
  });

  it("renders the catalog unavailable state for a missing account", async () => {
    await loadPopup(async () => ({ ok: true, state: unknownState() }));
    const account = value("account");
    expect(account?.textContent).toBe("در دسترس نیست");
    // The unavailable placeholder is Persian copy, never an LTR technical token.
    expect(account?.classList.contains("market-ops-ltr")).toBe(false);
  });
});

// Issue #149, fix 3. The "could not confirm" state has NO approved Persian term
// yet — `design/` is read-only and its glossary is the single source for state
// copy, so coining one here would be inventing product language. The catalog key
// is DECLARED in code (PENDING product-owner approval) and deliberately NOT
// added to the locale pack, so the popup falls back to rendering the stable
// locale-neutral token. That is visibly unfinished, which is correct — a
// placeholder that reads as approved copy would be worse.
//
// These are NEGATIVES: the state must never borrow another state's copy.
describe("popup — the unconfirmed-revocation state never borrows another state's copy (#149)", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  function unconfirmedState(): PopupState {
    return {
      capability: "revocation_unconfirmed",
      marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
      lastUploadAt: null,
      queuedCount: 0,
      degradation: "revocation_unconfirmed",
      scheduleEnabled: false,
      deadLetter: [],
      revocationUnconfirmed: true,
    };
  }

  it("NEVER renders the `credentialRevoked` copy — that would claim a kill switch it did not achieve", async () => {
    await loadPopup(async () => ({ ok: true, state: unconfirmedState() }));
    const note = document.querySelector('[data-role="degradation"]');
    expect(note?.getAttribute("data-reason")).toBe("revocation_unconfirmed");
    // The verbatim fa-IR values of the two states it must not be confused with.
    expect(note?.textContent).not.toBe("دسترسی لغو شد — برای ادامه دوباره جفت‌سازی کنید.");
    expect(note?.textContent).not.toBe("ضبط خاموش است — در انتظار تایید لغو دسترسی از سرور.");
    // It renders the stable locale-neutral token instead — honestly unfinished.
    expect(note?.textContent).toBe("revocation_unconfirmed");
  });

  it("keeps an outstanding unconfirmed revocation VISIBLE even after a RE-PAIR", async () => {
    // Re-paired: the capability reads `ready` again and there is no degradation
    // to show, yet an earlier credential may still be live at the authority.
    await loadPopup(async () => ({
      ok: true,
      state: { ...readyState(), revocationUnconfirmed: true },
    }));
    const indicator = document.querySelector('[data-role="revocation-unconfirmed"]');
    expect(indicator).not.toBeNull();
    expect(document.querySelector('[data-role="degradation"]')).toBeNull();
  });

  it("shows NO indicator when there is no outstanding unconfirmed revocation", async () => {
    await loadPopup(async () => ({ ok: true, state: readyState() }));
    expect(document.querySelector('[data-role="revocation-unconfirmed"]')).toBeNull();
  });
});
