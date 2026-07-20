import { describe, expect, it, vi } from "vitest";
import { captureEnabled, degradationReason } from "./capability";
import { GatewayClient } from "./gateway";
import { UploadQueue } from "./queue";
import { MemoryStore } from "./storage";
import type { CaptureUpload } from "./types";

function capture(): CaptureUpload {
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
  };
}

function resp(status: number): Response {
  return new Response(
    status === 202 ? JSON.stringify({ deduped: false, quality: "verified" }) : "{}",
    { status },
  );
}

describe("GatewayClient status → queue outcome mapping", () => {
  it("202 → accepted; 401 → revoked; 409/403/400 → drop; 5xx/network → retry", async () => {
    const cases: Array<[number, string]> = [
      [202, "accepted"],
      [401, "revoked"],
      [409, "drop"],
      [403, "drop"],
      [400, "drop"],
      [500, "retry"],
    ];
    for (const [status, want] of cases) {
      const client = new GatewayClient("http://gw", async () => resp(status));
      expect(await client.uploadCapture("cred", capture())).toBe(want);
    }
    // Network failure is transient.
    const netdown = new GatewayClient("http://gw", async () => {
      throw new Error("offline");
    });
    expect(await netdown.uploadCapture("cred", capture())).toBe("retry");
  });

  it("claimPairing throws on a non-200 so pairing never appears to succeed silently", async () => {
    const g = new GatewayClient("http://gw", async () => resp(401));
    await expect(g.claimPairing("bad-code")).rejects.toThrow();
  });
});

describe("GatewayClient.fetchOwnedTargets — credential-scoped owned-target read (#145, EXT-004)", () => {
  const target = {
    id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
    identityId: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
    variantId: "cccccccc-cccc-cccc-cccc-cccccccccccc",
    nativeVariantId: 987654321,
    nativeProductId: 123456789,
    tier: "standard",
    cadenceSeconds: 21600,
    freshnessDeadlineSeconds: 21600,
    active: true,
  };

  it("maps 200 rows to OwnedTarget projections and sends the credential as a Bearer", async () => {
    const fetcher = vi.fn(
      async () => new Response(JSON.stringify({ items: [target] }), { status: 200 }),
    );
    const g = new GatewayClient("http://gw", fetcher);
    const got = await g.fetchOwnedTargets("cap-cred");
    expect(got).toEqual([
      {
        targetId: target.id,
        marketplaceAccountId: target.marketplaceAccountId,
        nativeVariantId: target.nativeVariantId,
        variantId: target.variantId,
      },
    ]);
    expect(fetcher).toHaveBeenCalledWith(
      "http://gw/ext/owned-targets",
      expect.objectContaining({
        method: "GET",
        headers: expect.objectContaining({ authorization: "Bearer cap-cred" }),
      }),
    );
  });

  it("returns null (fail closed) on 401/500/network — never a fabricated empty owned set", async () => {
    for (const status of [401, 500]) {
      const g = new GatewayClient("http://gw", async () => new Response("{}", { status }));
      expect(await g.fetchOwnedTargets("cap-cred")).toBeNull();
    }
    const netdown = new GatewayClient("http://gw", async () => {
      throw new Error("offline");
    });
    expect(await netdown.fetchOwnedTargets("cap-cred")).toBeNull();
  });
});

describe("revoked credential ⇒ upload 401 ⇒ visible disabled state (EXT-001/EXT-009)", () => {
  it("a 401 upload flips the queue to revoked, which disables capture with a reason", async () => {
    const fetcher = vi.fn(async () => resp(401));
    const gateway = new GatewayClient("http://gw", fetcher);
    const queue = new UploadQueue(new MemoryStore());
    await queue.enqueue(capture());

    const result = await queue.flush((c) => gateway.uploadCapture("revoked-cred", c));

    expect(result.revoked).toBe(true);
    // The service worker maps a revoked flush to capability 'revoked'. That state
    // must be a VISIBLE disabled state, never a silent no-op.
    const capability = result.revoked ? "revoked" : "ready";
    expect(captureEnabled(capability)).toBe(false);
    expect(degradationReason(capability)).toBe("credential_revoked");
    // The item is retained (not lost) so a re-pair can resume it.
    expect(await queue.count()).toBe(1);
  });
});
