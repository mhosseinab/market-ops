/// <reference types="vite/client" />

// Runtime env contract. VITE_ vars are the ONLY app-visible env. Sentry/Spotlight
// is dev-only and additionally gated by `import.meta.env.DEV`.
interface ImportMetaEnv {
  /** Spotlight sidecar stream URL. Presence enables dev-only Sentry wiring. */
  readonly VITE_SENTRY_SPOTLIGHT?: string;
  /** Gateway base URL for the generated API client. */
  readonly VITE_GATEWAY_BASE_URL?: string;
  /** Active marketplace account id (P0 has no list-accounts endpoint). */
  readonly VITE_MARKETPLACE_ACCOUNT_ID?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
