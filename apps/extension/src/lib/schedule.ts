import { incr } from "./observability";

// Bounded scheduled refresh (EXT-012): opt-in, server-allocated, circuit-stop
// honored WITHIN one allocation cycle. `chrome.alarms` is only a scheduling
// HINT (docs/09 closing note) — the extension NEVER self-allocates a crawl; it
// asks the server each cycle and NEVER issues more requests than the server
// allowed, and NEVER attaches a DK session credential/cookie to a scheduled
// request (unlike the content script's deliberate own-session passive-capture
// read — docs/12).
//
// The server-side allocation endpoint does not exist in gen/ts yet (same S37
// dependency chain as watchlist.ts — scheduled refresh only makes sense over
// watchlist targets, EXT-007). This is the SAME fail-closed-seam discipline:
// no allocation ⇒ zero requests, by construction, every cycle, until the real
// endpoint lands.

export interface AllocationTarget {
  readonly targetId: string;
  readonly nativeProductId: number;
}

export type AllocationOutcome =
  | { ok: true; allowedTargets: readonly AllocationTarget[]; stopSignal: boolean }
  | { ok: false; reason: "endpoint_unavailable" | "denied" };

export interface AllocationGateway {
  requestAllocation(marketplaceAccountId: string): Promise<AllocationOutcome>;
}

// pendingAllocationGateway: fail-closed default — no server allocation
// endpoint exists yet, so every cycle is a circuit-stop by construction (zero
// requests). Swapping in the real gateway is the single change needed once the
// allocation endpoint is verified and contracted.
export const pendingAllocationGateway: AllocationGateway = {
  async requestAllocation(): Promise<AllocationOutcome> {
    return { ok: false, reason: "endpoint_unavailable" };
  },
};

export type CaptureOneFn = (target: AllocationTarget) => Promise<void>;

export interface CycleResult {
  readonly requested: number;
  readonly stopped: boolean;
}

// runScheduledCycle executes ONE allocation cycle. It never exceeds the
// server's allocation, and honors a stop signal by issuing ZERO further
// requests for the rest of the cycle — even if `allowedTargets` were non-empty
// (a stop signal takes precedence over any already-granted allocation).
export async function runScheduledCycle(
  marketplaceAccountId: string,
  gateway: AllocationGateway,
  captureOne: CaptureOneFn,
): Promise<CycleResult> {
  incr("schedule_cycle");
  const allocation = await gateway.requestAllocation(marketplaceAccountId);

  if (!allocation.ok) {
    incr("schedule_request_denied", { reason: allocation.reason });
    return { requested: 0, stopped: true };
  }
  if (allocation.stopSignal) {
    incr("schedule_circuit_stop");
    return { requested: 0, stopped: true };
  }

  let requested = 0;
  for (const target of allocation.allowedTargets) {
    await captureOne(target);
    requested++;
  }
  return { requested, stopped: false };
}
