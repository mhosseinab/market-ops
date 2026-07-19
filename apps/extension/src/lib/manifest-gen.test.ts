import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  assertManifestScoped,
  DIGIKALA_HOST_PERMISSIONS,
  deriveManifest,
  FORBIDDEN_MANIFEST_ENTRIES,
  gatewayHostPermission,
  resolveGatewayBaseUrl,
} from "../../scripts/manifest.mjs";

// EXT-001/§14: the packaged extension must reach EXACTLY its configured first-party
// gateway origin, and NOTHING wider. host_permissions is generated at build time
// from VITE_GATEWAY_BASE_URL — the static source manifest never carries the gateway
// origin (least privilege). These tests pin the validation + derivation + the
// packaged-artifact cross-boundary assertion.

const extRoot = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const sourceManifest = JSON.parse(readFileSync(join(extRoot, "public", "manifest.json"), "utf8"));

describe("resolveGatewayBaseUrl — production requires an explicit gateway (NEGATIVE first)", () => {
  it("FAILS closed when VITE_GATEWAY_BASE_URL is UNSET in production", () => {
    expect(() => resolveGatewayBaseUrl({}, "production")).toThrow();
  });

  it("FAILS closed when VITE_GATEWAY_BASE_URL is EMPTY in production", () => {
    expect(() => resolveGatewayBaseUrl({ VITE_GATEWAY_BASE_URL: "" }, "production")).toThrow();
    expect(() => resolveGatewayBaseUrl({ VITE_GATEWAY_BASE_URL: "   " }, "production")).toThrow();
  });

  it("returns the explicit gateway in production when set", () => {
    expect(
      resolveGatewayBaseUrl({ VITE_GATEWAY_BASE_URL: "https://gateway.example" }, "production"),
    ).toBe("https://gateway.example");
  });

  it("defaults to the loopback gateway ONLY for the dev/unpacked flow", () => {
    expect(resolveGatewayBaseUrl({}, "development")).toBe("http://localhost:8080");
  });

  it("honours an explicit dev gateway override without inventing a default", () => {
    expect(
      resolveGatewayBaseUrl({ VITE_GATEWAY_BASE_URL: "http://localhost:9000" }, "development"),
    ).toBe("http://localhost:9000");
  });
});

describe("gatewayHostPermission — validation (NEGATIVE first)", () => {
  it("rejects the total wildcard *://*/*", () => {
    expect(() => gatewayHostPermission("*://*/*")).toThrow();
  });

  it("rejects an https wildcard host https://*/*", () => {
    expect(() => gatewayHostPermission("https://*/*")).toThrow();
  });

  it("rejects a wildcard subdomain host https://*.example.com", () => {
    expect(() => gatewayHostPermission("https://*.example.com")).toThrow();
  });

  it("rejects an empty / missing base URL", () => {
    expect(() => gatewayHostPermission("")).toThrow();
    // @ts-expect-error deliberately passing undefined
    expect(() => gatewayHostPermission(undefined)).toThrow();
  });

  it("rejects a non-http(s) scheme (ftp/file/chrome-extension)", () => {
    expect(() => gatewayHostPermission("ftp://gateway.example")).toThrow();
    expect(() => gatewayHostPermission("file:///etc/passwd")).toThrow();
    expect(() => gatewayHostPermission("chrome-extension://abc/")).toThrow();
  });

  it("rejects plain http on a NON-loopback host (production must be TLS)", () => {
    expect(() => gatewayHostPermission("http://gateway.example")).toThrow();
  });

  it("rejects a URL with no host", () => {
    expect(() => gatewayHostPermission("https://")).toThrow();
  });
});

describe("gatewayHostPermission — happy path", () => {
  it("derives a scoped match pattern from an https gateway", () => {
    expect(gatewayHostPermission("https://gateway.example")).toBe("https://gateway.example/*");
  });

  it("drops the port from the match pattern (Chrome patterns match any port)", () => {
    expect(gatewayHostPermission("https://gateway.example:8443")).toBe("https://gateway.example/*");
  });

  it("strips path/query/fragment down to origin", () => {
    expect(gatewayHostPermission("https://gateway.example/api?x=1#y")).toBe(
      "https://gateway.example/*",
    );
  });

  it("allows plain http ONLY for loopback dev (localhost / 127.0.0.1)", () => {
    expect(gatewayHostPermission("http://localhost:8080")).toBe("http://localhost/*");
    expect(gatewayHostPermission("http://127.0.0.1:8080")).toBe("http://127.0.0.1/*");
  });
});

describe("deriveManifest — inject exactly the gateway origin", () => {
  it("adds EXACTLY the gateway origin plus the two Digikala origins", () => {
    const out = deriveManifest(sourceManifest, "https://gateway.example");
    expect(new Set(out.host_permissions)).toEqual(
      new Set([...DIGIKALA_HOST_PERMISSIONS, "https://gateway.example/*"]),
    );
    expect(out.host_permissions).toHaveLength(3);
  });

  it("does not mutate the source manifest object", () => {
    const before = JSON.stringify(sourceManifest);
    deriveManifest(sourceManifest, "https://gateway.example");
    expect(JSON.stringify(sourceManifest)).toBe(before);
  });

  it("does not duplicate when the gateway equals a Digikala origin", () => {
    const out = deriveManifest(sourceManifest, "https://api.digikala.com");
    expect(new Set(out.host_permissions)).toEqual(new Set(DIGIKALA_HOST_PERMISSIONS));
    expect(out.host_permissions).toHaveLength(2);
  });

  it("NEVER introduces a forbidden permission or wildcard host", () => {
    const out = deriveManifest(sourceManifest, "https://gateway.example");
    const all: string[] = [...out.permissions, ...out.host_permissions];
    for (const forbidden of FORBIDDEN_MANIFEST_ENTRIES) {
      expect(all).not.toContain(forbidden);
    }
    for (const hp of out.host_permissions) {
      expect(hp).not.toContain("*://");
      expect(hp).not.toMatch(/\/\/\*/); // no wildcard host
    }
  });

  it("propagates the source permissions unchanged (activeTab/storage/alarms/scripting)", () => {
    const out = deriveManifest(sourceManifest, "https://gateway.example");
    expect(new Set(out.permissions)).toEqual(
      new Set(["activeTab", "storage", "alarms", "scripting"]),
    );
  });

  it("rejects a source manifest that already carries a forbidden permission", () => {
    const tainted = { ...sourceManifest, permissions: [...sourceManifest.permissions, "tabs"] };
    expect(() => deriveManifest(tainted, "https://gateway.example")).toThrow();
  });
});

describe("assertManifestScoped — packaged-artifact cross-boundary assertion", () => {
  it("passes when the effective manifest matches VITE_GATEWAY_BASE_URL exactly", () => {
    const out = deriveManifest(sourceManifest, "https://gateway.example");
    expect(() => assertManifestScoped(out, "https://gateway.example")).not.toThrow();
  });

  it("FAILS when the gateway permission is missing (build must fail)", () => {
    // Simulate the static manifest shipping without the injected origin.
    expect(() => assertManifestScoped(sourceManifest, "https://gateway.example")).toThrow();
  });

  it("FAILS when the manifest permission does not match the configured gateway", () => {
    const out = deriveManifest(sourceManifest, "https://gateway.example");
    expect(() => assertManifestScoped(out, "https://other.example")).toThrow();
  });

  it("FAILS when the manifest carries an extra/arbitrary host origin", () => {
    const out = deriveManifest(sourceManifest, "https://gateway.example");
    const tainted = {
      ...out,
      host_permissions: [...out.host_permissions, "https://evil.example/*"],
    };
    expect(() => assertManifestScoped(tainted, "https://gateway.example")).toThrow();
  });

  it("FAILS when a forbidden permission or wildcard is present", () => {
    const out = deriveManifest(sourceManifest, "https://gateway.example");
    expect(() =>
      assertManifestScoped(
        { ...out, permissions: [...out.permissions, "webRequest"] },
        "https://gateway.example",
      ),
    ).toThrow();
    expect(() =>
      assertManifestScoped(
        { ...out, host_permissions: [...out.host_permissions, "*://*/*"] },
        "https://gateway.example",
      ),
    ).toThrow();
  });
});
