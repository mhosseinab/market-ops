import type { MessageKey } from "@market-ops/locale";
import { useT } from "../app/i18n";
import type {
  Contribution,
  ContributionDeduction,
  CostComponent,
  MarginReadinessState,
  MoneyAmount,
} from "../data/types";
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

// Renders the deterministic §9.2 breakdown VERBATIM. Two callers supply it:
//   • the approval flow, which has a full `Contribution` (net proceeds + rounding
//     rule + total);
//   • the authoritative RecommendationDetail read, which carries the persisted
//     `deductions` + `total` + `readiness` but genuinely has no net-proceeds or
//     rounding-rule field — those lines are simply omitted, never fabricated.
export function ContributionBreakdown({
  contribution,
  deductions,
  total,
  netProceeds,
  readiness,
  roundingRule,
}: {
  contribution?: Contribution;
  deductions?: ContributionDeduction[];
  total?: MoneyAmount;
  netProceeds?: MoneyAmount;
  readiness?: MarginReadinessState;
  roundingRule?: string;
}) {
  const t = useT();
  const lines = contribution?.deductions ?? deductions ?? [];
  const totalAmount = contribution?.amount ?? total;
  const net = contribution?.netProceeds ?? netProceeds;
  const rd = contribution?.readiness ?? readiness;
  const rule = contribution?.roundingRule ?? roundingRule;
  return (
    <div className="contribution" data-testid="contribution-breakdown">
      <ul className="contribution__lines">
        {lines.map((d) => (
          <li key={`${d.component}-${d.version}`} className="contribution__line">
            <span className="contribution__name">{t(COMPONENT_LABEL[d.component])}</span>
            <MoneyView amount={d.amount} />
          </li>
        ))}
        {net ? (
          <li className="contribution__line contribution__line--total">
            <span className="contribution__name">{t("rec.contribution.netProceeds")}</span>
            <MoneyView amount={net} />
          </li>
        ) : null}
        {totalAmount ? (
          <li className="contribution__line contribution__line--total">
            <span className="contribution__name">{t("rec.contribution.total")}</span>
            <MoneyView amount={totalAmount} />
          </li>
        ) : null}
      </ul>
      <div className="contribution__meta">
        {rd ? <ReadinessBadge state={rd} /> : null}
        {rule ? (
          <span className="muted">
            {t("rec.contribution.roundingRule", { rule: "" })}
            <LtrToken text={rule} />
          </span>
        ) : null}
      </div>
    </div>
  );
}
