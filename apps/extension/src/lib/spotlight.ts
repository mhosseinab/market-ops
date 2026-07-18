// Dev-only error surface (S30: "Dev-only Spotlight wiring for service-worker +
// content-script errors, build-time dev flag"). Gated at RUNTIME behind
// `import.meta.env.DEV`, which Vite statically replaces with `false` in a
// production build; esbuild then dead-code-eliminates the unreachable block, so
// the packaged zip carries NO Spotlight/Sentry code (scripts/assert-prod-clean
// proves it — the same pattern the web app uses).
//
// The ONLY reference to the dev-observability package is the non-analyzable
// dynamic import INSIDE the guard; a top-level import would defeat elimination.

export async function initDevErrorReporting(
  surface: "service-worker" | "content-script",
): Promise<void> {
  if (!import.meta.env.DEV) return;
  try {
    const pkg = `${"@spotlight"}js/overlay`;
    const mod = (await import(/* @vite-ignore */ pkg).catch(() => null)) as {
      init?: (o: unknown) => void;
    } | null;
    mod?.init?.({ injectImmediately: true });
    globalThis.addEventListener?.("error", (e) => {
      console.error(`[dev:${surface}]`, (e as ErrorEvent).message);
    });
  } catch {
    // Dev-only convenience; never affects capture behaviour.
  }
}
