import type { OwnedTarget } from "./owned-targets";
import type { UploadOutcome } from "./queue";
import type { CaptureUpload, ObservationTargetList, PairingCredential } from "./types";

// Gateway transport. The extension talks to the market-ops gateway over exactly
// the routes its capture credential authorizes: claim a pairing code for a
// scoped capture credential, upload a capture, read its own owned targets, and
// revoke ITSELF (#149). The base URL is injected at build time
// (VITE_GATEWAY_BASE_URL); its host is added to host_permissions at deploy.

export type Fetcher = (input: string, init?: RequestInit) => Promise<Response>;

// The outcome of a server-side self-revoke (#149, EXT-009):
//   confirmed — the AUTHORITY says this credential no longer authorizes
//               anything, so the local credential material may be discarded;
//   pending   — no authoritative answer was obtained; the revocation is durably
//               recorded and retried, and capture stays disabled meanwhile.
export type RevocationOutcome = "confirmed" | "pending";

// The result of one self-revoke attempt. `reachedServer` is deliberately
// SEPARATE from the outcome: a 500 and a network failure are both "pending",
// but only the former proves the gateway is reachable from this device. The
// pending marker's expiry shortcut is judged against the DEVICE clock, so it
// requires that proof before it may finalize (issue #149).
export interface RevocationResult {
  outcome: RevocationOutcome;
  reachedServer: boolean;
}

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

  // revokeCredential revokes THIS credential at the server (#149, EXT-009). It
  // presents the capture credential as a Bearer on the credential-scoped
  // self-revoke route and sends NO body: the credential to revoke is derived
  // server-side from the Bearer, so the extension can never revoke another
  // device's or account's pairing (identity quarantine).
  //
  // Status → outcome, and why:
  //   204 (and any 2xx) — the authority revoked it: CONFIRMED.
  //   401               — the credential does not authenticate AT ALL any more
  //                       (already revoked, expired, or unknown). That is the
  //                       same end state a successful revoke produces, so it is
  //                       CONFIRMED — treating it as pending would strand a
  //                       pending marker forever on an expired credential.
  //                       The gateway upholds the other half of this bargain:
  //                       it answers 503/500 (never 401) for an unconfigured
  //                       plane or a transient store failure, so a 401 always
  //                       means the authority genuinely does not recognise the
  //                       credential.
  //   anything else (4xx other than 401, 5xx, 503, network/transport failure)
  //                     — NOT an authoritative statement that the credential is
  //                       dead, so it stays PENDING: capture remains disabled,
  //                       the credential material is retained, and the revoke is
  //                       retried. Never a silent "assume it worked".
  async revokeCredential(credential: string): Promise<RevocationResult> {
    let resp: Response;
    try {
      resp = await this.fetcher(`${this.baseUrl}/ext/pairing/self-revoke`, {
        method: "POST",
        headers: { authorization: `Bearer ${credential}` },
      });
    } catch {
      // Network/transport error — no authoritative answer, and no evidence the
      // gateway is reachable from this device at all.
      return { outcome: "pending", reachedServer: false };
    }
    if (resp.ok || resp.status === 401) return { outcome: "confirmed", reachedServer: true };
    return { outcome: "pending", reachedServer: true };
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
      variantId: t.variantId,
    }));
  }
}
