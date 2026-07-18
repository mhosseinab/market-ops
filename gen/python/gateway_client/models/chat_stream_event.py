from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.chat_stream_event_kind import ChatStreamEventKind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.chat_failure import ChatFailure
    from ..models.chat_stream_event_envelope import ChatStreamEventEnvelope


T = TypeVar("T", bound="ChatStreamEvent")


@_attrs_define
class ChatStreamEvent:
    """The JSON payload carried in each SSE `data:` frame from /chat. Documented so every consumer (web, tests) decodes the
    same shape; the wire transport is text/event-stream, not a JSON response body. `kind` discriminates the frame;
    `token` carries incremental assistant text; `conversationId` is emitted once when a new conversation is opened;
    `envelope` carries the final typed response envelope on the `final` frame; `failure` carries the §12.4 structured
    failure state. No frame ever carries an approval control.

        Attributes:
            kind (ChatStreamEventKind): Frame discriminator.
            conversation_id (UUID | Unset): Emitted once on the `conversation` frame for a new conversation.
            token (str | Unset): Incremental assistant text on a `token` frame.
            envelope (ChatStreamEventEnvelope | Unset): The final typed response envelope on a `final` frame. Its internal
                shape (category-separated content, evidence, freshness) is owned and validated inside the LLM plane (§12.2).
                UNCHANGED in S37: narrowing this field to the new ChatEnvelope schema (below) is a breaking change for the S29
                web consumer's current view-model (`{sections, evidence}`) and needs FE coordination outside this step's
                delegation boundary — see the S37 PD-3 addendum note on ChatEnvelope. The gateway relays this verbatim.
            failure (ChatFailure | Unset): The §12.4 structured failure state: after one automatic retry, a concise message
                plus a deep link to the structured screen. Free text only; no authority. Emitted as the `failure` frame, or when
                a hard bound (turn recursion limit, tool-call limit, timeout, token ceiling) is exceeded.
    """

    kind: ChatStreamEventKind
    conversation_id: UUID | Unset = UNSET
    token: str | Unset = UNSET
    envelope: ChatStreamEventEnvelope | Unset = UNSET
    failure: ChatFailure | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind = self.kind.value

        conversation_id: str | Unset = UNSET
        if not isinstance(self.conversation_id, Unset):
            conversation_id = str(self.conversation_id)

        token = self.token

        envelope: dict[str, Any] | Unset = UNSET
        if not isinstance(self.envelope, Unset):
            envelope = self.envelope.to_dict()

        failure: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure, Unset):
            failure = self.failure.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
            }
        )
        if conversation_id is not UNSET:
            field_dict["conversationId"] = conversation_id
        if token is not UNSET:
            field_dict["token"] = token
        if envelope is not UNSET:
            field_dict["envelope"] = envelope
        if failure is not UNSET:
            field_dict["failure"] = failure

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.chat_failure import ChatFailure
        from ..models.chat_stream_event_envelope import ChatStreamEventEnvelope

        d = dict(src_dict)
        kind = ChatStreamEventKind(d.pop("kind"))

        _conversation_id = d.pop("conversationId", UNSET)
        conversation_id: UUID | Unset
        if isinstance(_conversation_id, Unset):
            conversation_id = UNSET
        else:
            conversation_id = UUID(_conversation_id)

        token = d.pop("token", UNSET)

        _envelope = d.pop("envelope", UNSET)
        envelope: ChatStreamEventEnvelope | Unset
        if isinstance(_envelope, Unset):
            envelope = UNSET
        else:
            envelope = ChatStreamEventEnvelope.from_dict(_envelope)

        _failure = d.pop("failure", UNSET)
        failure: ChatFailure | Unset
        if isinstance(_failure, Unset):
            failure = UNSET
        else:
            failure = ChatFailure.from_dict(_failure)

        chat_stream_event = cls(
            kind=kind,
            conversation_id=conversation_id,
            token=token,
            envelope=envelope,
            failure=failure,
        )

        return chat_stream_event
