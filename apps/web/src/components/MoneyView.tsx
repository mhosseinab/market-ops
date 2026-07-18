import { useLocale, useT } from "../app/i18n";
import { renderAmount } from "../data/format";
import type { MoneyAmount } from "../data/types";

// Renders an exact money amount in the source unit (LOC-008): the localized
// grouped amount plus its unit label (a catalog key). A currency/exponent
// mismatch against the region source unit is QUARANTINED — never inferred into
// another unit — and shows the quarantine label instead of a fabricated number.
export function MoneyView({ amount }: { amount: MoneyAmount }) {
  const { locale } = useLocale();
  const t = useT();
  const rendered = renderAmount(amount, locale);
  if (rendered.quarantined) {
    return <span className="money money--quarantined">{t(rendered.unitKey)}</span>;
  }
  return (
    <span className="money">
      <span className="money__amount">{rendered.amount}</span>{" "}
      <span className="money__unit">{t(rendered.unitKey)}</span>
    </span>
  );
}
