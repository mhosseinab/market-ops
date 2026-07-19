// NEGATIVE fixture — a ternary of enum-ish tokens in a NON-display position
// (a variable initialiser) and a CSS value on a non-display attribute are not
// copy. Only the visible text flows through t(key).
declare function t(key: string): string;
export const EnumTernary = ({ align, done }: { align?: string; done: boolean }) => {
  const state = done ? "done" : "todo";
  return (
    <div style={{ textAlign: align ?? "start" }} data-state={state}>
      {t("today.title")}
    </div>
  );
};
