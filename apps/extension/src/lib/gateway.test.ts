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

// Issue #149 / PD-4(B): the extension's kill switch must invalidate the
// credential at the AUTHORITY that verifies it, not merely delete the local
// copy. This is the transport half — the status → revocation-outcome mapping.
describe("GatewayClient.revokeCredential — server-side self-revoke (#149, EXT-009)", () => {
  it("presents the credential as a Bearer on the credential-scoped self-revoke route, with no selector", async () => {
    const fetcher = vi.fn(async () => new Response(null, { status: 204 }));
    const client = new GatewayClient("http://gw", fetcher);

    expect((await client.revokeCredential("cap-cred")).outcome).toBe("confirmed");

    const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("http://gw/ext/pairing/self-revoke");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>).authorization).toBe("Bearer cap-cred");
    // Identity quarantine: the credential to revoke is derived SERVER-side from
    // the Bearer. The extension never sends a body/selector naming a credential
    // or account — it cannot revoke another device's or account's pairing.
    expect(init.body).toBeUndefined();
  });

  // Issue #149, fix 3: POSITIVE PROOF of revocation. NEGATIVES FIRST — every
  // 401 that is NOT the authority's own verdict must stay unconfirmed.
  //
  // A probe proved an UNMOUNTED gateway route and a genuine authoritative
  // revocation returned byte-identical `401 {"code":"NO_SESSION"}`. Any gateway
  // build that has not mounted the route (staged rollout, rollback, canary,
  // reverse proxy, WAF) was therefore read as CONFIRMED, and the extension
  // destroyed its credential while the server row stayed live — issue #149
  // verbatim. The cost of the fail-closed reading (more unconfirmed states
  // during a deploy window) is accepted.
  it("NEGATIVE: a GENERIC 401 (proxy / pre-rollout gateway / unmounted route) is NOT a confirmation", async () => {
    const generic = [
      JSON.stringify({ code: "NO_SESSION", message: "authentication required" }),
      JSON.stringify({ code: "FORBIDDEN" }),
      "", // no body at all — a bare proxy refusal
      "<html>401 Unauthorized</html>", // unparseable body
    ];
    for (const body of generic) {
      const client = new GatewayClient(
        "http://gw",
        async () => new Response(body === "" ? null : body, { status: 401 }),
      );
      const r = await client.revokeCredential("cap-cred");
      expect(r.outcome).toBe("pending");
      expect(r.reachedServer).toBe(true);
      expect(r.evidence).toBe("unconfirmed_generic_401");
    }
  });

  it("NEGATIVE: 404/405 (the route is not mounted on this gateway build) is NOT a confirmation", async () => {
    for (const status of [404, 405]) {
      const client = new GatewayClient("http://gw", async () => new Response("{}", { status }));
      const r = await client.revokeCredential("cap-cred");
      expect(r.outcome).toBe("pending");
      expect(r.evidence).toBe("unconfirmed_route_missing");
    }
  });

  it("NEGATIVE: any OTHER 2xx is not proof the real handler ran — only the contract's 204 is", async () => {
    // A reverse proxy / captive portal "200 OK" page is not the handler.
    for (const status of [200, 201, 202]) {
      const client = new GatewayClient("http://gw", async () => new Response("{}", { status }));
      const r = await client.revokeCredential("cap-cred");
      expect(r.outcome).toBe("pending");
      expect(r.evidence).toBe("unconfirmed_status");
    }
  });

  it("network error, 5xx, and 503 are NOT confirmations — they stay pending", async () => {
    for (const status of [500, 502, 503, 400, 403]) {
      const client = new GatewayClient("http://gw", async () => new Response("{}", { status }));
      expect((await client.revokeCredential("cap-cred")).outcome).toBe("pending");
    }
    const offline = new GatewayClient("http://gw", async () => {
      throw new Error("offline");
    });
    const r = await offline.revokeCredential("cap-cred");
    expect(r.outcome).toBe("pending");
    expect(r.evidence).toBe("unconfirmed_transport");
  });

  // POSITIVE PROOF, both admissible forms.
  it("204 — the contract's ONLY success status — is a confirmation", async () => {
    const client = new GatewayClient("http://gw", async () => new Response(null, { status: 204 }));
    const r = await client.revokeCredential("cap-cred");
    expect(r.outcome).toBe("confirmed");
    expect(r.evidence).toBe("confirmed_204");
  });

  it("a 401 carrying the authority's OWN CAPTURE_CREDENTIAL_INVALID verdict is a confirmation", async () => {
    // This is what keeps a REPEATED revoke idempotent and stops a pending marker
    // stranding forever on an already-revoked or expired credential.
    const client = new GatewayClient(
      "http://gw",
      async () =>
        new Response(JSON.stringify({ code: "CAPTURE_CREDENTIAL_INVALID", message: "not valid" }), {
          status: 401,
        }),
    );
    const r = await client.revokeCredential("cap-cred");
    expect(r.outcome).toBe("confirmed");
    expect(r.evidence).toBe("confirmed_credential_invalid");
  });

  it("the evidence token is a BOUNDED locale-neutral label — never an interpolated status or body", async () => {
    const bounded = new Set([
      "confirmed_204",
      "confirmed_credential_invalid",
      "unconfirmed_generic_401",
      "unconfirmed_route_missing",
      "unconfirmed_status",
      "unconfirmed_transport",
    ]);
    for (const status of [204, 401, 404, 405, 200, 418, 500, 503]) {
      const client = new GatewayClient(
        "http://gw",
        async () => new Response(status === 204 ? null : "{}", { status }),
      );
      const r = await client.revokeCredential("cap-cred");
      // Membership in a CLOSED set is the anti-interpolation proof: an
      // interpolated status/body could not be a member. (`confirmed_204` names
      // the contract's success status as a fixed label — it is a member, not an
      // interpolation, which is why the closed set is the assertion.)
      expect(bounded.has(r.evidence)).toBe(true);
    }
    // Explicitly: an arbitrary/unknown status never leaks into the label.
    const teapot = new GatewayClient("http://gw", async () => new Response("{}", { status: 418 }));
    expect((await teapot.revokeCredential("cap-cred")).evidence).toBe("unconfirmed_status");
  });

  // Issue #149 (F4). The caller needs to distinguish "the server answered, just
  // not authoritatively" from "we never reached the server at all", because the
  // pending marker's expiry shortcut is judged against the DEVICE clock. A
  // device that has never reached the gateway has no evidence its clock is
  // right, so it may not conclude the credential expired.
  it("reports whether the SERVER was actually reached, separately from the outcome", async () => {
    for (const status of [204, 401, 500, 503]) {
      const client = new GatewayClient(
        "http://gw",
        // A 204 carries NO body (constructing one with a body throws).
        async () => new Response(status === 204 ? null : "{}", { status }),
      );
      expect((await client.revokeCredential("cap-cred")).reachedServer).toBe(true);
    }
    const offline = new GatewayClient("http://gw", async () => {
      throw new Error("offline");
    });
    expect((await offline.revokeCredential("cap-cred")).reachedServer).toBe(false);
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
