import type { QualityState } from "../components/badges";
import {
  type ChatEnvelope,
  type DeepLink,
  type DockCard,
  type EvidenceRef,
  type InlineTable,
  type Level2Proposal,
  type PickerOption,
  STATEMENT_KINDS,
  type StatementKind,
  type StatementSection,
} from "./types";

// Defensive parsing of the UNTYPED gateway envelope (see chat/types.ts contract
// gap). Every field is validated before use; anything malformed is dropped rather
// than fabricated. The parser never throws — a garbage envelope yields an empty
// envelope, which the view renders as a degraded "no grounded content" state.

const INLINE_TABLE_CAP = 20; // CHAT-023: inline tables stop at 20 rows.

const QUALITY_STATES: readonly QualityState[] = [
  "verified",
  "supported",
  "unverified",
  "conflicted",
  "stale",
  "unavailable",
];

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asString(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
}

function asStatementKind(value: unknown): StatementKind | undefined {
  return STATEMENT_KINDS.find((k) => k === value);
}

function asQuality(value: unknown): QualityState | undefined {
  return QUALITY_STATES.find((q) => q === value);
}

const SEARCH_KEYS = ["variantId", "eventId", "cardId", "actionId"] as const;

/** Parse a gateway deep-link path ("/event?eventId=…") into a typed DeepLink. */
export function parseDeepLink(raw: unknown): DeepLink | undefined {
  const path = asString(raw);
  if (!path?.startsWith("/")) return undefined;
  const qIndex = path.indexOf("?");
  const to = qIndex === -1 ? path : path.slice(0, qIndex);
  const query = qIndex === -1 ? "" : path.slice(qIndex + 1);
  const search: NonNullable<DeepLink["search"]> = {};
  if (query) {
    const params = new URLSearchParams(query);
    for (const key of SEARCH_KEYS) {
      const v = params.get(key);
      if (v) search[key] = v;
    }
  }
  return Object.keys(search).length > 0 ? { to, search } : { to };
}

function parseSection(value: unknown): StatementSection | undefined {
  if (!isRecord(value)) return undefined;
  const kind = asStatementKind(value.kind);
  if (!kind) return undefined;
  const lines = asStringArray(value.lines);
  return { kind, lines };
}

function parseEvidence(value: unknown): EvidenceRef | undefined {
  if (!isRecord(value)) return undefined;
  const ref = asString(value.ref);
  if (!ref) return undefined;
  const evidence: EvidenceRef = { ref };
  const quality = asQuality(value.quality);
  const capturedAt = asString(value.capturedAt);
  return {
    ...evidence,
    ...(quality ? { quality } : {}),
    ...(capturedAt ? { capturedAt } : {}),
  };
}

function parseTable(value: unknown): InlineTable | undefined {
  if (!isRecord(value)) return undefined;
  const headers = asStringArray(value.headers);
  const rawRows = Array.isArray(value.rows) ? value.rows : [];
  const rows = rawRows
    .map((r) => asStringArray(r))
    .filter((r) => r.length > 0)
    .slice(0, INLINE_TABLE_CAP); // hard client cap — never dump unbounded rows.
  if (headers.length === 0 && rows.length === 0) return undefined;
  const declaredTotal = typeof value.totalRows === "number" ? value.totalRows : rawRows.length;
  const totalRows = Math.max(declaredTotal, rawRows.length);
  const deepLink = parseDeepLink(value.deepLink);
  return { headers, rows, totalRows, ...(deepLink ? { deepLink } : {}) };
}

function parsePickerOption(value: unknown): PickerOption | undefined {
  if (!isRecord(value)) return undefined;
  const id = asString(value.id);
  const label = asString(value.label);
  if (!id || !label) return undefined;
  const sku = asString(value.sku);
  const deepLink = parseDeepLink(value.deepLink);
  return { id, label, ...(sku ? { sku } : {}), ...(deepLink ? { deepLink } : {}) };
}

function parseLevel2(value: unknown): Level2Proposal {
  if (!isRecord(value)) return {};
  const deepLink = parseDeepLink(value.deepLink);
  return {
    ...(asString(value.setting) ? { setting: value.setting as string } : {}),
    ...(asString(value.before) ? { before: value.before as string } : {}),
    ...(asString(value.after) ? { after: value.after as string } : {}),
    ...(asString(value.scope) ? { scope: value.scope as string } : {}),
    ...(asString(value.consequence) ? { consequence: value.consequence as string } : {}),
    ...(asString(value.expiresAt) ? { expiresAt: value.expiresAt as string } : {}),
    ...(deepLink ? { deepLink } : {}),
  };
}

function parseCard(value: unknown): DockCard | undefined {
  if (!isRecord(value)) return undefined;
  switch (value.kind) {
    case "picker": {
      const options = (Array.isArray(value.options) ? value.options : [])
        .map(parsePickerOption)
        .filter((o): o is PickerOption => o !== undefined);
      return options.length > 0 ? { kind: "picker", options } : undefined;
    }
    case "approval": {
      const cardId = asString(value.cardId);
      // A restored/streamed approval card carries ONLY its id — never a cached
      // executable control. The control is re-fetched fresh at render (§8.1).
      return cardId ? { kind: "approval", cardId } : undefined;
    }
    case "level2":
      return { kind: "level2", proposal: parseLevel2(value.proposal) };
    default:
      return undefined;
  }
}

export interface ParsedEnvelope {
  readonly envelope: ChatEnvelope;
  readonly cards: readonly DockCard[];
}

/** Parse the untyped `final`-frame envelope into grounded view-models. */
export function parseEnvelope(raw: unknown): ParsedEnvelope {
  if (!isRecord(raw)) {
    return { envelope: { sections: [], evidence: [] }, cards: [] };
  }
  const sections = (Array.isArray(raw.sections) ? raw.sections : [])
    .map(parseSection)
    .filter((s): s is StatementSection => s !== undefined);
  const evidence = (Array.isArray(raw.evidence) ? raw.evidence : [])
    .map(parseEvidence)
    .filter((e): e is EvidenceRef => e !== undefined);
  const table = parseTable(raw.table);
  const deepLink = parseDeepLink(raw.deepLink);
  const cards = (Array.isArray(raw.cards) ? raw.cards : [])
    .map(parseCard)
    .filter((c): c is DockCard => c !== undefined);
  return {
    envelope: {
      sections,
      evidence,
      ...(table ? { table } : {}),
      ...(deepLink ? { deepLink } : {}),
    },
    cards,
  };
}
