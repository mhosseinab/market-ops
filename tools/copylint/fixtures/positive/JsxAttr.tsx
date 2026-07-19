// POSITIVE fixture — string literals in display attributes flag [jsx-attr].
// The non-display attributes (type, src) are correctly ignored.
export const JsxAttr = () => (
  <div>
    <button type="button" aria-label="Close dialog" />
    <input placeholder="Search products" />
    <img src="/logo.png" alt="Company logo" />
    <span title="More info" />
  </div>
);
