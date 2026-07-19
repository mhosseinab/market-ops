// NEGATIVE fixture — technical attributes are not display copy: className,
// role, data-*, id, href, target carry literals that must NOT flag.
declare function t(key: string): string;
export const TechnicalAttrs = () => (
  <div className="banner banner--warn" role="alert" data-testid="warn" id="main">
    <a href="/today" target="_blank" rel="noreferrer">
      {t("nav.today")}
    </a>
  </div>
);
