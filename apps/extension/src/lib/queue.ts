import { MAX_ATTEMPTS, QUEUE_CAP } from "./constants";
import { dedupKey } from "./dedup";
import { KEY_QUEUE, type KeyValueStore, type QueuedItem } from "./storage";
import type { CaptureUpload } from "./types";

// The outcome of attempting one upload. The queue reacts to each: accepted →
// remove; revoked → stop the whole flush and fail closed (EXT-001/EXT-009);
// retry → keep with bounded backoff; drop → a permanent client rejection (e.g.
// the target is not Confirmed owned) that must not be retried forever.
export type UploadOutcome = "accepted" | "revoked" | "retry" | "drop";

export type Uploader = (capture: CaptureUpload) => Promise<UploadOutcome>;

// FlushResult reports what a flush did, for the observability seam + popup state.
export interface FlushResult {
  accepted: number;
  retried: number;
  dropped: number;
  revoked: boolean;
  remaining: number;
}

// UploadQueue is the service worker's delivery discipline (docs/09): queue in
// storage, dedupe on enqueue, retry with bounded backoff, enforce a cap. It is
// idempotent end to end: enqueuing an identical capture twice yields ONE item,
// and the persisted item replays byte-identically after a restart.
export class UploadQueue {
  constructor(private store: KeyValueStore) {}

  private async load(): Promise<QueuedItem[]> {
    return (await this.store.get<QueuedItem[]>(KEY_QUEUE)) ?? [];
  }

  private async save(items: QueuedItem[]): Promise<void> {
    await this.store.set(KEY_QUEUE, items);
  }

  async count(): Promise<number> {
    return (await this.load()).length;
  }

  // enqueue adds a capture unless an identical one is already pending (dedup key
  // collision). Returns false when it was a duplicate (no new item) OR when the
  // cap shed it. Enforces the bounded cap by shedding the OLDEST pending item and
  // signalling backpressure via the returned `shed` flag.
  async enqueue(
    capture: CaptureUpload,
  ): Promise<{ enqueued: boolean; deduped: boolean; shed: boolean }> {
    const items = await this.load();
    const key = dedupKey(capture);
    if (items.some((i) => i.dedupKey === key)) {
      // Idempotent: an identical pending capture already exists — no duplicate.
      return { enqueued: false, deduped: true, shed: false };
    }
    items.push({ dedupKey: key, capture, attempts: 0, enqueuedAt: new Date().toISOString() });
    let shed = false;
    while (items.length > QUEUE_CAP) {
      items.shift(); // backpressure: shed the oldest, never grow unbounded
      shed = true;
    }
    await this.save(items);
    return { enqueued: true, deduped: false, shed };
  }

  // flush attempts to deliver every pending item in order. It STOPS on the first
  // `revoked` outcome (fail closed) so a killed credential cannot keep retrying.
  // Items that exhaust MAX_ATTEMPTS are parked-dropped (still counted) rather than
  // retried forever.
  async flush(upload: Uploader): Promise<FlushResult> {
    const items = await this.load();
    const remaining: QueuedItem[] = [];
    const res: FlushResult = { accepted: 0, retried: 0, dropped: 0, revoked: false, remaining: 0 };

    for (let idx = 0; idx < items.length; idx++) {
      const item = items[idx];
      if (item === undefined) continue;
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
            res.dropped++; // parked: bounded, never an infinite loop
          } else {
            remaining.push({ ...item, attempts });
            res.retried++;
          }
          break;
        }
      }
    }

    await this.save(remaining);
    res.remaining = remaining.length;
    return res;
  }
}
