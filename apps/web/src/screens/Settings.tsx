import { parseNumericInput } from "@market-ops/locale";
import { useState } from "react";
import { useT } from "../app/i18n";
import { AppLink } from "../components/AppLink";
import { LtrToken } from "../components/LtrToken";
import { Section } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { useConnectorStatus, useSession } from "../data/hooks";
import type { UserRole } from "../data/types";

// Settings (design screen 11 / §8.3 admin levels): connection, users/roles, the
// L3 commercial guardrails, and L2 notification prefs. The never-cut rule here is
// the permission matrix: L3 guardrail fields (hard floor / max movement / cooldown
// / never-cross-zero) are OWNER-ONLY and tagged Level 3; a non-owner NEVER sees
// the edit controls (role-gated render). Guardrails may only be TIGHTENED
// (stricter-only), a rule surfaced beside the controls. The current guardrail
// values and a users list are not exposed by the P0 contract, so values render
// explicitly unavailable and the owner-only inputs are staged (carry-forward for
// api_data_contracts); role gating and stricter-only validation are live now.

const ROLE_LABEL: Record<UserRole, "role.owner" | "role.operator" | "role.internal"> = {
  owner: "role.owner",
  operator: "role.operator",
  internal: "role.internal",
};

export function Settings() {
  const t = useT();
  const sessionQuery = useSession();
  const connectorQuery = useConnectorStatus();
  const role = sessionQuery.data?.role;
  const isOwner = role === "owner";

  // Stricter-only demo: the max-movement cap may only be lowered. The baseline is
  // the first value the owner commits this session; a later loosening (higher cap)
  // is rejected client-side and surfaced. Authoritative enforcement is server-side.
  const [capBaseline, setCapBaseline] = useState<number | null>(null);
  const [capDraft, setCapDraft] = useState("");
  const [capError, setCapError] = useState(false);

  function onCapChange(raw: string) {
    setCapDraft(raw);
    const parsed = parseNumericInput(raw);
    if (parsed === null) {
      setCapError(false);
      return;
    }
    const value = Number(parsed);
    if (capBaseline === null) setCapBaseline(value);
    setCapError(capBaseline !== null && value > capBaseline);
  }

  const unavailable = t("common.notAvailable");

  return (
    <div className="screen">
      <ViewState
        pending={sessionQuery.isPending}
        error={sessionQuery.isError}
        onRetry={() => void sessionQuery.refetch()}
      >
        <Section
          titleKey="settings.connection.title"
          actions={
            <AppLink to="/onboarding" className="link" testId="settings-to-onboarding">
              {t("settings.connection.manage")}
            </AppLink>
          }
        >
          <dl className="kv">
            <div className="kv__row">
              <dt>{t("settings.connection.state")}</dt>
              <dd>
                {connectorQuery.data ? (
                  <span>
                    {connectorQuery.data.connectionState === "connected"
                      ? t("connector.state.connected")
                      : t("connector.state.disconnected")}
                  </span>
                ) : (
                  <span className="muted">{unavailable}</span>
                )}
              </dd>
            </div>
          </dl>
        </Section>

        <Section titleKey="settings.users.title">
          {sessionQuery.data ? (
            <div className="user-chip" data-testid="settings-user">
              <span className="user-chip__email">
                <LtrToken text={sessionQuery.data.email} />
              </span>
              {role ? (
                <span className="badge badge--pill tone-info" data-testid="settings-role">
                  <span className="badge__dot" aria-hidden />
                  {t(ROLE_LABEL[role])}
                </span>
              ) : null}
            </div>
          ) : (
            <p className="muted">{unavailable}</p>
          )}
          <p className="muted">{t("settings.users.rosterUnavailable")}</p>
        </Section>

        <Section titleKey="settings.guardrails.title">
          <div className="level-tag" data-testid="l3-tag">
            {t("settings.level.l3")}
          </div>
          <p className="muted">{t("settings.guardrails.explainNote")}</p>
          <p className="muted" data-testid="stricter-only-note">
            {t("settings.guardrails.stricterOnly")}
          </p>

          <dl className="kv">
            <div className="kv__row">
              <dt>{t("settings.guardrails.floor")}</dt>
              <dd>
                <span className="muted">{unavailable}</span>
              </dd>
            </div>
            <div className="kv__row">
              <dt>{t("settings.guardrails.maxMovement")}</dt>
              <dd>
                <span className="muted">{unavailable}</span>
              </dd>
            </div>
            <div className="kv__row">
              <dt>{t("settings.guardrails.cooldown")}</dt>
              <dd>
                <span className="muted">{unavailable}</span>
              </dd>
            </div>
            <div className="kv__row">
              <dt>{t("settings.guardrails.neverCrossZero")}</dt>
              <dd>
                <span className="muted">{unavailable}</span>
              </dd>
            </div>
          </dl>

          {isOwner ? (
            <div className="l3-edit" data-testid="l3-edit-controls">
              <label className="field">
                <span className="field__label">{t("settings.guardrails.maxMovement")}</span>
                <input
                  className="field__input"
                  data-testid="l3-edit-maxMovement"
                  inputMode="numeric"
                  value={capDraft}
                  onChange={(e) => onCapChange(e.target.value)}
                />
              </label>
              {capError ? (
                <p className="blocker-note" data-testid="stricter-only-error">
                  {t("settings.guardrails.stricterOnlyError")}
                </p>
              ) : null}
              <button type="button" className="btn btn--secondary" disabled data-testid="l3-save">
                {t("settings.guardrails.save")}
              </button>
              <p className="muted">{t("settings.guardrails.persistencePending")}</p>
            </div>
          ) : (
            <p className="muted" data-testid="l3-owner-only-note">
              {t("settings.guardrails.ownerOnlyNote")}
            </p>
          )}
        </Section>

        <Section titleKey="settings.notifications.title">
          <div className="level-tag" data-testid="l2-tag">
            {t("settings.level.l2")}
          </div>
          <label className="field field--inline">
            <input type="checkbox" data-testid="notif-digest" />
            <span className="field__label">{t("settings.notifications.emailDigest")}</span>
          </label>
        </Section>
      </ViewState>
    </div>
  );
}
