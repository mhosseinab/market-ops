import type { ReactNode } from "react";
import { useT } from "../app/i18n";
import type { CapabilityState } from "./badges";

// ACC-001 enforcement point: a capability enables dependent UI ONLY when a probe
// has confirmed it `supported`. Unknown, Unsupported, and Degraded all keep the
// dependent control disabled and surface the gated-reason note. This is the
// single place the "Unknown never enables dependent UI" rule is applied in
// screens, so the negative test targets it.
export function CapabilityGate({
  state,
  children,
}: {
  state: CapabilityState;
  children: (enabled: boolean) => ReactNode;
}) {
  const t = useT();
  const enabled = state === "supported";
  return (
    <div className="capability-gate" data-capability-enabled={enabled ? "true" : "false"}>
      {children(enabled)}
      {enabled ? null : <p className="capability-gate__note">{t("capability.gatedNote")}</p>}
    </div>
  );
}
