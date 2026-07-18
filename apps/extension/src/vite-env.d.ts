/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Gateway base URL injected at build time. Its host must be added to
  // host_permissions at packaging/deploy (see manifest.json).
  readonly VITE_GATEWAY_BASE_URL?: string;
  // The market-ops web app origin, used ONLY to build deep-link hrefs the user
  // clicks to open a new tab (EXT-008) — never fetched from, never added to
  // host_permissions.
  readonly VITE_WEB_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
