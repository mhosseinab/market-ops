import type { ExtMessage, ExtResponse } from "../lib/messages";
import type { PopupState } from "../lib/storage";

// Popup (EXT-009 kill switch): a VISIBLE, real state — account, capture toggle,
// last upload, queued count, and the degradation reason. Disabling capture must
// produce a visibly disabled state, never a silent no-op. The popup only reads
// and forwards; it never computes commercial values.

function send(msg: ExtMessage): Promise<ExtResponse> {
  return chrome.runtime.sendMessage(msg) as Promise<ExtResponse>;
}

// A stable token → display text map. The extension popup is outside the web copy
// catalog; these are the human labels for the kill-switch states.
const DEGRADATION_TEXT: Record<string, string> = {
  not_paired: "Not paired — enter a pairing code.",
  credential_revoked: "Access revoked — re-pair to resume.",
  capture_disabled: "Capture is turned off.",
};

function render(state: PopupState): void {
  const root = document.getElementById("root");
  if (!root) return;
  root.replaceChildren();

  root.appendChild(row("Account", state.marketplaceAccountId ?? "—"));
  root.appendChild(row("Capture", state.capability === "ready" ? "On" : "Off"));
  root.appendChild(row("Last upload", state.lastUploadAt ?? "—"));
  root.appendChild(row("Queued", String(state.queuedCount)));
  if (state.degradation) {
    const note = document.createElement("p");
    note.dataset.role = "degradation";
    note.dataset.reason = state.degradation;
    note.textContent = DEGRADATION_TEXT[state.degradation] ?? state.degradation;
    root.appendChild(note);
  }

  // Pairing input (shown when not yet paired / revoked).
  if (state.capability === "unknown" || state.capability === "revoked") {
    const input = document.createElement("input");
    input.id = "pairing-code";
    input.placeholder = "Pairing code";
    const pairBtn = button("Pair", async () => {
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
      state.capability === "ready" ? "Disable capture" : "Enable capture",
      async () => {
        await send({ kind: "setEnabled", enabled: state.capability !== "ready" });
        await refresh();
      },
    );
    toggle.dataset.role = "capture-toggle";
    root.appendChild(toggle);
  }
}

function row(label: string, value: string): HTMLElement {
  const el = document.createElement("div");
  el.dataset.role = "row";
  el.dataset.field = label.toLowerCase().replace(/\s+/g, "-");
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
