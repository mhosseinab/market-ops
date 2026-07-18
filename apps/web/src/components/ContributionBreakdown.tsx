import type { MessageKey } from "@market-ops/locale";
import { useT } from "../app/i18n";
import type { Contribution, CostComponent } from "../data/types";
import { ReadinessBadge } from "./badges";
import { LtrToken } from "./LtrToken";
import { MoneyView } from "./MoneyView";

// ContributionBreakdown (component inventory): the inspectable §9.2 margin math.
// It RENDERS the core's deterministic Contribution verbatim — every amount comes
// from the API (MoneyView), no percentage or subtraction is recomputed here
// (money correctness). The rounding-rule id and readiness are surfaced for
// reproducibility (CST-002 / CST-003).

const COMPONENT_LABEL: Record<CostComponent, MessageKey> = {
  cogs: "costComponent.cogs",
  commission: "costComponent.commission",
  fulfillment: "costComponent.fulfillment",
  shipping: "costComponent.shipping",
  packaging: "costComponent.packaging",
  promotion: "costComponent.promotion",
  ads: "costComponent.ads",
  returns: "costComponent.returns",
};

export function ContributionBreakdown({ contribution }: { contribution: Contribution }) {
  const t = useT();
  return (
    <div className="contribution" data-testid="contribution-breakdown">
      <ul className="contribution__lines">
        {contribution.deductions.map((d) => (
          <li key={`${d.component}-${d.version}`} className="contribution__line">
            <span className="contribution__name">{t(COMPONENT_LABEL[d.component])}</span>
            <MoneyView amount={d.amount} />
          </li>
        ))}
        <li className="contribution__line contribution__line--total">
          <span className="contribution__name">{t("rec.contribution.netProceeds")}</span>
          <MoneyView amount={contribution.netProceeds} />
        </li>
        <li className="contribution__line contribution__line--total">
          <span className="contribution__name">{t("rec.contribution.total")}</span>
          <MoneyView amount={contribution.amount} />
        </li>
      </ul>
      <div className="contribution__meta">
        <ReadinessBadge state={contribution.readiness} />
        <span className="muted">
          {t("rec.contribution.roundingRule", { rule: "" })}
          <LtrToken text={contribution.roundingRule} />
        </span>
      </div>
    </div>
  );
}
