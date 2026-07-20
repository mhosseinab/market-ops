import { createContext, useContext } from "react";
import type { PickerOption } from "./types";

// Actions the chat dock exposes to nested structured cards (CHAT-007). A picker
// option is BOUND to the conversation through a typed continuation turn — an
// explicit, versioned context transition — BEFORE any card-producing request, so
// the conversation is never silently relabeled and the bound entity is the exact
// option the operator chose. The context is provided by the dock; a card rendered
// without a dock (fallback) sees null and keeps its deep-link navigation.
export interface ChatDockActions {
  readonly bindPickerOption: (option: PickerOption) => void;
}

export const ChatDockActionsContext = createContext<ChatDockActions | null>(null);

export function useChatDockActions(): ChatDockActions | null {
  return useContext(ChatDockActionsContext);
}
