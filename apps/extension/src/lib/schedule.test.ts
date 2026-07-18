import { beforeEach, describe, expect, it, vi } from "vitest";
import { resetCounters } from "./observability";
import {
  type AllocationGateway,
  type AllocationOutcome,
  pendingAllocationGateway,
  runScheduledCycle,
} from "./schedule";

describe("scheduled refresh — EXT-012 server-allocated, circuit-stop honored (never-cut: no self-allocation)", () => {
  beforeEach(() => {
    resetCounters();
  });

  it("the fail-closed default allocation gateway issues ZERO requests every cycle", async () => {
    const captureOne = vi.fn();
    const result = await runScheduledCycle("acct-1", pendingAllocationGateway, captureOne);
    expect(result).toEqual({ requested: 0, stopped: true });
    expect(captureOne).not.toHaveBeenCalled();
  });

  it("NEVER exceeds the server's allocation — issues exactly allowedTargets.length requests", async () => {
    const gateway: AllocationGateway = {
      requestAllocation: async () =>
        ({
          ok: true,
          stopSignal: false,
          allowedTargets: [
            { targetId: "t1", nativeProductId: 1 },
            { targetId: "t2", nativeProductId: 2 },
          ],
        }) satisfies AllocationOutcome,
    };
    const captureOne = vi.fn(async () => {});
    const result = await runScheduledCycle("acct-1", gateway, captureOne);
    expect(result).toEqual({ requested: 2, stopped: false });
    expect(captureOne).toHaveBeenCalledTimes(2);
  });

  it("a stop signal (open breaker) ⇒ ZERO requests this cycle, even with a non-empty allocation", async () => {
    const gateway: AllocationGateway = {
      requestAllocation: async () =>
        ({
          ok: true,
          stopSignal: true,
          allowedTargets: [{ targetId: "t1", nativeProductId: 1 }],
        }) satisfies AllocationOutcome,
    };
    const captureOne = vi.fn(async () => {});
    const result = await runScheduledCycle("acct-1", gateway, captureOne);
    expect(result).toEqual({ requested: 0, stopped: true });
    expect(captureOne).not.toHaveBeenCalled();
  });

  it("a denied allocation is a circuit-stop, not a retry-with-fewer-targets", async () => {
    const gateway: AllocationGateway = {
      requestAllocation: async () => ({ ok: false, reason: "denied" }),
    };
    const captureOne = vi.fn();
    const result = await runScheduledCycle("acct-1", gateway, captureOne);
    expect(result.stopped).toBe(true);
    expect(captureOne).not.toHaveBeenCalled();
  });
});
