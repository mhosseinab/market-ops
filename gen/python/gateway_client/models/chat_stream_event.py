from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.chat_stream_event_kind import ChatStreamEventKind
from ..models.conversation_context_kind import ConversationContextKind
from ..models.supported_locale import SupportedLocale
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
            context_kind (ConversationContextKind | Unset): The kind of entity a conversation's single deterministic context
                is bound to (PRD §8.1 CHAT-007). One canonical §15.1 record kind per value; a conversation has EXACTLY ONE
                active context at a time, never inferred from free text. `global` is the no-entity context (the operator's whole
                account). The gateway is authoritative for the bound kind — this field is the client's DECLARED binding,
                validated and versioned server-side.
            context_entity_id (str | Unset): Echoed on the `conversation` frame: the entity id of the conversation's
                AUTHORITATIVE bound context (an LTR technical identifier), so the client renders the chip the gateway actually
                persisted, never a claimed one. Absent for the `global` context.
            context_version (int | Unset): Echoed on the `conversation` frame: the conversation's current server-issued
                context version. The client sends it back on the next turn so a stale binding is rejected rather than silently
                relabeled.
            locale_tag (SupportedLocale | Unset): The CLOSED set of locales the application supports (PRD §11.1, LOC-001). A
                BCP-47 language tag treated purely as DATA — it selects a locale pack (direction, digits, calendar, catalog)
                with NO locale/calendar/currency branch in core logic. The set mirrors the web locale package's declared
                locales; adding a locale is a new entry here plus a locale pack, never a code branch. The gateway validates
                every chat turn's locale against this set and fails closed on anything outside it — locale is never inferred.
            locale_version (int | Unset): Echoed on the `conversation` frame: the conversation's current server-issued
                locale-binding version. The client sends it back on the next turn so a locale change is an explicit, versioned
                transition and a stale binding is rejected rather than silently relabeled.
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
    context_kind: ConversationContextKind | Unset = UNSET
    context_entity_id: str | Unset = UNSET
    context_version: int | Unset = UNSET
    locale_tag: SupportedLocale | Unset = UNSET
    locale_version: int | Unset = UNSET
    token: str | Unset = UNSET
    envelope: ChatStreamEventEnvelope | Unset = UNSET
    failure: ChatFailure | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind = self.kind.value

        conversation_id: str | Unset = UNSET
        if not isinstance(self.conversation_id, Unset):
            conversation_id = str(self.conversation_id)

        context_kind: str | Unset = UNSET
        if not isinstance(self.context_kind, Unset):
            context_kind = self.context_kind.value

        context_entity_id = self.context_entity_id

        context_version = self.context_version

        locale_tag: str | Unset = UNSET
        if not isinstance(self.locale_tag, Unset):
            locale_tag = self.locale_tag.value

        locale_version = self.locale_version

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
        if context_kind is not UNSET:
            field_dict["contextKind"] = context_kind
        if context_entity_id is not UNSET:
            field_dict["contextEntityId"] = context_entity_id
        if context_version is not UNSET:
            field_dict["contextVersion"] = context_version
        if locale_tag is not UNSET:
            field_dict["localeTag"] = locale_tag
        if locale_version is not UNSET:
            field_dict["localeVersion"] = locale_version
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

        _context_kind = d.pop("contextKind", UNSET)
        context_kind: ConversationContextKind | Unset
        if isinstance(_context_kind, Unset):
            context_kind = UNSET
        else:
            context_kind = ConversationContextKind(_context_kind)

        context_entity_id = d.pop("contextEntityId", UNSET)

        context_version = d.pop("contextVersion", UNSET)

        _locale_tag = d.pop("localeTag", UNSET)
        locale_tag: SupportedLocale | Unset
        if isinstance(_locale_tag, Unset):
            locale_tag = UNSET
        else:
            locale_tag = SupportedLocale(_locale_tag)

        locale_version = d.pop("localeVersion", UNSET)

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
            context_kind=context_kind,
            context_entity_id=context_entity_id,
            context_version=context_version,
            locale_tag=locale_tag,
            locale_version=locale_version,
            token=token,
            envelope=envelope,
            failure=failure,
        )

        return chat_stream_event
