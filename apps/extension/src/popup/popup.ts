import { formatDate, formatInteger } from "@market-ops/locale";
import { EXT_LOCALE, t } from "../lib/i18n";
import type { ExtMessage, ExtResponse } from "../lib/messages";
import type { PopupState } from "../lib/storage";

// Popup (EXT-009 kill switch): a VISIBLE, real state — account, capture toggle,
// last upload, queued count, and the degradation reason. Disabling capture must
// produce a visibly disabled state, never a silent no-op. The popup only reads
// and forwards; it never computes commercial values. ALL copy flows through the
// shared fa-IR catalog (LOC boundary) — zero string literals here (S31: closes
// the S30 carry-forward where the popup rendered hardcoded English).
//
// Dynamic VALUES are localized too (issue #160, LOC-005): counts render in the
// active locale's digit family via `formatInteger`, the last-upload time in the
// approved Persian display calendar / IR timezone via `formatDate`, and raw
// technical identifiers (account UUID, ISO instants) are LTR-isolated inside the
// RTL popup — mirroring the overlay's `ltrToken` and apps/web's `LtrToken` — so
// the bidi algorithm never reorders their hyphen/segment fragments. Stable raw
// values are kept in `data-raw` for tests/tooling.

// Presentation options for the user-facing last-upload time. `formatDate`
// defaults the timezone to the IR region (Asia/Tehran) and the tag carries the
// Jalali calendar + digit family — no locale/calendar branch lives here.
const UPLOAD_DATE_OPTS: Intl.DateTimeFormatOptions = {
  year: "numeric",
  month: "long",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
};

// The LTR-isolation posture, shared by the overlay/web LtrToken pattern. A plain
// <style> assignment (identifier, not a quoted copy literal) — copy-lint clean.
const POPUP_STYLE = `
  .market-ops-ltr {
    direction: ltr;
    unicode-bidi: isolate;
    display: inline-block;
  }
`;

function send(msg: ExtMessage): Promise<ExtResponse> {
  return chrome.runtime.sendMessage(msg) as Promise<ExtResponse>;
}

// Format a UTC instant in the active locale's display calendar. Returns null on
// an unparseable instant so the caller falls back to the catalog unavailable
// state — a bad timestamp is quarantined to the honest "not available" copy,
// never coerced or shown raw (fail closed, LOC-005 / §4.6).
function formatUpload(instant: string): string | null {
  try {
    return formatDate(instant, EXT_LOCALE, UPLOAD_DATE_OPTS);
  } catch {
    return null;
  }
}

// A stable degradation token → catalog key map. `degradationReason()` in
// capability.ts returns a LOCALE-NEUTRAL token; this is the ONLY place it is
// mapped to display copy, and it goes through the catalog, never inline text.
const DEGRADATION_KEY = {
  not_paired: "ext.degradation.notPaired",
  credential_revoked: "ext.degradation.credentialRevoked",
  capture_disabled: "ext.degradation.captureDisabled",
} as const;

// A dead-letter failure-reason token → catalog key map (issue #150). The reason
// is a LOCALE-NEUTRAL token from the queue; this is the ONLY place it becomes
// display copy, and it goes through the catalog, never inline text.
const DEAD_LETTER_REASON_KEY = {
  max_attempts_exhausted: "ext.deadLetter.reason.maxAttempts",
} as const;

function render(state: PopupState): void {
  const root = document.getElementById("root");
  if (!root) return;
  root.replaceChildren();

  const style = document.createElement("style");
  style.textContent = POPUP_STYLE;
  root.appendChild(style);

  // Account: a UUID is a technical identifier → LTR-isolated so RTL bidi never
  // reorders its segments; missing ⇒ the catalog unavailable state.
  root.appendChild(
    valueRow(
      "account",
      t("ext.popup.account"),
      state.marketplaceAccountId ? ltrTokenValue(state.marketplaceAccountId) : unavailableValue(),
    ),
  );
  root.appendChild(
    row(
      "capture",
      t("ext.popup.capture"),
      state.capability === "ready" ? t("ext.popup.on") : t("ext.popup.off"),
    ),
  );
  // Last upload: user-facing time in the approved Persian display calendar; the
  // raw ISO instant is retained in data-raw. An unparseable/absent instant ⇒
  // the catalog unavailable state (never a raw ISO on the Persian surface).
  const upload = state.lastUploadAt ? formatUpload(state.lastUploadAt) : null;
  root.appendChild(
    valueRow(
      "last-upload",
      t("ext.popup.lastUpload"),
      upload !== null ? localeValue(upload, state.lastUploadAt as string) : unavailableValue(),
    ),
  );
  // Queued count: the active locale's digit family (Persian for fa-IR).
  root.appendChild(
    valueRow(
      "queued",
      t("ext.popup.queued"),
      localeValue(formatInteger(state.queuedCount, EXT_LOCALE), String(state.queuedCount)),
    ),
  );

  // Durable dead-letter (issue #150 / EXT-009): a VISIBLE count plus a per-item
  // recovery affordance — exhausted uploads are never silently erased. Rendered
  // only when there is something to recover.
  if (state.deadLetter.length > 0) {
    root.appendChild(
      row(
        "dead-letter",
        t("ext.deadLetter.count"),
        formatInteger(state.deadLetter.length, EXT_LOCALE),
      ),
    );
    for (const item of state.deadLetter) {
      const el = document.createElement("div");
      el.dataset.role = "dead-letter-item";
      // The dedup key is a technical identifier (LTR-isolated data attribute),
      // used to address the operator action — never rendered as copy.
      el.dataset.dedupKey = item.dedupKey;
      const reasonKey =
        DEAD_LETTER_REASON_KEY[item.failureReason as keyof typeof DEAD_LETTER_REASON_KEY];
      const reason = document.createElement("span");
      reason.dataset.role = "dead-letter-reason";
      reason.textContent = reasonKey ? t(reasonKey) : item.failureReason;
      el.appendChild(reason);

      const retryBtn = button(t("ext.deadLetter.retry"), async () => {
        await send({ kind: "retryDeadLetter", dedupKey: item.dedupKey });
        await refresh();
      });
      retryBtn.dataset.role = "dead-letter-retry";
      el.appendChild(retryBtn);

      const discardBtn = button(t("ext.deadLetter.discard"), async () => {
        await send({ kind: "discardDeadLetter", dedupKey: item.dedupKey });
        await refresh();
      });
      discardBtn.dataset.role = "dead-letter-discard";
      el.appendChild(discardBtn);

      root.appendChild(el);
    }
  }

  if (state.degradation) {
    const key = DEGRADATION_KEY[state.degradation as keyof typeof DEGRADATION_KEY];
    const note = document.createElement("p");
    note.dataset.role = "degradation";
    note.dataset.reason = state.degradation;
    note.textContent = key ? t(key) : state.degradation;
    root.appendChild(note);
  }

  // Pairing input (shown when not yet paired / revoked).
  if (state.capability === "unknown" || state.capability === "revoked") {
    const input = document.createElement("input");
    input.id = "pairing-code";
    input.placeholder = t("ext.pairing.placeholder");
    const pairBtn = button(t("ext.pairing.submit"), async () => {
      const code = input.value.trim();
      if (code) await send({ kind: "pair", code });
      await refresh();
    });
    root.appendChild(input);
    root.appendChild(pairBtn);
  }

  // Capture kill switch — a real toggle producing a visible state.
  if (state.capability === "ready" || state.capability === "disabled") {
    const toggle = button(
      state.capability === "ready" ? t("ext.capture.disable") : t("ext.capture.enable"),
      async () => {
        await send({ kind: "setEnabled", enabled: state.capability !== "ready" });
        await refresh();
      },
    );
    toggle.dataset.role = "capture-toggle";
    root.appendChild(toggle);
  }

  // EXT-012 opt-in toggle for bounded scheduled refresh — only offered once
  // paired (never enables anything while Unknown/revoked).
  if (state.capability === "ready") {
    root.appendChild(
      row(
        "schedule",
        t("ext.schedule.toggle"),
        state.scheduleEnabled ? t("ext.schedule.on") : t("ext.schedule.off"),
      ),
    );
    const scheduleToggle = button(
      state.scheduleEnabled ? t("ext.schedule.off") : t("ext.schedule.on"),
      async () => {
        await send({ kind: "setScheduleEnabled", enabled: !state.scheduleEnabled });
        await refresh();
      },
    );
    scheduleToggle.dataset.role = "schedule-toggle";
    root.appendChild(scheduleToggle);
  }
}

// `field` is a STABLE, locale-neutral identifier for tests/tooling — never the
// translated label itself (which is Persian in the shipping locale).
function row(field: string, label: string, value: string): HTMLElement {
  const el = document.createElement("div");
  el.dataset.role = "row";
  el.dataset.field = field;
  el.textContent = `${label}: ${value}`;
  return el;
}

// A row whose VALUE is a dedicated element (localized copy or an LTR-isolated
// technical token) rather than plain text — so a technical identifier can be
// bidi-isolated from the RTL label without a locale/direction branch in the
// caller. Keeps the stable `data-field` for tooling.
function valueRow(field: string, label: string, value: HTMLElement): HTMLElement {
  const el = document.createElement("div");
  el.dataset.role = "row";
  el.dataset.field = field;
  const labelEl = document.createElement("span");
  labelEl.dataset.role = "row-label";
  labelEl.textContent = `${label}: `;
  el.appendChild(labelEl);
  el.appendChild(value);
  return el;
}

// A user-facing localized value (Persian digits / display calendar): RTL copy,
// with the stable raw value preserved in `data-raw` for tests/tooling.
function localeValue(display: string, raw: string): HTMLElement {
  const el = document.createElement("span");
  el.dataset.role = "row-value";
  el.dataset.raw = raw;
  el.textContent = display;
  return el;
}

// A raw technical identifier (UUID, native id): LTR-isolated (`dir=ltr` +
// `unicode-bidi:isolate`) so the RTL bidi algorithm never reorders its
// hyphen/segment fragments — mirroring the overlay's ltrToken and apps/web's
// LtrToken. The raw value is both the display and `data-raw`.
function ltrTokenValue(raw: string): HTMLElement {
  const el = document.createElement("span");
  el.dataset.role = "row-value";
  el.className = "market-ops-ltr";
  el.dir = "ltr";
  el.dataset.raw = raw;
  el.textContent = raw;
  return el;
}

// The catalog-backed "not available" value — Persian copy (never an LTR token),
// with an empty `data-raw` marking the absence for tooling.
function unavailableValue(): HTMLElement {
  return localeValue(t("common.notAvailable"), "");
}

function button(label: string, onClick: () => void | Promise<void>): HTMLButtonElement {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = label;
  b.addEventListener("click", () => void onClick());
  return b;
}

async function refresh(): Promise<void> {
  const resp = await send({ kind: "getState" });
  if (resp.ok && "state" in resp) render(resp.state);
}

void refresh();
