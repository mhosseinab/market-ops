import type { MessageKey } from "@market-ops/locale";
import { useT } from "../../app/i18n";
import { AppLink } from "../../components/AppLink";
import type { DeepLink } from "../types";

// Every data-bearing chat answer carries a deep link to the matching structured
// state (CHAT-006). The link opens the same entity + filters via the router — the
// screens-only surface the whole safety model falls back to.
export function DeepLinkButton({
  link,
  labelKey = "chat.deepLink",
  testId,
}: {
  link: DeepLink;
  labelKey?: MessageKey;
  testId?: string;
}) {
  const t = useT();
  return (
    <AppLink
      to={link.to}
      {...(link.search ? { search: link.search } : {})}
      className="chat-deeplink"
      {...(testId ? { testId } : {})}
    >
      {t(labelKey)}
    </AppLink>
  );
}
