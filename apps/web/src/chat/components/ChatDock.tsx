import { AssistantRuntimeProvider, ComposerPrimitive, ThreadPrimitive } from "@assistant-ui/react";
import type { MessageKey } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { useAppState } from "../../app/appState";
import { useT } from "../../app/i18n";
import { Banner } from "../../components/Banner";
import { CONTEXT_LABEL_KEY, deriveChatContext } from "../context";
import { ChatDockActionsContext } from "../dockActions";
import type { ChatUnavailable, ChatUnavailableReason } from "../types";
import { useChatDock } from "../useChatDock";
import { BriefingPanel } from "./BriefingPanel";
import { DeepLinkButton } from "./DeepLinkButton";
import { DockMessageView } from "./DockMessageView";

// The persistent chat dock (CHAT-001): a dock layer over all six areas, NOT a
// seventh nav area. Built on @assistant-ui/react HEADLESS primitives
// (Thread/Composer) over a `useExternalStoreRuntime` bound to the gateway `/chat`
// SSE — no styled registry, no react-langgraph, no LLM connection. All visible
// copy is catalog keys; structured content renders as OUR custom message parts.
// RTL is inherited from <html dir> (driven by the locale pack, LOC-005); the
// primitives carry no built-in strings and no direction branch lives here.

// Curated (not model-generated in P0) suggested prompts. Sending one opens a
// read/Draft-only turn — it never approves.
const SUGGESTED_PROMPTS: readonly MessageKey[] = [
  "chat.prompt.briefing",
  "chat.prompt.blockers",
  "chat.prompt.freshness",
];

const UNAVAILABLE_REASON_KEY: Record<ChatUnavailableReason, MessageKey> = {
  kill_switch_global: "chat.unavailable.kill_switch_global",
  kill_switch_account: "chat.unavailable.kill_switch_account",
  provider_unavailable: "chat.unavailable.provider_unavailable",
};

function UnavailableBanner({ unavailable }: { unavailable: ChatUnavailable }) {
  const t = useT();
  // Kill switch / provider outage: the dock degrades to a read-only conversation
  // and the screens-only fallback. Every valid card remains actionable in Actions
  // (§16) — the banner deep-links there. Screens are entirely unaffected.
  return (
    <div data-testid="chat-unavailable" data-reason={unavailable.reason}>
      <Banner
        tone="warn"
        title={t("chat.unavailable.title")}
        body={
          <>
            {t(UNAVAILABLE_REASON_KEY[unavailable.reason])} {t("chat.unavailable.screensNote")}
          </>
        }
        actions={
          <DeepLinkButton
            link={{ to: "/actions" }}
            labelKey="chat.unavailable.toActions"
            testId="chat-unavailable-actions"
          />
        }
      />
    </div>
  );
}

export function ChatDock() {
  const t = useT();
  const { chatOpen, toggleChat } = useAppState();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const rawSearch = useRouterState({ select: (s) => s.location.search });
  const context = deriveChatContext(pathname, rawSearch as Record<string, string>);
  const { runtime, unavailable, actions } = useChatDock(context);

  if (!chatOpen) return null;

  return (
    <aside className="chat-dock" data-testid="chat-dock">
      <ChatDockActionsContext.Provider value={actions}>
        <AssistantRuntimeProvider runtime={runtime}>
          <header className="chat-dock__head">
            <span className="chat-dock__title">{t("chat.title")}</span>
            <span
              className="chat-context-chip"
              data-testid="chat-context-chip"
              data-context={context.kind}
            >
              {t("chat.context.label")} {t(CONTEXT_LABEL_KEY[context.kind])}
            </span>
            <button
              type="button"
              className="chat-dock__close"
              aria-label={t("chat.close")}
              onClick={toggleChat}
            >
              {"×"}
            </button>
          </header>

          <BriefingPanel />

          {unavailable ? <UnavailableBanner unavailable={unavailable} /> : null}

          <div className="chat-dock__suggestions" data-testid="chat-suggestions">
            {SUGGESTED_PROMPTS.map((key) => (
              <ThreadPrimitive.Suggestion key={key} prompt={t(key)} send className="chat-chip">
                {t(key)}
              </ThreadPrimitive.Suggestion>
            ))}
          </div>

          <ThreadPrimitive.Root className="chat-thread">
            <ThreadPrimitive.Viewport className="chat-thread__viewport">
              <ThreadPrimitive.Empty>
                <div className="chat-empty" data-testid="chat-empty">
                  <p className="chat-empty__title">{t("chat.empty.title")}</p>
                  <p className="chat-empty__body">{t("chat.empty.body")}</p>
                </div>
              </ThreadPrimitive.Empty>
              <ThreadPrimitive.Messages>
                {({ message }) => <DockMessageView message={message} />}
              </ThreadPrimitive.Messages>
            </ThreadPrimitive.Viewport>
          </ThreadPrimitive.Root>

          <ComposerPrimitive.Root className="chat-composer">
            <ComposerPrimitive.Input
              className="chat-composer__input"
              placeholder={t("chat.composer.placeholder")}
              aria-label={t("chat.composer.placeholder")}
              data-testid="chat-input"
            />
            {/* Sends a read/Draft-only turn. It NEVER approves — the only confirm
              path is the structured ApprovalCard control (a card part). */}
            <ComposerPrimitive.Send
              className="chat-composer__send"
              aria-label={t("chat.composer.send")}
              data-testid="chat-send"
            >
              {"➤"}
            </ComposerPrimitive.Send>
          </ComposerPrimitive.Root>

          <p className="chat-dock__footnote" data-testid="chat-footnote">
            {t("chat.footnote")}
          </p>
        </AssistantRuntimeProvider>
      </ChatDockActionsContext.Provider>
    </aside>
  );
}
