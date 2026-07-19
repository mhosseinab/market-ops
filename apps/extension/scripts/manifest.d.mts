// Type surface for the build-time manifest derivation (scripts/manifest.mjs).
// The runtime is a dependency-free .mjs so build.mjs can import it directly; this
// declaration lets the vitest suite (TS) typecheck the same seam.

export interface Mv3Manifest {
  permissions: string[];
  host_permissions: string[];
  [key: string]: unknown;
}

export const DIGIKALA_HOST_PERMISSIONS: readonly string[];
export const FORBIDDEN_MANIFEST_ENTRIES: readonly string[];

export function resolveGatewayBaseUrl(
  env: { VITE_GATEWAY_BASE_URL?: string | undefined } | undefined,
  mode: "production" | "development",
): string;

export function gatewayHostPermission(baseUrl: string): string;
export function deriveManifest(sourceManifest: Mv3Manifest, gatewayBaseUrl: string): Mv3Manifest;
export function assertManifestScoped(manifest: Mv3Manifest, gatewayBaseUrl: string): void;
