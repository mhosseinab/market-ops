import type { UploadOutcome } from "./queue";
import type { CaptureUpload, PairingCredential } from "./types";

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
}
