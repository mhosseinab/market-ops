// POSITIVE fixture — an exemption directive with NO reason is itself reported
// [exemption-without-reason] and does NOT suppress the literal it sits above,
// so the [jsx-text] violation still fires. The escape hatch must justify itself.
export const ExemptNoReason = () => (
  // copylint-allow
  <span>Needs a reason</span>
);
