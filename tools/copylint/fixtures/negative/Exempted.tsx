// NEGATIVE fixture — an inline literal with a reasoned allow directive on the
// line above is a reviewed, documented exemption and must pass.
export const Exempted = () => (
  <div>
    {/* copylint-allow: verbatim brand name rendered LTR, never localized */}
    <span>Digikala</span>
  </div>
);
