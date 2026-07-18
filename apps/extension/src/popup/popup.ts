import { t } from "../lib/i18n";
import type { ExtMessage, ExtResponse } from "../lib/messages";
import type { PopupState } from "../lib/storage";

// Popup (EXT-009 kill switch): a VISIBLE, real state — account, capture toggle,
// last upload, queued count, and the degradation reason. Disabling capture must
// produce a visibly disabled state, never a silent no-op. The popup only reads
// and forwards; it never computes commercial values. ALL copy flows through the
// shared fa-IR catalog (LOC boundary) — zero string literals here (S31: closes
// the S30 carry-forward where the popup rendered hardcoded English).

function send(msg: ExtMessage): Promise<ExtResponse> {
  return chrome.runtime.sendMessage(msg) as Promise<ExtResponse>;
}

// A stable degradation token → catalog key map. `degradationReason()` in
// capability.ts returns a LOCALE-NEUTRAL token; this is the ONLY place it is
// mapped to display copy, and it goes through the catalog, never inline text.
const DEGRADATION_KEY = {
  not_paired: "ext.degradation.notPaired",
  credential_revoked: "ext.degradation.credentialRevoked",
  capture_disabled: "ext.degradation.captureDisabled",
} as const;

function render(state: PopupState): void {
  const root = document.getElementById("root");
  if (!root) return;
  root.replaceChildren();

  root.appendChild(
    row("account", t("ext.popup.account"), state.marketplaceAccountId ?? t("common.notAvailable")),
  );
  root.appendChild(
    row(
      "capture",
      t("ext.popup.capture"),
      state.capability === "ready" ? t("ext.popup.on") : t("ext.popup.off"),
    ),
  );
  root.appendChild(
    row("last-upload", t("ext.popup.lastUpload"), state.lastUploadAt ?? t("common.notAvailable")),
  );
  root.appendChild(row("queued", t("ext.popup.queued"), String(state.queuedCount)));
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
