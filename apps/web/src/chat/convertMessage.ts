import type { ThreadMessageLike } from "@assistant-ui/react";
import type { DockMessage } from "./types";

// One element of an assistant message's content (text or a custom data part).
type ContentPart = Exclude<ThreadMessageLike["content"], string>[number];

// Converts our domain messages into assistant-ui `ThreadMessageLike`s for the
// external-store runtime. Structured content rides as `data` message parts
// ({ type: "data", name, data }) so OUR components render them as custom parts —
// assistant-ui only lays out text + parts and NEVER owns a card or an approval
// action. Envelope + cards are opaque `data` to the library.

export function toThreadMessageLike(message: DockMessage): ThreadMessageLike {
  if (message.role === "user") {
    return {
      role: "user",
      id: message.id,
      content: [{ type: "text", text: message.text }],
    };
  }

  const content: ContentPart[] = [];
  if (message.text.length > 0) content.push({ type: "text", text: message.text });
  if (message.envelope) content.push({ type: "data", name: "envelope", data: message.envelope });
  for (const card of message.cards) content.push({ type: "data", name: "card", data: card });
  if (message.failure) content.push({ type: "data", name: "failure", data: message.failure });
  // A transport-seam failure (truncation / malformed / EOF without terminal —
  // issue #116) carries no structured `failure` payload; it rides as its own part
  // so the view renders an unmistakable incomplete notice, never a completed turn.
  if (message.transportFailed) content.push({ type: "data", name: "incomplete", data: {} });
  // A part is always present so a streaming assistant turn with no text yet still
  // renders (assistant-ui shows the running indicator via message status).
  if (content.length === 0) content.push({ type: "text", text: "" });

  return {
    role: "assistant",
    id: message.id,
    content,
    status:
      message.status === "streaming"
        ? { type: "running" }
        : message.status === "failed"
          ? { type: "incomplete", reason: "error" }
          : { type: "complete", reason: "stop" },
  };
}
