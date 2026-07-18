import { useLocale, useT } from "../../app/i18n";
import { formatInstant } from "../../data/format";
import type { Level2Proposal } from "../types";
import { DeepLinkButton } from "./DeepLinkButton";

// Level-2 reversible-config before/after proposal (CHAT-061). It shows the
// setting, before→after, scope, and reversible consequence. The reversible write
// itself is committed through the structured Settings surface (with audit) — the
// dock deep-links there rather than confirming from free text. No Level-3 write
// path exists in the dock (CHAT-062).
//
// CONTRACT GAP (carry-forward): there is no browser-facing L2-confirm endpoint in
// the merged gateway contract (only the machine-plane Draft-write). Until one
// lands, the card confirms via the Settings screen (screens-only fallback).
export function Level2Card({ proposal }: { proposal: Level2Proposal }) {
  const t = useT();
  const { locale } = useLocale();
  return (
    <section className="chat-card chat-l2" data-testid="chat-level2">
      <p className="chat-card__title">{t("chat.l2.title")}</p>
      <dl className="kv chat-l2__kv">
        {proposal.setting ? (
          <div className="kv__row">
            <dt>{t("chat.l2.setting")}</dt>
            <dd>{proposal.setting}</dd>
          </div>
        ) : null}
        <div className="kv__row">
          <dt>{t("chat.l2.before")}</dt>
          <dd data-testid="l2-before">{proposal.before ?? t("common.notAvailable")}</dd>
        </div>
        <div className="kv__row">
          <dt>{t("chat.l2.after")}</dt>
          <dd data-testid="l2-after">{proposal.after ?? t("common.notAvailable")}</dd>
        </div>
        {proposal.scope ? (
          <div className="kv__row">
            <dt>{t("chat.l2.scope")}</dt>
            <dd>{proposal.scope}</dd>
          </div>
        ) : null}
        {proposal.consequence ? (
          <div className="kv__row">
            <dt>{t("chat.l2.consequence")}</dt>
            <dd>{proposal.consequence}</dd>
          </div>
        ) : null}
        {proposal.expiresAt ? (
          <div className="kv__row">
            <dt>{t("chat.l2.expiry")}</dt>
            <dd>{formatInstant(proposal.expiresAt, locale)}</dd>
          </div>
        ) : null}
      </dl>
      <DeepLinkButton
        link={proposal.deepLink ?? { to: "/settings" }}
        labelKey="chat.l2.confirmInSettings"
        testId="l2-confirm-settings"
      />
    </section>
  );
}
