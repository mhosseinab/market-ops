// POSITIVE fixture — literals inside a display-attribute expression container,
// including both branches of a ternary, flag [jsx-attr-expr].
export const JsxAttrExpr = ({ ok }: { ok: boolean }) => (
  <button title={"Save changes"} aria-label={ok ? "Enabled" : "Disabled"} />
);
