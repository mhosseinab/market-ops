import { useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { formatInstant, mantissaToWire } from "../data/format";
import type { ApprovalBinding, ApprovalCardView, MoneyAmount } from "../data/types";
import { LtrToken } from "./LtrToken";
import { MoneyView } from "./MoneyView";

// ApprovalCard (component inventory): THE only mutation control (§8, never-cut
// free-text containment). Confirmation is a single structured <button> bound to
// the card's exact APR-001 binding (action id + parameter/context/policy/cost
// versions + evidence versions + expiry) — free text, Enter, and keyboard
// shortcuts CANNOT confirm.
//
// APR-001 invalidation UX (journey 6): the proposed price is editable via −/+;
// the moment it diverges from the card's authoritative price the prior control
// is VOID — the confirm button disables and a recalculate action appears. A card
// whose server version changed under a live control (polled by the screen) is
// STALE and likewise renders disabled with recalculate. A voided control can
// never be confirmed.
//
// Editing is integer-exact on the bigint mantissa (same currency/exponent, no
// float, no JS-number intermediate — the wire mantissa is an int64 string) and
// NEVER executes — the authoritative next price is minted server-side on
// recalculate, so no fabricated money reaches an approval.

function editStep(mantissa: bigint): bigint {
  const abs = mantissa < 0n ? -mantissa : mantissa;
  const step = abs / 100n;
  return step > 1n ? step : 1n;
}

export function ApprovalCard({
  card,
  baselineVersion,
  confirmPending = false,
  onConfirm,
  onRecalculate,
}: {
  card: ApprovalCardView;
  baselineVersion: number;
  confirmPending?: boolean;
  onConfirm: (binding: ApprovalBinding) => void;
  onRecalculate: () => void;
}) {
  const t = useT();
  const { locale } = useLocale();
  const basePrice = BigInt(card.price.mantissa);
  const [proposedMantissa, setProposedMantissa] = useState<bigint>(basePrice);

  const edited = proposedMantissa !== basePrice;
  const staleVersion = card.version !== baselineVersion;
  const voided = edited || staleVersion;
  const live = card.state === "awaiting_confirmation" && card.hasControl;
  const canConfirm = live && !voided && !confirmPending;

  const proposed: MoneyAmount = { ...card.price, mantissa: mantissaToWire(proposedMantissa) };
  const step = editStep(basePrice);

  return (
    <section
      className="panel approval-card"
      data-testid="approval-card"
      data-card-version={card.version}
      data-baseline-version={baselineVersion}
      data-stale={staleVersion ? "true" : "false"}
      data-edited={edited ? "true" : "false"}
    >
      <div className="panel__head">
        <h2 className="panel__title">{t("rec.card.title")}</h2>
        <span className="muted">
          {t("rec.card.id")} <LtrToken text={card.id} />
        </span>
      </div>

      <dl className="kv approval-card__meta">
        <div className="kv__row">
          <dt>{t("rec.card.version")}</dt>
          <dd>
            <LtrToken text={String(card.version)} />
          </dd>
        </div>
        <div className="kv__row">
          <dt>{t("rec.card.expiry")}</dt>
          <dd data-expires-at={card.binding.expiresAt}>
            {formatInstant(card.binding.expiresAt, locale)}
          </dd>
        </div>
      </dl>

      <div className="approval-card__price">
        <span className="approval-card__price-label">{t("rec.price.proposed")}</span>
        <div className="approval-card__stepper">
          <button
            type="button"
            className="btn btn--sm"
            aria-label={t("rec.price.decrease")}
            disabled={!live || confirmPending}
            onClick={() => setProposedMantissa((m) => m - step)}
          >
            {"−"}
          </button>
          <MoneyView amount={proposed} />
          <button
            type="button"
            className="btn btn--sm"
            aria-label={t("rec.price.increase")}
            disabled={!live || confirmPending}
            onClick={() => setProposedMantissa((m) => m + step)}
          >
            {"+"}
          </button>
        </div>
        {edited ? (
          <span className="sm-state" data-tone="warn" data-testid="edited-flag">
            <span className="badge__dot" aria-hidden />
            {t("rec.price.edited")}
          </span>
        ) : null}
      </div>

      {staleVersion ? (
        <div className="banner banner--warn" role="alert" data-testid="stale-card">
          <div className="banner__body">
            <p className="banner__title">{t("rec.stale.title")}</p>
            <p className="banner__text">{t("rec.stale.body")}</p>
          </div>
        </div>
      ) : edited ? (
        <p className="blocker-note" data-testid="edited-void">
          {t("rec.edited.void")}
        </p>
      ) : null}

      <div className="approval-card__controls">
        <button
          type="button"
          className="btn btn--primary"
          data-testid="confirm-approval"
          disabled={!canConfirm}
          onClick={() => onConfirm(card.binding)}
        >
          {t("rec.action.confirm")}
        </button>
        {voided ? (
          <button
            type="button"
            className="btn btn--sm"
            data-testid="recalculate"
            onClick={() => {
              setProposedMantissa(basePrice);
              onRecalculate();
            }}
          >
            {t("rec.action.recalculate")}
          </button>
        ) : null}
      </div>

      <p className="approval-card__footnote muted" data-testid="approval-footnote">
        {t("rec.footnote")}
      </p>
    </section>
  );
}
