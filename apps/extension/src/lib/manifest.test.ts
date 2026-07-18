import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const extRoot = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const manifest = JSON.parse(readFileSync(join(extRoot, "public", "manifest.json"), "utf8"));

// Permissions are minimal and FIXED (docs/09). This test fails closed if a future
// change requests one of the explicitly-excluded permissions.
const FORBIDDEN_PERMISSIONS = ["tabs", "history", "webRequest", "webRequestBlocking", "<all_urls>"];

describe("manifest — minimal MV3 permissions (docs/09, §12)", () => {
  it("is Manifest V3", () => {
    expect(manifest.manifest_version).toBe(3);
  });

  it("requests ONLY the allow-listed permissions", () => {
    expect(new Set(manifest.permissions)).toEqual(
      new Set(["activeTab", "storage", "alarms", "scripting"]),
    );
  });

  it("NEVER requests tabs/history/webRequest/<all_urls>", () => {
    const perms: string[] = [...(manifest.permissions ?? []), ...(manifest.host_permissions ?? [])];
    for (const forbidden of FORBIDDEN_PERMISSIONS) {
      expect(perms).not.toContain(forbidden);
    }
  });

  it("scopes host permissions to Digikala hosts only", () => {
    expect(manifest.host_permissions).toEqual([
      "https://www.digikala.com/*",
      "https://api.digikala.com/*",
    ]);
  });

  it("captures ONLY on Digikala product pages (explicit product browsing — EXT-002)", () => {
    const matches = manifest.content_scripts.flatMap((c: { matches: string[] }) => c.matches);
    expect(matches).toEqual(["https://www.digikala.com/product/*"]);
  });
});
