from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.supported_locale import SupportedLocale
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.conversation_context_binding import ConversationContextBinding


T = TypeVar("T", bound="ChatTurnRequest")


@_attrs_define
class ChatTurnRequest:
    """One conversation turn from the browser. The message is free text and carries NO authority (PRD §8 free-text
    containment): it can never approve, execute, or confirm — those live only in structured controls outside the model
    plane. A turn optionally continues an existing conversation and/or binds a marketplace-account context; context
    resolution itself is deterministic in the LLM plane (§8.1), never guessed from this field.

        Attributes:
            locale (SupportedLocale): The CLOSED set of locales the application supports (PRD §11.1, LOC-001). A BCP-47
                language tag treated purely as DATA — it selects a locale pack (direction, digits, calendar, catalog) with NO
                locale/calendar/currency branch in core logic. The set mirrors the web locale package's declared locales; adding
                a locale is a new entry here plus a locale pack, never a code branch. The gateway validates every chat turn's
                locale against this set and fails closed on anything outside it — locale is never inferred.
            message (str): The user's free-text message. Bounded; carries no authority.
            conversation_id (UUID | Unset): Existing conversation to continue. Absent on the first turn; the gateway opens a
                new conversation and returns its id in the stream.
            locale_version (int | Unset): The conversation's locale-binding version this turn is issued against (mirrors
                `contextVersion`, CHAT-007). Absent on the first turn (the gateway issues version 1). On a continuation whose
                `locale` differs from the conversation's current bound locale it must equal the current bound version; a
                stale/mismatched version is rejected without producing a Draft.
            locale_transition (bool | Unset): Set true to EXPLICITLY transition the conversation's bound locale to a
                different value (appends a new locale version). Without it, a turn whose `locale` differs from the
                conversation's current bound locale is rejected rather than silently relabeling the conversation — a same-locale
                continuation is idempotent and needs neither this flag nor a version.
            marketplace_account_id (UUID | Unset): Optional account context for the turn. Exactly one context is active per
                conversation; ambiguity is resolved by a structured picker, never inferred (CHAT-007).
            context (ConversationContextBinding | Unset): The deterministic context a turn binds to the conversation (PRD
                §8.1 CHAT-007). Route entry and structured picker selection BIND the selected entity here so the gateway
                persists the exact kind/entity the operator sees — the chip can never claim a context the gateway did not
                receive. Binding is SERVER-VERSIONED and APPEND-ONLY: the first turn establishes version 1; changing the bound
                entity requires an EXPLICIT transition (`transition: true`) that appends a new version, never a silent relabel;
                a `contextVersion` that no longer matches the conversation's current bound version is REJECTED (stale) and
                produces no Draft. Account, context entity, and conversation provenance must belong to the same tenant.
    """

    locale: SupportedLocale
    message: str
    conversation_id: UUID | Unset = UNSET
    locale_version: int | Unset = UNSET
    locale_transition: bool | Unset = UNSET
    marketplace_account_id: UUID | Unset = UNSET
    context: ConversationContextBinding | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        locale = self.locale.value

        message = self.message

        conversation_id: str | Unset = UNSET
        if not isinstance(self.conversation_id, Unset):
            conversation_id = str(self.conversation_id)

        locale_version = self.locale_version

        locale_transition = self.locale_transition

        marketplace_account_id: str | Unset = UNSET
        if not isinstance(self.marketplace_account_id, Unset):
            marketplace_account_id = str(self.marketplace_account_id)

        context: dict[str, Any] | Unset = UNSET
        if not isinstance(self.context, Unset):
            context = self.context.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "locale": locale,
                "message": message,
            }
        )
        if conversation_id is not UNSET:
            field_dict["conversationId"] = conversation_id
        if locale_version is not UNSET:
            field_dict["localeVersion"] = locale_version
        if locale_transition is not UNSET:
            field_dict["localeTransition"] = locale_transition
        if marketplace_account_id is not UNSET:
            field_dict["marketplaceAccountId"] = marketplace_account_id
        if context is not UNSET:
            field_dict["context"] = context

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.conversation_context_binding import ConversationContextBinding

        d = dict(src_dict)
        locale = SupportedLocale(d.pop("locale"))

        message = d.pop("message")

        _conversation_id = d.pop("conversationId", UNSET)
        conversation_id: UUID | Unset
        if isinstance(_conversation_id, Unset):
            conversation_id = UNSET
        else:
            conversation_id = UUID(_conversation_id)

        locale_version = d.pop("localeVersion", UNSET)

        locale_transition = d.pop("localeTransition", UNSET)

        _marketplace_account_id = d.pop("marketplaceAccountId", UNSET)
        marketplace_account_id: UUID | Unset
        if isinstance(_marketplace_account_id, Unset):
            marketplace_account_id = UNSET
        else:
            marketplace_account_id = UUID(_marketplace_account_id)

        _context = d.pop("context", UNSET)
        context: ConversationContextBinding | Unset
        if isinstance(_context, Unset):
            context = UNSET
        else:
            context = ConversationContextBinding.from_dict(_context)

        chat_turn_request = cls(
            locale=locale,
            message=message,
            conversation_id=conversation_id,
            locale_version=locale_version,
            locale_transition=locale_transition,
            marketplace_account_id=marketplace_account_id,
            context=context,
        )

        return chat_turn_request
