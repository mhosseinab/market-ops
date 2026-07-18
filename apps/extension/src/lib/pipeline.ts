import { buildCapture, type CaptureSubRoute } from "./build-capture";
import { type Capability, captureEnabled } from "./capability";
import type { OwnedTargetIndex } from "./owned-targets";
import type { CaptureUpload, ParsedProduct } from "./types";

// The capture-decision seam: given a parsed product, the account's Confirmed
// owned-target index, and the current capability, decide whether a capture may be
// prepared for upload. This is the single choke point where BOTH never-cut gates
// apply together:
//   - Capability UNKNOWN/REVOKED/DISABLED never enables (PRD §4.6);
//   - only a Confirmed owned target enters the commercial path (EXT-004): a
//     Needs Review / rejected / unmapped product is skipped with a reason, never
//     uploaded.
export type CaptureDecision =
  | { action: "enqueue"; capture: CaptureUpload }
  | { action: "skip"; reason: string };

export function prepareCapture(
  product: ParsedProduct,
  index: OwnedTargetIndex,
  capability: Capability,
  capturedAt: string,
  subRoute: CaptureSubRoute = "passive",
): CaptureDecision {
  // Gate 1 — capability. Unknown (never paired), revoked, or disabled: no-op.
  if (!captureEnabled(capability)) {
    return { action: "skip", reason: `capability_${capability}` };
  }
  // Gate 2 — Confirmed owned recognition (EXT-004). No Confirmed target for this
  // product's offer variant ⇒ it is NOT owned commercial data; skip.
  const target = index.resolve(product);
  if (target === null) {
    return { action: "skip", reason: "not_confirmed_owned" };
  }
  const capture = buildCapture(product, target, capturedAt, subRoute);
  if (capture === null) {
    return { action: "skip", reason: "no_offer" };
  }
  return { action: "enqueue", capture };
}
