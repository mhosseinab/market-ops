from enum import Enum


class ChatStatementKind(str, Enum):
    ACTION_SUMMARY = "action_summary"
    ANSWER = "answer"
    CARD_REFERENCE = "card_reference"
    CLARIFICATION = "clarification"
    DEGRADED_NOTICE = "degraded_notice"
    EVIDENCE_CITATION = "evidence_citation"
    TABLE = "table"

    def __str__(self) -> str:
        return str(self.value)
