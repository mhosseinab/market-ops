// Unsupported-value telemetry adapter for the web edge (LOC-002, issue #121).
//
// The web edge maps stable server codes/types (a chat `failure.code`, a briefing
// `event.eventType`) to CLOSED catalog `MessageKey` maps. A value OUTSIDE the
// closed set renders a catalog-backed localized unavailable label AND emits this
// telemetry so the drift is OBSERVABLE. It is:
//   * safe        — carries only the STABLE machine code/type (a snake_case
//                    technical identifier) plus safe release/page context. It
//                    NEVER carries rendered Persian/fallback copy, free text, or
//                    user data. Locale copy is data, never a diagnostic id
//                    (CLAUDE.md localization boundary).
//   * bounded     — identical (kind+value) signatures are deduplicated via a
//                    bounded, oldest-out set so a hot render loop cannot flood.
//   * failure-safe — a throwing/slow sink is swallowed; emission never breaks or
//                    blocks rendering (the localized fallback already rendered).
//
// All field names here are STABLE ASCII technical identifiers (wire keys, not
// user copy) and deliberately do NOT pass through the copy catalog.

/** Which closed web-edge map rejected the value. */
export type UnsupportedValueKind = "chat_failure_code" | "briefing_event_type";

/** The safe payload emitted for a single unsupported (unmapped) value. */
export interface UnsupportedValueReport {
  /** The closed map that had no entry for the value. */
  readonly kind: UnsupportedValueKind;
  /** The STABLE machine code/type — a technical identifier, never user copy. */
  readonly value: string;
  /** Build/release identifier (safe, non-PII). */
  readonly release: string;
  /** Path only (no query/hash) of the page where the miss occurred. */
  readonly page: string;
}

/** An application telemetry sink. Injected in tests; defaulted in the bundle. */
export type UnsupportedValueSink = (report: UnsupportedValueReport) => void;

/** The event emitted with each unsupported value (the caller supplies release/page). */
export interface UnsupportedValueEvent {
  readonly kind: UnsupportedValueKind;
  readonly value: string;
}

export interface UnsupportedValueReporterOptions {
  /** Max distinct (kind+value) signatures retained for dedup. */
  readonly dedupLimit?: number;
}

const DEFAULT_DEDUP_LIMIT = 256;

// Dedup-signature separator. A NUL can never appear inside a machine code/type,
// so it is a collision-proof delimiter; written as the visible escape so the
// source stays plain ASCII and remains diff-reviewable. Runtime value is the raw
// NUL byte.
const SIG_SEP = "\0";

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

/** Build a bounded, failure-safe reporter around an injected sink. */
export function createUnsupportedValueReporter(
  sink: UnsupportedValueSink,
  options: UnsupportedValueReporterOptions = {},
): (event: UnsupportedValueEvent) => void {
  const dedupLimit = options.dedupLimit ?? DEFAULT_DEDUP_LIMIT;
  const seen = new Set<string>();

  return (event: UnsupportedValueEvent): void => {
    const signature = `${event.kind}${SIG_SEP}${event.value}`;
    if (seen.has(signature)) return; // deduped — already reported this miss.

    if (seen.size >= dedupLimit) {
      const oldest = seen.values().next().value;
      if (oldest !== undefined) seen.delete(oldest);
    }
    seen.add(signature);

    const report: UnsupportedValueReport = {
      kind: event.kind,
      value: event.value,
      release: safeRelease(),
      page: safePage(),
    };

    try {
      sink(report);
    } catch {
      // Telemetry must never break rendering; the localized fallback already
      // rendered. Failures are intentionally swallowed here.
    }
  };
}

/**
 * Default production sink: a structured, bounded emission keyed by a stable ASCII
 * event name a log drain can pick up. No cloud telemetry backend is wired in P0
 * web; a deployment can override via `setUnsupportedValueSink`.
 */
export const consoleUnsupportedValueSink: UnsupportedValueSink = (report) => {
  console.warn("chat.unsupported_value", report);
};

// One bounded, failure-safe reporter forwards to the active sink; its dedup state
// is rebuilt whenever the sink is swapped, so swapping the backend starts a fresh
// dedup window (and keeps tests isolated).
let emit = createUnsupportedValueReporter(consoleUnsupportedValueSink);

/** Inject a telemetry sink (deployment wiring / tests). */
export function setUnsupportedValueSink(sink: UnsupportedValueSink): void {
  emit = createUnsupportedValueReporter(sink);
}

/** Restore the default production sink. */
export function resetUnsupportedValueSink(): void {
  emit = createUnsupportedValueReporter(consoleUnsupportedValueSink);
}

/** Report an unsupported value (safe machine code/type only). */
export function reportUnsupportedValue(event: UnsupportedValueEvent): void {
  emit(event);
}
