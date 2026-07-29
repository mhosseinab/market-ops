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

// A BOUNDED, locale-neutral evidence token for one self-revoke attempt (issue
// #149, fix 3). It exists so telemetry can tell a deploy-window unconfirmed
// (route not mounted yet / a proxy 401) from a genuinely unreachable authority,
// WITHOUT ever interpolating a status code or a response body into a label.
export type RevocationEvidence =
  // The contract's only success status, so the real handler demonstrably ran.
  | "confirmed_204"
  // A 401 carrying the authority's own CAPTURE_CREDENTIAL_INVALID verdict.
  | "confirmed_credential_invalid"
  // A 401 with any other code, no body, or an unparseable body: a reverse
  // proxy, a WAF, or a gateway build that has not mounted the route yet.
  | "unconfirmed_generic_401"
  // 404/405 — this gateway build does not serve the route.
  | "unconfirmed_route_missing"
  // Any other status (other 2xx, other 4xx, 5xx, 503).
  | "unconfirmed_status"
  // The request never reached the gateway at all.
  | "unconfirmed_transport";

// The result of one self-revoke attempt. `reachedServer` is deliberately
// SEPARATE from the outcome: a 500 and a network failure are both "pending",
// but only the former proves the gateway is reachable from this device.
export interface RevocationResult {
  outcome: RevocationOutcome;
  reachedServer: boolean;
  evidence: RevocationEvidence;
}

// CAPTURE_CREDENTIAL_INVALID is the gateway's machine-readable verdict that the
// pairing plane itself judged the presented capture credential invalid. It is
// the ONLY code on a 401 that evidences revocation (services/core
// httpapi.captureCredentialInvalidCode).
const CREDENTIAL_INVALID_CODE = "CAPTURE_CREDENTIAL_INVALID";

// errorCode reads the ErrorEnvelope `code` from a response, or null when there
// is no body, the body is not JSON, or it carries no string code. Fail closed:
// an unreadable body is never treated as a verdict.
async function errorCode(resp: Response): Promise<string | null> {
  try {
    const body = (await resp.json()) as { code?: unknown } | null;
    return body && typeof body.code === "string" ? body.code : null;
  } catch {
    return null;
  }
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
  // Confirmation requires POSITIVE PROOF of revocation (issue #149, fix 3). An
  // earlier build treated `resp.ok || resp.status === 401` as confirmed. A probe
  // proved an UNMOUNTED route and a genuine authoritative revocation return
  // byte-identical `401 {"code":"NO_SESSION","message":"authentication
  // required"}` — so any gateway build that had not mounted this route yet
  // (staged rollout, rollback, canary, reverse proxy, WAF) was read as CONFIRMED
  // and the extension destroyed its credential while the server row stayed live.
  // That is issue #149 verbatim, so the rule is now fail-closed:
  //
  //   204 exactly            — CONFIRMED. The contract's only success status, so
  //                            the real handler demonstrably ran. Any OTHER 2xx
  //                            is NOT proof: a proxy "200 OK" page is not the
  //                            handler.
  //   401 + code ===
  //   CAPTURE_CREDENTIAL_INVALID
  //                          — CONFIRMED. The authority's own verdict that the
  //                            credential is not valid. This is what keeps a
  //                            repeated revoke idempotent and stops a pending
  //                            marker stranding on an expired credential.
  //   401, any other/absent/
  //   unparseable code       — NOT confirmed (generic proxy / pre-rollout 401).
  //   404 / 405              — NOT confirmed; the route is not mounted here.
  //   anything else, incl.
  //   5xx / 503              — NOT confirmed, but the server was reached.
  //   transport throw        — NOT confirmed and the server was never reached.
  //
  // The accepted cost is more unconfirmed states during a deploy window; an
  // unconfirmed revocation is quarantined and retried, never silently completed.
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
      return { outcome: "pending", reachedServer: false, evidence: "unconfirmed_transport" };
    }
    if (resp.status === 204) {
      return { outcome: "confirmed", reachedServer: true, evidence: "confirmed_204" };
    }
    if (resp.status === 401) {
      const code = await errorCode(resp);
      if (code === CREDENTIAL_INVALID_CODE) {
        return {
          outcome: "confirmed",
          reachedServer: true,
          evidence: "confirmed_credential_invalid",
        };
      }
      return { outcome: "pending", reachedServer: true, evidence: "unconfirmed_generic_401" };
    }
    if (resp.status === 404 || resp.status === 405) {
      return { outcome: "pending", reachedServer: true, evidence: "unconfirmed_route_missing" };
    }
    return { outcome: "pending", reachedServer: true, evidence: "unconfirmed_status" };
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
