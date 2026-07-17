/// <reference types="vite/client" />

// Runtime env contract. VITE_ vars are the ONLY app-visible env. Sentry/Spotlight
// is dev-only and additionally gated by `import.meta.env.DEV`.
interface ImportMetaEnv {
  /** Spotlight sidecar stream URL. Presence enables dev-only Sentry wiring. */
  readonly VITE_SENTRY_SPOTLIGHT?: string;
  /** Gateway base URL for the generated API client. */
  readonly VITE_GATEWAY_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
