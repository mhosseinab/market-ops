from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.chat_statement_kind import ChatStatementKind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.chat_evidence_ref import ChatEvidenceRef


T = TypeVar("T", bound="ChatStatement")


@_attrs_define
class ChatStatement:
    """One typed statement inside the final chat envelope.

    Attributes:
        kind (ChatStatementKind): The seven typed statement kinds an LLM-plane response envelope carries (S29/S37
            addendum). Free text alone never carries authority — a statement is category-separated content, not an approval
            control.
        text (str): The rendered text for this statement. Free text; carries no authority.
        evidence (list[ChatEvidenceRef] | Unset):
        card_id (UUID | Unset): Present only for a card_reference statement (never a control).
        table_rows (list[list[str]] | Unset): Present only for a table statement. Rows of string cells.
    """

    kind: ChatStatementKind
    text: str
    evidence: list[ChatEvidenceRef] | Unset = UNSET
    card_id: UUID | Unset = UNSET
    table_rows: list[list[str]] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind = self.kind.value

        text = self.text

        evidence: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = []
            for evidence_item_data in self.evidence:
                evidence_item = evidence_item_data.to_dict()
                evidence.append(evidence_item)

        card_id: str | Unset = UNSET
        if not isinstance(self.card_id, Unset):
            card_id = str(self.card_id)

        table_rows: list[list[str]] | Unset = UNSET
        if not isinstance(self.table_rows, Unset):
            table_rows = []
            for table_rows_item_data in self.table_rows:
                table_rows_item = table_rows_item_data

                table_rows.append(table_rows_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "text": text,
            }
        )
        if evidence is not UNSET:
            field_dict["evidence"] = evidence
        if card_id is not UNSET:
            field_dict["cardId"] = card_id
        if table_rows is not UNSET:
            field_dict["tableRows"] = table_rows

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.chat_evidence_ref import ChatEvidenceRef

        d = dict(src_dict)
        kind = ChatStatementKind(d.pop("kind"))

        text = d.pop("text")

        _evidence = d.pop("evidence", UNSET)
        evidence: list[ChatEvidenceRef] | Unset = UNSET
        if _evidence is not UNSET:
            evidence = []
            for evidence_item_data in _evidence:
                evidence_item = ChatEvidenceRef.from_dict(evidence_item_data)

                evidence.append(evidence_item)

        _card_id = d.pop("cardId", UNSET)
        card_id: UUID | Unset
        if isinstance(_card_id, Unset):
            card_id = UNSET
        else:
            card_id = UUID(_card_id)

        _table_rows = d.pop("tableRows", UNSET)
        table_rows: list[list[str]] | Unset = UNSET
        if _table_rows is not UNSET:
            table_rows = []
            for table_rows_item_data in _table_rows:
                table_rows_item = cast(list[str], table_rows_item_data)

                table_rows.append(table_rows_item)

        chat_statement = cls(
            kind=kind,
            text=text,
            evidence=evidence,
            card_id=card_id,
            table_rows=table_rows,
        )

        return chat_statement
