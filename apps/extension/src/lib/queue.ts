import { MAX_ATTEMPTS, QUEUE_CAP } from "./constants";
import { dedupKey } from "./dedup";
import { log } from "./observability";
import {
  type DeadLetterItem,
  type DeadLetterSummary,
  KEY_DEADLETTER,
  KEY_QUEUE,
  type KeyValueStore,
  type QueuedItem,
} from "./storage";
import type { CaptureUpload } from "./types";

// The outcome of attempting one upload. The queue reacts to each: accepted →
// remove; revoked → stop the whole flush and fail closed (EXT-001/EXT-009);
// retry → keep with bounded backoff; drop → a permanent client rejection (e.g.
// the target is not Confirmed owned) that must not be retried forever.
export type UploadOutcome = "accepted" | "revoked" | "retry" | "drop";

export type Uploader = (capture: CaptureUpload) => Promise<UploadOutcome>;

// FlushResult reports what a flush did, for the observability seam + popup state.
// Three failure classes are kept SEPARATE (issue #150): `dropped` is a permanent
// 4xx client rejection (gone, never retried); `deadLettered` is a transient
// failure that exhausted its bounded retry budget and was preserved in the
// durable dead-letter store (recoverable); cap-shed backpressure is signalled
// distinctly on enqueue, never here.
export interface FlushResult {
  accepted: number;
  retried: number;
  dropped: number;
  deadLettered: number;
  revoked: boolean;
  remaining: number;
}

// Mutex is a minimal promise-chain lock. The MV3 service worker is single-
// threaded but its runtime-message and alarm handlers interleave at await
// points, so an unsynchronized load→modify→save can last-writer-wins away a
// capture (issue #150). Every queue mutation runs through this ONE lock, so
// concurrent enqueue/enqueue and enqueue/flush are serialized end to end.
class Mutex {
  private tail: Promise<unknown> = Promise.resolve();
  run<T>(fn: () => Promise<T>): Promise<T> {
    // Chain after whatever is currently queued; advance the tail even if `fn`
    // rejects so one failed critical section never wedges the lock.
    const result = this.tail.then(fn, fn);
    this.tail = result.catch(() => undefined);
    return result;
  }
}

// UploadQueue is the service worker's delivery discipline (docs/09): queue in
// storage, dedupe on enqueue, retry with bounded backoff, enforce a cap. It is
// idempotent end to end: enqueuing an identical capture twice yields ONE item,
// and the persisted item replays byte-identically after a restart. EVERY
// mutation is serialized through a single in-worker lock so overlapping runtime
// messages / alarms cannot overwrite one another (issue #150).
export class UploadQueue {
  private readonly lock = new Mutex();

  constructor(private store: KeyValueStore) {}

  private async loadQueue(): Promise<QueuedItem[]> {
    return (await this.store.get<QueuedItem[]>(KEY_QUEUE)) ?? [];
  }

  private async saveQueue(items: QueuedItem[]): Promise<void> {
    await this.store.set(KEY_QUEUE, items);
  }

  private async loadDeadLetter(): Promise<DeadLetterItem[]> {
    return (await this.store.get<DeadLetterItem[]>(KEY_DEADLETTER)) ?? [];
  }

  private async saveDeadLetter(items: DeadLetterItem[]): Promise<void> {
    await this.store.set(KEY_DEADLETTER, items);
  }

  // count / listDeadLetter / deadLetterCount are single-read observations of the
  // durable store. They are not serialized through the lock: a storage read is
  // one atomic get, and reflecting whatever the last committed mutation wrote is
  // exactly the intended point-in-time semantics.
  async count(): Promise<number> {
    return (await this.loadQueue()).length;
  }

  async deadLetterCount(): Promise<number> {
    return (await this.loadDeadLetter()).length;
  }

  async listDeadLetter(): Promise<DeadLetterItem[]> {
    return await this.loadDeadLetter();
  }

  // deadLetterSummaries is the popup-facing projection (stable id + reason token
  // only) — never leaks the capture payload into UI state.
  async deadLetterSummaries(): Promise<DeadLetterSummary[]> {
    return (await this.loadDeadLetter()).map(({ dedupKey, failureReason }) => ({
      dedupKey,
      failureReason,
    }));
  }

  // enqueue adds a capture unless an identical one is already pending (dedup key
  // collision). Returns false when it was a duplicate (no new item) OR when the
  // cap shed it. Enforces the bounded cap by shedding the OLDEST pending item and
  // signalling backpressure via the returned `shed` flag. Serialized so two
  // overlapping enqueues cannot both read the same snapshot (issue #150).
  async enqueue(
    capture: CaptureUpload,
  ): Promise<{ enqueued: boolean; deduped: boolean; shed: boolean }> {
    return this.lock.run(async () => {
      const items = await this.loadQueue();
      const key = dedupKey(capture);
      if (items.some((i) => i.dedupKey === key)) {
        // Idempotent: an identical pending capture already exists — no duplicate.
        return { enqueued: false, deduped: true, shed: false };
      }
      items.push({ dedupKey: key, capture, attempts: 0, enqueuedAt: new Date().toISOString() });
      const shed = capToBound(items);
      await this.saveQueue(items);
      return { enqueued: true, deduped: false, shed };
    });
  }

  // flush attempts to deliver every pending item in order. It STOPS on the first
  // `revoked` outcome (fail closed) so a killed credential cannot keep retrying.
  // Items that exhaust MAX_ATTEMPTS are moved to the DURABLE dead-letter store
  // (issue #150) — preserved with a safe failure reason, never deleted as if
  // accepted. The whole flush runs inside the lock, so a concurrent enqueue is
  // applied to the committed post-flush queue instead of racing it.
  async flush(upload: Uploader): Promise<FlushResult> {
    return this.lock.run(async () => {
      const items = await this.loadQueue();
      const deadLetter = await this.loadDeadLetter();
      const remaining: QueuedItem[] = [];
      const res: FlushResult = {
        accepted: 0,
        retried: 0,
        dropped: 0,
        deadLettered: 0,
        revoked: false,
        remaining: 0,
      };

      for (const item of items) {
        if (res.revoked) {
          remaining.push(item);
          continue;
        }
        const outcome = await upload(item.capture);
        switch (outcome) {
          case "accepted":
            res.accepted++;
            break;
          case "drop":
            // Permanent client rejection (e.g. not Confirmed owned): gone, never
            // retried, and NOT dead-lettered — a distinct, separately-counted class.
            res.dropped++;
            break;
          case "revoked":
            // Fail closed: keep the item, stop delivering, and signal revocation so
            // the caller flips capability → revoked and shows a disabled state.
            res.revoked = true;
            remaining.push(item);
            break;
          case "retry": {
            const attempts = item.attempts + 1;
            if (attempts >= MAX_ATTEMPTS) {
              // Bounded: stop automatic attempts, but PRESERVE the evidence in the
              // durable dead-letter store rather than erasing it (issue #150).
              deadLetter.push({
                dedupKey: item.dedupKey,
                capture: item.capture,
                attempts,
                enqueuedAt: item.enqueuedAt,
                deadLetteredAt: new Date().toISOString(),
                failureReason: "max_attempts_exhausted",
              });
              res.deadLettered++;
              // Structured, locale-neutral transition log (no PII / no marketplace
              // free text) — the dead-letter transition is now as observable as the
              // revoked one: a metric AND a warn line.
              log("warn", "upload_dead_letter", {
                dedupKey: item.dedupKey,
                reason: "max_attempts_exhausted",
              });
            } else {
              remaining.push({ ...item, attempts });
              res.retried++;
            }
            break;
          }
        }
      }

      // No-loss write ORDERING (issue #150, BLOCKER 1): KEY_DEADLETTER and
      // KEY_QUEUE are two SEPARATE chrome.storage.local commits, not an atomic
      // multi-key write, and an MV3 worker can be killed between them. Persist
      // the dead-letter record FIRST, THEN shrink the pending queue: a teardown
      // after commit 1 leaves the exhausted item in BOTH stores (recoverable,
      // and server-idempotent via the preserved dedupKey), never in NEITHER. The
      // inverse order would remove it from pending before it was preserved —
      // permanent silent loss, exactly the failure #150 exists to close.
      if (res.deadLettered > 0) await this.saveDeadLetter(deadLetter);
      await this.saveQueue(remaining);
      res.remaining = remaining.length;
      return res;
    });
  }

  // retryDeadLetter returns a dead-lettered item to the PENDING queue with a
  // fresh retry budget (attempts reset). The original dedupKey is preserved so a
  // successful redelivery is still server-idempotent against any earlier partial
  // upload. Returns `moved: false` (actionable, not a silent success) when no
  // such item exists, and `shed: true` when re-queuing at the cap forced a
  // backpressure shed — the caller MUST emit the queue_backpressure metric so an
  // operator retry that dropped a live capture is never silent (issue #150,
  // BLOCKER 2). ADD-first write ordering (saveQueue then saveDeadLetter) means a
  // mid-op worker kill leaves the item in BOTH stores, never lost.
  async retryDeadLetter(dedupKeyValue: string): Promise<{ moved: boolean; shed: boolean }> {
    return this.lock.run(async () => {
      const deadLetter = await this.loadDeadLetter();
      const idx = deadLetter.findIndex((i) => i.dedupKey === dedupKeyValue);
      if (idx === -1) return { moved: false, shed: false };
      const [item] = deadLetter.splice(idx, 1);
      if (item === undefined) return { moved: false, shed: false };
      const queue = await this.loadQueue();
      let shed = false;
      // Guard the idempotency invariant: if an identical capture is already
      // pending, don't create a duplicate — just drop the dead-letter record.
      if (!queue.some((i) => i.dedupKey === item.dedupKey)) {
        queue.push({
          dedupKey: item.dedupKey,
          capture: item.capture,
          attempts: 0,
          enqueuedAt: item.enqueuedAt,
        });
        shed = capToBound(queue);
        await this.saveQueue(queue);
      }
      await this.saveDeadLetter(deadLetter);
      return { moved: true, shed };
    });
  }

  // discardDeadLetter intentionally removes a dead-lettered item — an explicit,
  // observable operator action (the caller emits the metric). Returns false when
  // there was nothing to discard, so a no-op is never mistaken for a success.
  async discardDeadLetter(dedupKeyValue: string): Promise<boolean> {
    return this.lock.run(async () => {
      const deadLetter = await this.loadDeadLetter();
      const idx = deadLetter.findIndex((i) => i.dedupKey === dedupKeyValue);
      if (idx === -1) return false;
      deadLetter.splice(idx, 1);
      await this.saveDeadLetter(deadLetter);
      return true;
    });
  }
}

// capToBound enforces the bounded cap in place by shedding the OLDEST pending
// item(s) — backpressure, never unbounded growth (docs/09). Returns whether any
// item was shed so the caller can signal the distinct backpressure metric.
function capToBound(items: QueuedItem[]): boolean {
  let shed = false;
  while (items.length > QUEUE_CAP) {
    items.shift();
    shed = true;
  }
  return shed;
}
