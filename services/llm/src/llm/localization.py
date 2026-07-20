"""Bound-locale resolution for the LLM plane (LOC-001/LOC-004, PRD §11.1, §12.2).

Locale is DATA, not a code branch. The gateway is the authoritative source: it
validates and binds the active locale on every chat turn and hands the plane the
bound tag as read-only pass-through business data (issue #120). This module is
the plane's consumption seam — it re-validates that tag against the SAME closed
set the contract enum defines and selects the response/failure catalog by it,
never branching business logic on the value.

The closed set is derived from the generated contract enum
(:class:`gateway_client.models.SupportedLocale`) so the plane can never drift
from the wire contract: adding a locale to the frozen spec re-freezes the enum,
which widens this set automatically.

Fail-closed rules (never-cut §4.6, LOC-001):

* an UNKNOWN tag is rejected outright — never inferred from message text, digit
  shape, region, or account default;
* a MISSING (``None``) tag maps to the ONE explicit authoritative fallback
  policy (LOC-004: English), never a guessed locale.
"""

from __future__ import annotations

from gateway_client.models.supported_locale import SupportedLocale

# The closed set of BCP-47 tags the application supports, sourced from the frozen
# contract enum (single source of truth — no hand-maintained duplicate).
SUPPORTED_LOCALE_TAGS: frozenset[str] = frozenset(locale.value for locale in SupportedLocale)

# LOC-004 authoritative fallback: English is the base/fallback locale. Used ONLY
# for a missing tag, never to paper over an unknown one.
FALLBACK_LOCALE_TAG: str = SupportedLocale.EN.value


class UnknownLocaleError(ValueError):
    """A locale tag outside the closed supported set (fail closed, never inferred)."""


def resolve_locale(tag: str | None, *, fallback: str = FALLBACK_LOCALE_TAG) -> str:
    """Resolve a wire locale tag to a supported one, failing closed on the unknown.

    * ``None`` (missing) ⇒ the explicit ``fallback`` policy (LOC-004). The
      fallback is itself validated, so a misconfigured fallback fails closed.
    * a supported tag ⇒ returned unchanged.
    * anything else ⇒ :class:`UnknownLocaleError` — never coerced, never guessed.
    """
    if tag is None:
        return _validated(fallback)
    return _validated(tag)


def _validated(tag: str) -> str:
    if tag not in SUPPORTED_LOCALE_TAGS:
        raise UnknownLocaleError(
            f"unsupported locale {tag!r}; the closed set is "
            f"{sorted(SUPPORTED_LOCALE_TAGS)} (LOC-001, fail closed — never inferred)"
        )
    return tag
