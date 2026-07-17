import { useT } from "../app/i18n";

// Reassuring "nothing to show" state (STATE_MATRIX). Copy resolves through the
// catalog; the reason line is a text label, never color alone.
export function EmptyState() {
  const t = useT();
  return (
    <div className="screen-empty">
      <p>{t("state.empty.title")}</p>
      <p>{t("state.empty.body")}</p>
    </div>
  );
}
