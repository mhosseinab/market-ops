import type { MessageKey } from "@market-ops/locale";
import { useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { Banner } from "../components/Banner";
import { CapabilityBadge } from "../components/badges";
import { CapabilityGate } from "../components/CapabilityGate";
import { Section } from "../components/primitives";
import { type Step, Stepper, type StepState } from "../components/Stepper";
import { ViewState } from "../components/ViewState";
import { formatInstant } from "../data/format";
import { useConnect, useConnectorAction, useConnectorStatus, useSyncCatalog } from "../data/hooks";
import type {
  CapabilityStatus,
  CatalogSyncState,
  ConnectorCapability,
  ConnectorStatus,
} from "../data/types";
import { NeedsReview } from "./NeedsReview";

// Durable catalog-sync state → its catalog label key (LOC-002: no string
// literals; the canonical Failed term mirrors the glossary). The state is
// EVIDENCE of completed work — it is what advances onboarding, never capability
// support (issue #76).
const SYNC_STATE_LABEL: Record<CatalogSyncState, MessageKey> = {
  none: "onboarding.sync.state.none",
  queued: "onboarding.sync.state.queued",
  running: "onboarding.sync.state.running",
  completed: "onboarding.sync.state.completed",
  failed: "onboarding.sync.state.failed",
};

// The durable sync state, defaulting to "none" when the server reports no run.
// Capability support is NEVER consulted here (issue #76 acceptance).
function syncStateOf(status: ConnectorStatus): CatalogSyncState {
  return status.catalogSync?.state ?? "none";
}

// The nine §15.2 capabilities in the order the onboarding "access scopes" list
// shows them. Data, not a branch — a marketplace's capability set never gates
// behavior by name.
const CAPABILITY_ORDER: readonly ConnectorCapability[] = [
  "catalog_read",
  "owned_offer_read",
  "stock_read",
  "buybox_read",
  "boundary_read",
  "commission_read",
  "sales_context_read",
  "price_write",
  "change_feed",
];

const CAPABILITY_LABEL: Record<ConnectorCapability, MessageKey> = {
  catalog_read: "capability.catalog_read",
  owned_offer_read: "capability.owned_offer_read",
  stock_read: "capability.stock_read",
  buybox_read: "capability.buybox_read",
  boundary_read: "capability.boundary_read",
  commission_read: "capability.commission_read",
  sales_context_read: "capability.sales_context_read",
  price_write: "capability.price_write",
  change_feed: "capability.change_feed",
};

function findCapability(
  status: ConnectorStatus,
  capability: ConnectorCapability,
): CapabilityStatus | undefined {
  return status.capabilities.find((c) => c.capability === capability);
}

function deriveSteps(status: ConnectorStatus): readonly Step[] {
  const connected = status.connectionState === "connected";
  // Sync completion is DURABLE EVIDENCE, not capability availability: the step is
  // done ONLY when the latest sync run reached "completed" (issue #76). A
  // Supported catalog_read with no completed run keeps the step active/todo.
  const syncCompleted = syncStateOf(status) === "completed";
  const s = (done: boolean, active = false): StepState =>
    done ? "done" : active ? "active" : "todo";
  return [
    { id: "org", labelKey: "onboarding.step.createOrg", state: "done" },
    { id: "connect", labelKey: "onboarding.step.connectDk", state: s(connected, !connected) },
    {
      id: "sync",
      labelKey: "onboarding.step.syncCatalog",
      state: s(syncCompleted, connected && !syncCompleted),
    },
    { id: "costs", labelKey: "onboarding.step.importCosts", state: s(false, syncCompleted) },
    { id: "map", labelKey: "onboarding.step.resolveMappings", state: "todo" },
    { id: "assort", labelKey: "onboarding.step.confirmAssortment", state: "todo" },
    { id: "event", labelKey: "onboarding.step.firstEvent", state: "todo" },
  ];
}

export function Onboarding() {
  const t = useT();
  const { locale } = useLocale();
  const query = useConnectorStatus();
  const connect = useConnect();
  const refresh = useConnectorAction("/connector/refresh");
  const disconnect = useConnectorAction("/connector/disconnect");
  const syncCatalog = useSyncCatalog();
  const [authCode, setAuthCode] = useState("");

  const status = query.data;

  return (
    <div className="screen">
      <ViewState
        pending={query.isPending}
        error={query.isError}
        onRetry={() => void query.refetch()}
      >
        {status ? (
          <>
            {status.connectionState === "disconnected" ? (
              <Banner
                tone="risk"
                title={t("connector.disconnected.title")}
                body={t("connector.disconnected.body")}
                actions={
                  <>
                    <button
                      type="button"
                      className="btn btn--primary"
                      disabled={refresh.isPending}
                      onClick={() => refresh.mutate()}
                    >
                      {t("onboarding.action.reconnect")}
                    </button>
                    <span className="muted">{t("connector.readOnlyNote")}</span>
                  </>
                }
              />
            ) : null}

            <div className="screen__grid">
              <Section titleKey="onboarding.stepper.title">
                <Stepper steps={deriveSteps(status)} />
              </Section>

              <Section
                titleKey="onboarding.connectionHealth.title"
                actions={
                  <button
                    type="button"
                    className="btn btn--secondary btn--sm"
                    disabled={refresh.isPending}
                    onClick={() => refresh.mutate()}
                  >
                    {t("onboarding.action.refresh")}
                  </button>
                }
              >
                <div className="kv">
                  <div className="kv__row">
                    <span>{t("onboarding.tokenStatus")}</span>
                    <span>
                      {t(
                        status.connectionState === "connected"
                          ? "connector.state.connected"
                          : "connector.state.disconnected",
                      )}
                    </span>
                  </div>
                </div>

                {/* Durable sync state (ACC-004/ACC-005): rendered from the latest
                    run, NOT from capability support. */}
                <div className="kv">
                  <div className="kv__row">
                    <span>{t("onboarding.sync.label")}</span>
                    <span data-testid="sync-state">{t(SYNC_STATE_LABEL[syncStateOf(status)])}</span>
                  </div>
                </div>

                {/* ACC-001 dependent UI: syncing the catalog requires a probe to
                    have confirmed catalog_read. Unknown NEVER enables it. The
                    click initiates exactly one idempotent sync request; the
                    server collapses duplicates while a run is in-flight. */}
                <CapabilityGate state={findCapability(status, "catalog_read")?.status ?? "unknown"}>
                  {(enabled) => (
                    <button
                      type="button"
                      className="btn btn--primary"
                      data-testid="sync-catalog"
                      disabled={!enabled || syncCatalog.isPending}
                      onClick={() => syncCatalog.mutate()}
                    >
                      {t("onboarding.action.syncCatalog")}
                    </button>
                  )}
                </CapabilityGate>
              </Section>
            </div>

            <Section titleKey="onboarding.capabilities.title">
              <ul className="capability-list">
                {CAPABILITY_ORDER.map((cap) => {
                  const c = findCapability(status, cap);
                  const state = c?.status ?? "unknown";
                  return (
                    <li key={cap} className="capability-list__item">
                      <span className="capability-list__name">{t(CAPABILITY_LABEL[cap])}</span>
                      <CapabilityBadge state={state} />
                      <span className="capability-list__meta">
                        {c?.lastVerified
                          ? t("common.lastVerified", {
                              time: formatInstant(c.lastVerified, locale),
                            })
                          : t("common.lastVerifiedNever")}
                      </span>
                    </li>
                  );
                })}
              </ul>
            </Section>

            <Section
              titleKey="onboarding.connect.title"
              actions={
                status.connectionState === "connected" ? (
                  <button
                    type="button"
                    className="btn btn--secondary btn--sm"
                    disabled={disconnect.isPending}
                    onClick={() => disconnect.mutate()}
                  >
                    {t("onboarding.action.disconnect")}
                  </button>
                ) : undefined
              }
            >
              <label className="field">
                <span className="field__label">{t("onboarding.connect.authCodeLabel")}</span>
                <input
                  className="field__input ltr"
                  value={authCode}
                  onChange={(e) => setAuthCode(e.target.value)}
                />
              </label>
              <button
                type="button"
                className="btn btn--primary"
                disabled={connect.isPending || authCode.trim() === ""}
                onClick={() => connect.mutate(authCode.trim())}
              >
                {t("onboarding.connect.submit")}
              </button>
            </Section>

            <NeedsReview />
          </>
        ) : null}
      </ViewState>
    </div>
  );
}
