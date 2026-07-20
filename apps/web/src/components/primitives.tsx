import type { MessageKey } from "@market-ops/locale";
import type { ReactNode } from "react";
import { useT } from "../app/i18n";

// Small presentational primitives from the component inventory: StatCard,
// FilterChips, and a Section card wrapper. All copy resolves through the catalog.

export type Accent = "risk" | "pos" | "warn" | "info" | "accent" | "neutral";

export function StatCard({
  value,
  labelKey,
  accent = "neutral",
  ariaLabel,
}: {
  value: ReactNode;
  labelKey: MessageKey;
  accent?: Accent;
  /**
   * An already-localized accessible name for the whole stat. Supply this when the
   * value alone is ambiguous (e.g. a count whose unit must be spoken explicitly so
   * it is never announced as a monetary amount — issue #98). When present the card
   * is exposed as a labeled group and the raw value/label are hidden from AT.
   */
  ariaLabel?: string;
}) {
  const t = useT();
  const labeled = ariaLabel !== undefined;
  return (
    <div
      className="stat-card"
      data-accent={accent}
      {...(labeled ? { role: "group", "aria-label": ariaLabel } : {})}
    >
      <div className="stat-card__value" aria-hidden={labeled || undefined}>
        {value}
      </div>
      <div className="stat-card__label" aria-hidden={labeled || undefined}>
        {t(labelKey)}
      </div>
    </div>
  );
}

export interface FilterChip {
  readonly id: string;
  readonly labelKey: MessageKey;
  readonly active: boolean;
}

export function FilterChips({
  chips,
  onToggle,
}: {
  chips: readonly FilterChip[];
  onToggle: (id: string) => void;
}) {
  const t = useT();
  return (
    <div className="filter-chips">
      {chips.map((chip) => (
        <button
          key={chip.id}
          type="button"
          className="filter-chip"
          data-active={chip.active ? "true" : "false"}
          aria-pressed={chip.active}
          onClick={() => onToggle(chip.id)}
        >
          {t(chip.labelKey)}
        </button>
      ))}
    </div>
  );
}

export function Section({
  titleKey,
  actions,
  children,
}: {
  titleKey: MessageKey;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <section className="panel">
      <div className="panel__head">
        <h2 className="panel__title">{t(titleKey)}</h2>
        {actions ? <div className="panel__actions">{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}
