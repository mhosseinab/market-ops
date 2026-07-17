import type { MessageKey } from "@market-ops/locale";
import { useT } from "../app/i18n";

// Onboarding stepper (component inventory). Each step carries a state
// (done/active/todo) rendered with a shape marker AND a text label — never color
// alone. Step labels resolve through the catalog.
export type StepState = "done" | "active" | "todo";

export interface Step {
  readonly id: string;
  readonly labelKey: MessageKey;
  readonly state: StepState;
}

export function Stepper({ steps }: { steps: readonly Step[] }) {
  const t = useT();
  return (
    <ol className="stepper">
      {steps.map((step) => (
        <li key={step.id} className="stepper__step" data-state={step.state}>
          <span className="stepper__marker" aria-hidden />
          <span className="stepper__label">{t(step.labelKey)}</span>
        </li>
      ))}
    </ol>
  );
}
