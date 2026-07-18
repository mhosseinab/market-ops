// Dev-only observability (S25 assignment). The Sentry browser SDK streams errors
// and traces to the local Spotlight SIDECAR (deploy/compose.dev.yml, :8969) — it
// is NOT the embedded Spotlight overlay. The whole thing is gated on
// `import.meta.env.DEV && VITE_SENTRY_SPOTLIGHT`; because Vite statically
// replaces `import.meta.env.DEV` with `false` in a production build, this block
// (and its dynamic `@sentry/react` import) is dead-code-eliminated from the prod
// bundle — proven by scripts/assert-prod-clean.mjs.
//
// NOTE: Context7 is egress-blocked in this environment, so the exact Spotlight
// SDK wiring is implemented from the @sentry/react package types empirically;
// Context7 verification of the `spotlight` init option is DEFERRED (handoff risk).

export async function initObservability(): Promise<void> {
  if (import.meta.env.DEV && import.meta.env.VITE_SENTRY_SPOTLIGHT) {
    const spotlight = import.meta.env.VITE_SENTRY_SPOTLIGHT;
    const Sentry = await import("@sentry/react");
    Sentry.init({
      // No DSN: dev telemetry goes to the local sidecar, never to a cloud DSN.
      enabled: true,
      tracesSampleRate: 1.0,
      // Route envelopes to the Spotlight sidecar stream.
      spotlight,
    });
  }
}
