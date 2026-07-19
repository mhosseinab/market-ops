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
    expect(document.querySelector('[data-field="dead-letter"]')?.textContent).toBe(
      "بارگذاری‌های ناموفق: 1",
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
