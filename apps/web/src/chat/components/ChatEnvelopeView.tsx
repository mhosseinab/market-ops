import type { ChatEnvelope } from "../types";
import { DeepLinkButton } from "./DeepLinkButton";
import { EvidenceRefs } from "./EvidenceRefs";
import { InlineTableView } from "./InlineTableView";
import { StatementSection } from "./StatementSection";

// Renders a grounded operational response envelope: the seven visually-distinct
// statement-kind sections (CHAT-004), an optional 20-row-capped table (CHAT-023),
// the evidence refs/age/quality (CHAT-005), and the deep link back to structured
// state (CHAT-006). Evidence renders even when empty (fails closed to a missing
// state), so a claim never appears without its grounding.
export function ChatEnvelopeView({ envelope }: { envelope: ChatEnvelope }) {
  return (
    <div className="chat-envelope" data-testid="chat-envelope">
      {envelope.sections.map((section) => (
        <StatementSection key={section.kind} kind={section.kind} lines={section.lines} />
      ))}
      {envelope.table ? <InlineTableView table={envelope.table} /> : null}
      <EvidenceRefs evidence={envelope.evidence} />
      {envelope.deepLink ? (
        <DeepLinkButton link={envelope.deepLink} testId="chat-envelope-deeplink" />
      ) : null}
    </div>
  );
}
