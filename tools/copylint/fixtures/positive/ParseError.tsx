// POSITIVE fixture — a file TypeScript cannot fully parse must FAIL CLOSED. The
// broken declaration below leaves a partial AST that the structural walker never
// reaches, so the bare user-facing literal "Delete forever" would otherwise slip
// through. The linter must report [parse-error] rather than treat it as clean.
export const Broken = () => {
  const label: = "Delete forever";
  return null;
};
