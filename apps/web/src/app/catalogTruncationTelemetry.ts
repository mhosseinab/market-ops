// Catalog-truncation telemetry for the Products bounded page walk (issue #256).
//
// The Products screen walks the paginated `/catalog/products` read model into ONE
// authoritative client-side set so the readiness filter, count, and pagination all
// describe the whole catalog. That walk is explicitly bounded to CATALOG_MAX_PAGES.
// If the server still reports a next page WHEN the cap is reached, the loaded set is
// an INCOMPLETE, non-authoritative slice. The screen fails closed (a distinct
// truncated view state + an annotated count) AND emits this event so the cap hit is
// OBSERVABLE rather than a silent truncation of an evidence-bearing set.
//
// The payload is:
//   * safe    — counts only (pages fetched, the page cap) plus safe release/page
//               context. It NEVER carries product ids, native identifiers, rendered
//               copy, or any user/marketplace data. Locale copy is data, never a
//               diagnostic id (CLAUDE.md localization boundary).
//   * bounded — the screen emits once per cap-hit transition (a React effect keyed
//               on the truncated flag), so a hot render loop cannot flood the sink.
//   * failure-safe — a throwing/slow sink is swallowed; emission never breaks or
//               blocks rendering (the truncated state already rendered).
//
// All field names here are STABLE ASCII technical identifiers (wire keys, not user
// copy) and deliberately do NOT pass through the copy catalog.

/** The safe payload emitted for a single catalog-walk cap hit. */
export interface CatalogTruncationReport {
  /** Pages actually accumulated before the walk stopped (a count, never ids). */
  readonly pagesFetched: number;
  /** The configured page cap the walk stopped at (CATALOG_MAX_PAGES). */
  readonly pageCap: number;
  /** Build/release identifier (safe, non-PII). */
  readonly release: string;
  /** Path only (no query/hash) of the page where the truncation occurred. */
  readonly page: string;
}

/** An application telemetry sink. Injected in tests; defaulted in the bundle. */
export type CatalogTruncationSink = (report: CatalogTruncationReport) => void;

/** The event a caller supplies (release/page are resolved here). */
export interface CatalogTruncationEvent {
  readonly pagesFetched: number;
  readonly pageCap: number;
}

function safeRelease(): string {
  try {
    return import.meta.env.VITE_RELEASE ?? "unknown";
  } catch {
    return "unknown";
  }
}

function safePage(): string {
  try {
    // Path only — query strings and fragments can carry user data (no PII).
    return globalThis.location?.pathname ?? "";
  } catch {
    return "";
  }
}

/**
 * Default production sink: a structured, bounded emission keyed by a stable ASCII
 * event name a log drain can pick up. No cloud telemetry backend is wired in P0
 * web; a deployment can override via `setCatalogTruncationSink`.
 */
export const consoleCatalogTruncationSink: CatalogTruncationSink = (report) => {
  console.warn("products.catalog_truncated", report);
};

let sink: CatalogTruncationSink = consoleCatalogTruncationSink;

/** Inject a telemetry sink (deployment wiring / tests). */
export function setCatalogTruncationSink(next: CatalogTruncationSink): void {
  sink = next;
}

/** Restore the default production sink. */
export function resetCatalogTruncationSink(): void {
  sink = consoleCatalogTruncationSink;
}

/** Report a catalog-walk cap hit (safe counts only). */
export function reportCatalogTruncation(event: CatalogTruncationEvent): void {
  const report: CatalogTruncationReport = {
    pagesFetched: event.pagesFetched,
    pageCap: event.pageCap,
    release: safeRelease(),
    page: safePage(),
  };
  try {
    sink(report);
  } catch {
    // Telemetry must never break rendering; the truncated state already rendered.
    // Failures are intentionally swallowed here.
  }
}
