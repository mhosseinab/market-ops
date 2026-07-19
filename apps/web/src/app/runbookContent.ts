// Runbook markdown bundled as raw TEXT (Vite `?raw`). The repo `runbooks/*.md`
// files are the SINGLE source; no copy is duplicated here. The viewer renders the
// body as escaped preformatted text — no Markdown-to-HTML dependency, never
// dangerouslySetInnerHTML. Keyed by the registry `file` path so app/runbooks.ts
// stays the one place a queue→file mapping lives.

import reconciliationMd from "../../../../runbooks/action-reconciliation.md?raw";
import connectorMd from "../../../../runbooks/connector.md?raw";
import observationMd from "../../../../runbooks/observation.md?raw";
import parserMd from "../../../../runbooks/parser.md?raw";

const CONTENT_BY_FILE: Record<string, string> = {
  "runbooks/connector.md": connectorMd,
  "runbooks/observation.md": observationMd,
  "runbooks/parser.md": parserMd,
  "runbooks/action-reconciliation.md": reconciliationMd,
};

/** Raw markdown body for a registry `file`, or undefined if none is bundled. */
export function runbookContent(file: string): string | undefined {
  return CONTENT_BY_FILE[file];
}
