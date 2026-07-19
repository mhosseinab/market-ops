import type { OwnedTarget } from "./owned-targets";
import type { UploadOutcome } from "./queue";
import type { CaptureUpload, ObservationTargetList, PairingCredential } from "./types";

// Gateway transport. The extension talks to the market-ops gateway over exactly
// two routes: claim a pairing code for a scoped capture credential, and upload a
// capture authenticated by that credential. The base URL is injected at build
// time (VITE_GATEWAY_BASE_URL); its host is added to host_permissions at deploy.

export type Fetcher = (input: string, init?: RequestInit) => Promise<Response>;

export class GatewayClient {
  constructor(
    private baseUrl: string,
    private fetcher: Fetcher = globalThis.fetch.bind(globalThis),
  ) {}

  // claimPairing exchanges a short-lived pairing code for a scoped capture
  // credential (EXT-001). No session/cookie is sent — the extension is not
  // logged in. Throws on any non-200 so pairing never appears to succeed silently.
  async claimPairing(code: string): Promise<PairingCredential> {
    const resp = await this.fetcher(`${this.baseUrl}/ext/pairing/claim`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ code }),
    });
    if (!resp.ok) {
      throw new Error(`pairing claim failed: ${resp.status}`);
    }
    return (await resp.json()) as PairingCredential;
  }

  // uploadCapture posts one allow-listed capture with the capture credential as a
  // Bearer. It maps the HTTP status to a queue outcome:
  //   202 accepted (or deduped) — delivered;
  //   401 revoked/expired credential — fail closed (EXT-001 kill switch);
  //   400/403/409 permanent client rejection (incomplete, cross-account,
  //       non-Confirmed target) — drop, never retry;
  //   5xx / network — transient, retry with bounded backoff.
  async uploadCapture(credential: string, capture: CaptureUpload): Promise<UploadOutcome> {
    let resp: Response;
    try {
      resp = await this.fetcher(`${this.baseUrl}/observation/capture`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          authorization: `Bearer ${credential}`,
        },
        body: JSON.stringify(capture),
      });
    } catch {
      return "retry"; // network error — transient
    }
    if (resp.status === 202) return "accepted";
    if (resp.status === 401) return "revoked";
    if (resp.status === 400 || resp.status === 403 || resp.status === 409) return "drop";
    return "retry";
  }

  // fetchOwnedTargets reads the paired account's Confirmed owned observation
  // targets with the capture credential as a Bearer (#145, EXT-004). The
  // marketplace account is derived SERVER-SIDE from the credential — the
  // extension never selects it. It FAILS CLOSED: any non-200 (revoked/expired
  // 401, 5xx) or a network error returns null so the caller CLEARS its local
  // owned-target projection rather than fabricating or retaining an owned set —
  // an empty/unknown index uploads nothing (capture stays disabled). It maps the
  // server's targets to the minimal {targetId, marketplaceAccountId,
  // nativeVariantId} projection the gate needs.
  async fetchOwnedTargets(credential: string): Promise<OwnedTarget[] | null> {
    let resp: Response;
    try {
      resp = await this.fetcher(`${this.baseUrl}/ext/owned-targets`, {
        method: "GET",
        headers: { authorization: `Bearer ${credential}` },
      });
    } catch {
      return null; // network error — fail closed, never a guessed owned set
    }
    if (resp.status !== 200) return null;
    let body: ObservationTargetList;
    try {
      body = (await resp.json()) as ObservationTargetList;
    } catch {
      return null;
    }
    if (!body || !Array.isArray(body.items)) return null;
    return body.items.map((t) => ({
      targetId: t.id,
      marketplaceAccountId: t.marketplaceAccountId,
      nativeVariantId: t.nativeVariantId,
    }));
  }
}
