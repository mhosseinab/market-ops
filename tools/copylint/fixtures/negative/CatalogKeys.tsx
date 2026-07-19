// NEGATIVE fixture — every user-facing string flows through t(key), including
// display attributes. A string that is a call argument is a key, not copy.
declare function t(key: string): string;
export const CatalogKeys = () => (
  <div aria-label={t("nav.today")} title={t("chat.unavailable.title")}>
    {t("today.readiness.title")}
    <input placeholder={t("products.search.placeholder")} />
  </div>
);
