import { describe, expect, it } from "vitest";
import { MAX_ATTEMPTS, QUEUE_CAP } from "./constants";
import { dedupKey } from "./dedup";
import { type UploadOutcome, UploadQueue } from "./queue";
import {
  type DeadLetterItem,
  KEY_DEADLETTER,
  KEY_QUEUE,
  type KeyValueStore,
  MemoryStore,
  type QueuedItem,
} from "./storage";
import type { CaptureUpload } from "./types";

// CrashStore simulates an MV3 worker being killed mid-flush: it commits the
// first N `set()` calls to a real MemoryStore, then throws on the next one —
// reproducing a teardown BETWEEN the two cross-key storage commits (KEY_QUEUE
// and KEY_DEADLETTER are NOT an atomic multi-key write).
class CrashStore implements KeyValueStore {
  private readonly inner = new MemoryStore();
  private budget = Number.POSITIVE_INFINITY;
  crashAfter(writes: number): void {
    this.budget = writes;
  }
  async get<T>(key: string): Promise<T | undefined> {
    return this.inner.get<T>(key);
  }
  async set<T>(key: string, value: T): Promise<void> {
    if (this.budget <= 0) throw new Error("worker killed mid-commit");
    this.budget--;
    await this.inner.set(key, value);
  }
  async remove(key: string): Promise<void> {
    await this.inner.remove(key);
  }
  async snapshot(): Promise<Record<string, unknown>> {
    return this.inner.snapshot();
  }
}

function capture(overrides: Partial<CaptureUpload> = {}): CaptureUpload {
  return {
    marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
    targetId: "22222222-2222-2222-2222-222222222222",
    nativeVariantId: 987654321,
    subRoute: "passive",
    sourceType: "public-web-endpoint",
    parserVersion: "dk-product@1.0.0",
    evidenceRef: "https://www.digikala.com/product/dkp-2345678/",
    availabilityStatus: "in_stock",
    capturedAt: "2026-07-18T10:00:00Z",
    confidence: "verified",
    price: { text: "125000000 IRR-rial", value: "125000000", unit: "IRR-rial" },
    ...overrides,
  };
}

describe("UploadQueue — idempotent offline retry (OBS-008, docs/09)", () => {
  it("produces a STABLE dedup key for a byte-identical replay", () => {
    expect(dedupKey(capture())).toBe(dedupKey(capture()));
    // A different captured instant is distinct evidence, not a replay.
    expect(dedupKey(capture())).not.toBe(dedupKey(capture({ capturedAt: "2026-07-18T11:00:00Z" })));
  });

  it("enqueuing an identical capture twice yields ONE pending item (no duplicate)", async () => {
    const q = new UploadQueue(new MemoryStore());
    const first = await q.enqueue(capture());
    const second = await q.enqueue(capture());
    expect(first).toEqual({ enqueued: true, deduped: false, shed: false });
    expect(second).toEqual({ enqueued: false, deduped: true, shed: false });
    expect(await q.count()).toBe(1);
  });

  it("replays after an offline failure until accepted, then removes the item", async () => {
    const store = new MemoryStore();
    const q = new UploadQueue(store);
    await q.enqueue(capture());

    // First flush: transient failure (offline) → item retried, still queued.
    let r = await q.flush(async () => "retry");
    expect(r.retried).toBe(1);
    expect(await q.count()).toBe(1);

    // Second flush: accepted → removed, zero remaining.
    r = await q.flush(async () => "accepted");
    expect(r.accepted).toBe(1);
    expect(await q.count()).toBe(0);
  });

  it("STOPS the flush and fails closed on a revoked outcome (EXT-001 kill switch)", async () => {
    const q = new UploadQueue(new MemoryStore());
    await q.enqueue(capture());
    await q.enqueue(capture({ capturedAt: "2026-07-18T11:00:00Z" }));
    const r = await q.flush(async () => "revoked");
    expect(r.revoked).toBe(true);
    expect(r.accepted).toBe(0);
    // Both items are retained (nothing delivered under a killed credential).
    expect(await q.count()).toBe(2);
  });

  it("moves an item to a DURABLE dead-letter state after MAX_ATTEMPTS instead of retrying forever (bounded, not erased)", async () => {
    const q = new UploadQueue(new MemoryStore());
    await q.enqueue(capture());
    const always: () => Promise<UploadOutcome> = async () => "retry";
    for (let i = 0; i < 5; i++) await q.flush(always);
    // No longer PENDING (bounded, not an infinite loop)...
    expect(await q.count()).toBe(0);
    // ...but durably PRESERVED in dead-letter, never silently erased (issue #150).
    const dl = await q.listDeadLetter();
    expect(dl).toHaveLength(1);
    expect(dl[0]?.failureReason).toBe("max_attempts_exhausted");
  });
});

describe("UploadQueue — atomic mutation under concurrency (issue #150)", () => {
  it("(a) two concurrent enqueues of distinct captures BOTH persist (no lost update)", async () => {
    const store = new MemoryStore();
    const q = new UploadQueue(store);
    // Fire both WITHOUT awaiting the first — a last-writer-wins bug drops one.
    const [r1, r2] = await Promise.all([
      q.enqueue(capture()),
      q.enqueue(capture({ capturedAt: "2026-07-18T11:00:00Z" })),
    ]);
    expect(r1.enqueued).toBe(true);
    expect(r2.enqueued).toBe(true);
    expect(await q.count()).toBe(2);
  });

  it("(b) an enqueue during an in-flight flush is NOT lost, and an accepted item is NOT resurrected", async () => {
    const store = new MemoryStore();
    const q = new UploadQueue(store);
    await q.enqueue(capture()); // item B (accepted mid-flush)

    let release: () => void = () => {};
    const barrier = new Promise<void>((r) => {
      release = r;
    });
    const uploader = async (): Promise<UploadOutcome> => {
      await barrier; // block the flush's uploader on a deterministic barrier
      return "accepted";
    };

    const flushP = q.flush(uploader); // holds the queue while B's upload blocks
    // A NEW capture A races the in-flight flush.
    const enqueueP = q.enqueue(capture({ capturedAt: "2026-07-18T11:00:00Z" }));
    release(); // let B's upload complete (accepted)
    await Promise.all([flushP, enqueueP]);

    // B was accepted and removed (not resurrected); A survived (not lost).
    expect(await q.count()).toBe(1);
    const items = (await store.get<QueuedItem[]>(KEY_QUEUE)) ?? [];
    expect(items.map((i) => i.capture.capturedAt)).toEqual(["2026-07-18T11:00:00Z"]);
  });

  it("(e) converges across a worker restart (new UploadQueue over the SAME durable store) — no loss, no double-accept", async () => {
    const store = new MemoryStore();
    const q1 = new UploadQueue(store);
    await q1.enqueue(capture());

    // "Restart": a brand-new queue instance over the SAME persisted store.
    const q2 = new UploadQueue(store);
    expect(await q2.count()).toBe(1); // survived the restart
    const before = (await store.get<QueuedItem[]>(KEY_QUEUE)) ?? [];

    // Upload accepted after the restart → removed, converges to empty.
    const q3 = new UploadQueue(store);
    const r = await q3.flush(async () => "accepted");
    expect(r.accepted).toBe(1);
    expect(await q3.count()).toBe(0);

    // A crash-after-upload replay re-enqueues the SAME capture → the dedup key is
    // byte-identical, so the backend dedupes it (no double-accept).
    await q3.enqueue(capture());
    const after = (await store.get<QueuedItem[]>(KEY_QUEUE)) ?? [];
    expect(after[0]?.dedupKey).toBe(before[0]?.dedupKey);
  });
});

describe("UploadQueue — dead-letter durability, recovery, and classification (issue #150)", () => {
  it("(c) exhausted transient failures land in a DURABLE, inspectable dead-letter state — distinct from a permanent 4xx drop", async () => {
    const q = new UploadQueue(new MemoryStore());
    await q.enqueue(capture());
    let last: Awaited<ReturnType<UploadQueue["flush"]>> | undefined;
    for (let i = 0; i < MAX_ATTEMPTS; i++) last = await q.flush(async () => "retry");

    expect(await q.count()).toBe(0); // no longer pending
    expect(last?.deadLettered).toBe(1); // classified as dead-letter…
    expect(last?.dropped).toBe(0); // …NOT as a permanent 4xx drop
    const dl = await q.listDeadLetter();
    expect(dl).toHaveLength(1);
    expect(dl[0]?.failureReason).toBe("max_attempts_exhausted");
    // Durable + byte-identical: the capture (and its dedup key) is preserved.
    expect(dl[0]?.capture.capturedAt).toBe("2026-07-18T10:00:00Z");
    expect(dl[0]?.dedupKey).toBe(dedupKey(capture()));
  });

  it("(d) a dead-lettered item can be explicitly RETRIED back to pending, then accepted", async () => {
    const q = new UploadQueue(new MemoryStore());
    await q.enqueue(capture());
    for (let i = 0; i < MAX_ATTEMPTS; i++) await q.flush(async () => "retry");

    const [dl] = await q.listDeadLetter();
    expect(dl).toBeDefined();
    const retried = await q.retryDeadLetter(dl?.dedupKey ?? "");
    expect(retried.moved).toBe(true);
    expect(retried.shed).toBe(false);
    expect(await q.deadLetterCount()).toBe(0);
    expect(await q.count()).toBe(1); // back to pending, attempts reset

    const r = await q.flush(async () => "accepted");
    expect(r.accepted).toBe(1);
    expect(await q.count()).toBe(0);
  });

  it("(d) a dead-lettered item can be explicitly DISCARDED (removed intentionally, observable); a missing key is an actionable false, not silent success", async () => {
    const q = new UploadQueue(new MemoryStore());
    await q.enqueue(capture());
    for (let i = 0; i < MAX_ATTEMPTS; i++) await q.flush(async () => "retry");

    const [dl] = await q.listDeadLetter();
    const discarded = await q.discardDeadLetter(dl?.dedupKey ?? "");
    expect(discarded).toBe(true);
    expect(await q.deadLetterCount()).toBe(0);
    expect(await q.count()).toBe(0);
    // A no-op discard reports false (never a swallowed default treated as success).
    expect(await q.discardDeadLetter("does-not-exist")).toBe(false);
    expect(await q.retryDeadLetter("does-not-exist")).toEqual({ moved: false, shed: false });
  });

  it("(f) cap-shed backpressure, permanent 4xx drop, and exhausted dead-letter are THREE separately classified outcomes", async () => {
    // Permanent 4xx drop: dropped, NOT dead-lettered.
    const qDrop = new UploadQueue(new MemoryStore());
    await qDrop.enqueue(capture());
    const dropRes = await qDrop.flush(async () => "drop");
    expect(dropRes.dropped).toBe(1);
    expect(dropRes.deadLettered).toBe(0);
    expect(await qDrop.deadLetterCount()).toBe(0);
    expect(await qDrop.count()).toBe(0);

    // Exhausted transient: dead-lettered, NOT dropped.
    const qDead = new UploadQueue(new MemoryStore());
    await qDead.enqueue(capture());
    let exhaustRes: Awaited<ReturnType<UploadQueue["flush"]>> | undefined;
    for (let i = 0; i < MAX_ATTEMPTS; i++) exhaustRes = await qDead.flush(async () => "retry");
    expect(exhaustRes?.deadLettered).toBe(1);
    expect(exhaustRes?.dropped).toBe(0);
    expect(await qDead.deadLetterCount()).toBe(1);

    // Cap-shed backpressure: signalled on enqueue, distinct from either terminal.
    const qShed = new UploadQueue(new MemoryStore());
    let shedSeen = false;
    for (let i = 0; i <= QUEUE_CAP; i++) {
      const r = await qShed.enqueue(capture({ capturedAt: `2026-07-18T10:00:${i}` }));
      if (r.shed) shedSeen = true;
    }
    expect(shedSeen).toBe(true);
    expect(await qShed.count()).toBe(QUEUE_CAP);
    expect(await qShed.deadLetterCount()).toBe(0); // shedding is NOT dead-lettering
  });

  it("(BLOCKER1) a worker kill BETWEEN flush's two storage commits never loses the exhausted item", async () => {
    const store = new CrashStore();
    const q = new UploadQueue(store);
    await q.enqueue(capture());
    // Drive to the final, exhausting attempt without crashing (attempts 1..MAX-1).
    for (let i = 0; i < MAX_ATTEMPTS - 1; i++) await q.flush(async () => "retry");

    // Arm the crash: let the FIRST commit of the exhausting flush land, then kill
    // the worker before the SECOND. No-loss ordering must persist the dead-letter
    // record BEFORE it shrinks the pending queue, so the item survives in at
    // least one store. (Buggy order — shrink pending first — loses it entirely.)
    store.crashAfter(1);
    await expect(q.flush(async () => "retry")).rejects.toThrow();

    const key = dedupKey(capture());
    const pending = (await store.get<QueuedItem[]>(KEY_QUEUE)) ?? [];
    const dead = (await store.get<DeadLetterItem[]>(KEY_DEADLETTER)) ?? [];
    const inPending = pending.some((i) => i.dedupKey === key);
    const inDead = dead.some((i) => i.dedupKey === key);
    // Recoverable from exactly one of the two stores — never silently lost. The
    // preserved dedupKey makes any transient duplicate server-idempotent.
    expect(inPending || inDead).toBe(true);
  });

  it("(BLOCKER2) retrying a dead-letter item at cap sheds the oldest pending capture and SIGNALS backpressure (not silent)", async () => {
    const q = new UploadQueue(new MemoryStore());
    // Produce one dead-letter item.
    await q.enqueue(capture());
    for (let i = 0; i < MAX_ATTEMPTS; i++) await q.flush(async () => "retry");
    const [dl] = await q.listDeadLetter();
    expect(dl).toBeDefined();

    // Fill the pending queue to the bound with distinct live captures.
    for (let i = 0; i < QUEUE_CAP; i++) {
      await q.enqueue(capture({ capturedAt: `2026-07-18T10:00:${i}` }));
    }
    expect(await q.count()).toBe(QUEUE_CAP);

    const r = await q.retryDeadLetter(dl?.dedupKey ?? "");
    expect(r.moved).toBe(true);
    expect(r.shed).toBe(true); // backpressure propagated, never a silent drop
    expect(await q.count()).toBe(QUEUE_CAP);
  });
});
