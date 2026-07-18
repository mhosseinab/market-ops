// LTR-isolated technical identifier (SKU, URL, model number, ID). Renders the
// value inside `direction:ltr; unicode-bidi:isolate` (monospace) so a Latin
// identifier never corrupts the surrounding RTL text (LOC-005, design glossary).
export function LtrToken({ text }: { text: string }) {
  return <span className="ltr">{text}</span>;
}
