import type { DockCard } from "../types";
import { ApprovalCardHost } from "./ApprovalCardHost";
import { Level2Card } from "./Level2Card";
import { PickerCard } from "./PickerCard";

// Dispatches a structured card (mounted as a custom message part) to OUR
// component. assistant-ui never owns any of these; the approval card in
// particular reuses the S27 control + confirm endpoint verbatim.
export function CardView({ card }: { card: DockCard }) {
  switch (card.kind) {
    case "picker":
      return <PickerCard options={card.options} />;
    case "approval":
      return <ApprovalCardHost cardId={card.cardId} />;
    case "level2":
      return <Level2Card proposal={card.proposal} />;
    default:
      return null;
  }
}
