/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Gateway base URL injected at build time. Its host must be added to
  // host_permissions at packaging/deploy (see manifest.json).
  readonly VITE_GATEWAY_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
