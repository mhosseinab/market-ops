"""The read-only candidate-supply port for context resolution (PRD §8.1).

:func:`~llm.contextres.resolver.resolve` is a pure function: it never fetches.
The authoritative candidates an explicit reference could denote come from
deterministic gateway READS, which is a transport concern. This module is the
dependency-inversion seam for that supply, mirroring
:mod:`llm.flows.ports` (the Draft-only write port).

:class:`CandidatePort` has exactly ONE method and it is a lookup. There is
deliberately NO create / write / draft / approve / execute / confirm /
guardrail-write / permission method: the model plane cannot call what does not
exist (§12.3, CHAT-003). A candidate lookup is a read; it can never move state.

The production default is :class:`NoCandidatePort` — an explicitly-planned stub
that FAILS CLOSED by supplying nothing, so an explicit reference resolves to a
structured picker or NOT_FOUND, never to a guessed subject.
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Protocol

from llm.contextres.models import EntityCandidate, EntityRef, RequestScope

__all__ = ["CandidateLookupError", "CandidatePort", "NoCandidatePort"]


class CandidateLookupError(Exception):
    """A candidate lookup could not be completed — fail closed (§12.4).

    Raised by an implementation on a transport error, a non-2xx response, or a
    malformed body. The turn degrades to the structured failure + deep link; it
    NEVER proceeds with a partial or invented candidate set.
    """


class CandidatePort(Protocol):
    """Supply the authoritative candidates an explicit reference could denote.

    Read-only by construction. The returned mapping is keyed by the reference's
    verbatim ``raw`` token (the resolver folds keys canonically itself, #29) and
    every candidate must carry the organization/account provenance the resolver
    validates against ``scope`` — provenance is data returned by the read, never
    manufactured from the request.
    """

    def candidates_for(
        self, *, scope: RequestScope, references: Sequence[EntityRef]
    ) -> dict[str, list[EntityCandidate]]:
        """Return candidates per reference token, or raise :class:`CandidateLookupError`."""
        ...


class NoCandidatePort:
    """The fail-closed default: no candidate supply is wired yet.

    **Explicitly-planned stub (CLAUDE.md Engineering method).** It supplies NO
    candidates, so an explicit reference deterministically resolves to a
    structured picker (when the turn itself is ambiguous) or to NOT_FOUND — never
    to a guessed subject and never to a card. That is the correct fail-closed
    behavior for "the authoritative read is not reachable from this plane yet".

    **Downstream completer: sub-scope 108c of issue #108** wires the real
    gateway-backed :class:`CandidatePort` (outbound gateway base URL + the
    read/Draft-only token, a typed ``GatewayReadPort``, and production port
    construction in ``AppState``). Until then this stub is the production
    implementation and its negative test proves it never fabricates a subject.
    """

    def candidates_for(
        self, *, scope: RequestScope, references: Sequence[EntityRef]
    ) -> dict[str, list[EntityCandidate]]:
        """Always empty: nothing authoritative is reachable, so nothing is claimed."""
        return {}
