import type { MessageState } from "@assistant-ui/react";
import { useT } from "../../app/i18n";
import { parseDeepLink } from "../envelope";
import type { ChatEnvelope, ChatFailure, DockCard } from "../types";
import { CardView } from "./CardView";
import { ChatEnvelopeView } from "./ChatEnvelopeView";
import { DeepLinkButton } from "./DeepLinkButton";

// Renders one message's parts. Text parts are the assistant's grounded prose;
// `data` parts are OUR structured content (envelope / cards / failure) mounted as
// custom parts — assistant-ui only supplies the message + part iteration.

function FailureView({ failure }: { failure: ChatFailure }) {
  const t = useT();
  const link = parseDeepLink(failure.deepLink);
  return (
    <div className="chat-failure" data-testid="chat-failure">
      <p className="chat-failure__title">{t("chat.failure.title")}</p>
      {/* §12.4: a concise message (localized at the edge) plus a deep link. */}
      <p className="chat-failure__body">{failure.message}</p>
      {link ? <DeepLinkButton link={link} labelKey="chat.failure.deepLink" /> : null}
    </div>
  );
}

// A client-side TRANSPORT failure (issue #116): the stream was truncated, carried
// a malformed frame, or ended without a validated terminal. The turn is shown as
// unmistakably incomplete — no completed envelope/cards — and the operator is
// pointed to the screens-only fallback, which is always fully functional (§8).
function TransportFailureView() {
  const t = useT();
  return (
    <div className="chat-failure" data-testid="chat-transport-failure">
      <p className="chat-failure__title">{t("chat.failure.title")}</p>
      <p className="chat-failure__body">{t("chat.failure.transportBody")}</p>
      <DeepLinkButton link={{ to: "/today" }} labelKey="chat.deepLink" />
    </div>
  );
}

export function DockMessageView({ message }: { message: MessageState }) {
  const isUser = message.role === "user";
  return (
    <div
      className={`chat-msg ${isUser ? "chat-msg--user" : "chat-msg--assistant"}`}
      data-role={message.role}
      data-testid={isUser ? "chat-msg-user" : "chat-msg-assistant"}
    >
      {message.content.map((part, index) => {
        if (part.type === "text") {
          return part.text.length > 0 ? (
            // biome-ignore lint/suspicious/noArrayIndexKey: message parts are a stable ordered list
            <p key={`text-${index}`} className="chat-msg__text">
              {part.text}
            </p>
          ) : null;
        }
        if (part.type === "data") {
          const key = `${part.name}-${index}`;
          if (part.name === "envelope") {
            return <ChatEnvelopeView key={key} envelope={part.data as ChatEnvelope} />;
          }
          if (part.name === "card") {
            return <CardView key={key} card={part.data as DockCard} />;
          }
          if (part.name === "failure") {
            return <FailureView key={key} failure={part.data as ChatFailure} />;
          }
          if (part.name === "incomplete") {
            return <TransportFailureView key={key} />;
          }
        }
        return null;
      })}
    </div>
  );
}
