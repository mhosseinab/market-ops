// POSITIVE fixture — a template literal with letter-bearing text in a JSX child
// slot flags [jsx-expr] even though it interpolates a value.
export const JsxTemplate = ({ n }: { n: number }) => <span>{`Total value ${n}`}</span>;
