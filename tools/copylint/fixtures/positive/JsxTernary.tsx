// POSITIVE fixture — a ternary rendered as JSX children flags both result
// branches [jsx-expr, jsx-expr]. The condition is not treated as copy.
export const JsxTernary = ({ ok }: { ok: boolean }) => (
  <span>{ok ? "Accepted" : "Rejected"}</span>
);
