import type { OwnedTarget } from "./owned-targets";
import type { PopupState } from "./storage";
import type { ParsedProduct } from "./types";

// Typed message envelope between the content script / popup and the service
// worker. Keeping it explicit means the worker never trusts an untyped payload.
export type ExtMessage =
  | { kind: "capture"; product: ParsedProduct }
  | { kind: "pair"; code: string }
  | { kind: "setEnabled"; enabled: boolean }
  | { kind: "revoke" }
  | { kind: "getState" }
  | { kind: "setOwnedTargets"; targets: OwnedTarget[] };

export type ExtResponse =
  | { ok: true; state: PopupState }
  | { ok: true }
  | { ok: false; error: string };
