// Build-time MV3 manifest derivation (issue #144).
//
// The static source manifest (public/manifest.json) carries ONLY the two Digikala
// host permissions — least privilege, and the same file the manifest.test.ts
// invariant pins. The market-ops gateway origin the extension pairs/uploads to is
// injected HERE, at build time, from VITE_GATEWAY_BASE_URL, so the packaged
// artifact can reach exactly its configured first-party gateway and nothing wider
// (EXT-001, §14). Everything in this module is pure and dependency-free so the
// vitest suite can pin the validation, derivation, and cross-boundary assertion.

// The two Digikala origins are fixed and always present (docs/09). The gateway
// origin is the ONLY thing added on top.
export const DIGIKALA_HOST_PERMISSIONS = Object.freeze([
  "https://www.digikala.com/*",
  "https://api.digikala.com/*",
]);

// Explicitly-excluded manifest entries (docs/09, §12). These must never appear in
// permissions or host_permissions — not discouraged, forbidden. The wildcard host
// patterns are listed so the cross-boundary assertion fails closed on them.
export const FORBIDDEN_MANIFEST_ENTRIES = Object.freeze([
  "tabs",
  "history",
  "webRequest",
  "webRequestBlocking",
  "<all_urls>",
  "*://*/*",
  "https://*/*",
  "http://*/*",
]);

const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]"]);

// gatewayHostPermission validates a gateway base URL and returns the single Chrome
// match pattern that scopes host access to exactly that origin. It fails closed on
// anything ambiguous or wider than one concrete host:
//   - must parse as a URL with a host;
//   - scheme must be https (plain http allowed ONLY for loopback dev);
//   - the host must be a concrete name — wildcards (`*`) are rejected;
//   - port/path/query/fragment are dropped (Chrome match patterns match any port
//     and the pattern is host-scoped).
export function gatewayHostPermission(baseUrl) {
  if (typeof baseUrl !== "string" || baseUrl.trim() === "") {
    throw new Error("gateway base URL is empty — set VITE_GATEWAY_BASE_URL to the gateway origin");
  }

  let url;
  try {
    url = new URL(baseUrl);
  } catch {
    throw new Error(`gateway base URL is not a valid URL: ${baseUrl}`);
  }

  const scheme = url.protocol.replace(/:$/, "");
  const host = url.hostname; // no port

  if (host === "" || host.includes("*")) {
    throw new Error(`gateway host must be a concrete host, not a wildcard or empty: ${baseUrl}`);
  }

  if (scheme === "https") {
    // ok — production gateway
  } else if (scheme === "http" && LOOPBACK_HOSTS.has(host)) {
    // ok — loopback dev only
  } else {
    throw new Error(`gateway scheme must be https (http allowed only for loopback): ${baseUrl}`);
  }

  return `${scheme}://${host}/*`;
}

function assertNoForbidden(entries, where) {
  for (const entry of entries ?? []) {
    if (FORBIDDEN_MANIFEST_ENTRIES.includes(entry)) {
      throw new Error(`forbidden manifest entry in ${where}: ${entry}`);
    }
  }
}

// deriveManifest returns a deep copy of the source manifest with the validated
// gateway origin appended to host_permissions (deduped). It refuses to derive from
// a source manifest that already carries a forbidden entry (defense in depth), so a
// regression in the static manifest can never be laundered through the build.
export function deriveManifest(sourceManifest, gatewayBaseUrl) {
  const manifest = structuredClone(sourceManifest);
  assertNoForbidden(manifest.permissions, "permissions");
  assertNoForbidden(manifest.host_permissions, "host_permissions");

  const gateway = gatewayHostPermission(gatewayBaseUrl);
  const hosts = Array.isArray(manifest.host_permissions) ? manifest.host_permissions : [];
  manifest.host_permissions = hosts.includes(gateway) ? [...hosts] : [...hosts, gateway];
  return manifest;
}

// assertManifestScoped is the packaged-artifact cross-boundary gate: it compares an
// EFFECTIVE manifest against the configured VITE_GATEWAY_BASE_URL and throws (so the
// build fails) unless host_permissions is EXACTLY the two Digikala origins plus the
// one configured gateway origin — no missing gateway, no arbitrary extra origin, no
// forbidden permission, no wildcard. This is what makes a mismatched or missing
// gateway permission a hard build failure.
export function assertManifestScoped(manifest, gatewayBaseUrl) {
  assertNoForbidden(manifest.permissions, "permissions");
  assertNoForbidden(manifest.host_permissions, "host_permissions");

  const gateway = gatewayHostPermission(gatewayBaseUrl);
  const expected = new Set([...DIGIKALA_HOST_PERMISSIONS, gateway]);
  const actual = new Set(manifest.host_permissions ?? []);

  const missing = [...expected].filter((h) => !actual.has(h));
  const extra = [...actual].filter((h) => !expected.has(h));
  if (missing.length > 0 || extra.length > 0) {
    throw new Error(
      "manifest host_permissions do not match VITE_GATEWAY_BASE_URL:\n" +
        `  configured gateway: ${gatewayBaseUrl} -> ${gateway}\n` +
        `  missing: ${JSON.stringify(missing)}\n` +
        `  unexpected: ${JSON.stringify(extra)}`,
    );
  }
}
