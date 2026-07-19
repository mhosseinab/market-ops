"""Input normalization at the model-plane boundary (PRD §11.1, CHAT-081/CHAT-080).

Two pure, idempotent transforms run BEFORE any classification:

* :func:`normalize_digits` — Persian (``U+06F0..U+06F9``) and Arabic-Indic
  (``U+0660..U+0669``) digit glyphs fold to Latin ``0-9``. This mirrors, glyph
  for glyph, ``packages/locale/src/format/digits.ts`` so the model plane and the
  web/core boundary normalize digits IDENTICALLY (CHAT-081: Persian and Latin
  digits classify the same). Digit unification happens at the boundary; no
  calculation ever sees a non-Latin digit.
* :func:`normalize_input` — digit folding plus fa-IR character folding (Arabic
  Kaf/Yeh → Persian), whitespace collapse, and mixed-script tokenization that
  keeps ZWNJ-joined Persian words intact (per the fa-IR locale-pack collation /
  tokenization facet, §11.1).

Nothing here infers meaning; it only canonicalizes glyphs and splits tokens.
Ambiguity is never silently resolved (§9.1) — that is the resolver's job, and it
uses a picker, never a guess.
"""

from __future__ import annotations

from dataclasses import dataclass

_PERSIAN_ZERO = 0x06F0
_ARABIC_ZERO = 0x0660
_LATIN_ZERO = 0x30

# fa-IR character folding: Arabic presentation variants → their Persian glyphs.
# This is the locale-pack normalization facet (§11.1), NOT transliteration: it
# only unifies visually-identical letters that Persian and Arabic keyboards emit
# differently, so shorthand typed either way tokenizes identically (CHAT-080).
_CHAR_FOLD: dict[str, str] = {
    "ك": "ک",  # Arabic Kaf ك → Persian Keheh ک
    "ي": "ی",  # Arabic Yeh ي → Persian Yeh ی
    "ى": "ی",  # Arabic Alef Maksura ى → Persian Yeh ی
    "ۀ": "ه‌",  # Heh with hamza above ۀ → Heh + ZWNJ
}

# Zero-width non-joiner: a WITHIN-word joiner in Persian (e.g. می‌شود). It must
# never split a token; it is preserved so a compound word stays one token.
_ZWNJ = "‌"

# Characters that separate tokens. ASCII punctuation plus the Persian comma,
# semicolon, and question mark. Technical identifiers (SKU/URL/ID) are handled by
# the resolver's reference extraction, not here.
_SEPARATORS = frozenset(
    " \t\n\r\f\v"
    ".,:;!?()[]{}<>\"'`|/\\@#=&%*+~^"
    "،"  # Arabic/Persian comma ،
    "؛"  # Arabic semicolon ؛
    "؟"  # Arabic question mark ؟
    "«»"  # « »
)


def _fold_code_point(ch: str) -> str:
    """Fold one character: Persian/Arabic digit → Latin, else char-fold table."""
    code = ord(ch)
    if _PERSIAN_ZERO <= code <= _PERSIAN_ZERO + 9:
        return chr(_LATIN_ZERO + (code - _PERSIAN_ZERO))
    if _ARABIC_ZERO <= code <= _ARABIC_ZERO + 9:
        return chr(_LATIN_ZERO + (code - _ARABIC_ZERO))
    return ch


def normalize_digits(text: str) -> str:
    """Fold every Persian/Arabic digit glyph in ``text`` to Latin ``0-9``.

    Non-digit characters pass through unchanged. Pure and idempotent — the exact
    contract of ``normalizeDigits`` in ``packages/locale`` (CHAT-081).
    """
    return "".join(_fold_code_point(ch) for ch in text)


@dataclass(frozen=True)
class NormalizedInput:
    """The canonicalized form of a raw user message.

    ``raw`` is preserved verbatim (evidence, never discarded); ``text`` is the
    digit- and character-folded, whitespace-collapsed form the classifier sees;
    ``tokens`` is the mixed-script tokenization.
    """

    raw: str
    text: str
    tokens: tuple[str, ...]


def _fold_text(text: str) -> str:
    """Apply digit folding then fa-IR character folding to every glyph."""
    out: list[str] = []
    for ch in text:
        folded = _fold_code_point(ch)
        out.append(_CHAR_FOLD.get(folded, folded))
    return "".join(out)


def canonicalize_key(text: str) -> str:
    """Fold a technical identifier to its canonical match key (§11.1, CHAT-081).

    Applies the SAME boundary folding as :func:`normalize_input` — Persian
    (``U+06F0..U+06F9``) and Arabic-Indic (``U+0660..U+0669``) digits → Latin,
    Arabic Kaf/Yeh → Persian (via the shared ``_CHAR_FOLD`` table) — and then
    drops ZWNJ so a stray in-word joiner never defeats an identifier match. It is
    NOT lowercased: SKUs/IDs are case-sensitive.

    Pure and idempotent. Intended for equality of match keys only; the raw token
    is preserved separately as evidence by the caller (never mutated here).
    """
    return _fold_text(text).replace(_ZWNJ, "")


def tokenize(text: str) -> tuple[str, ...]:
    """Split ``text`` into mixed-script tokens (fa-IR collation facet, §11.1).

    Splits on whitespace and punctuation but PRESERVES ZWNJ inside a token, so a
    Persian compound (``می‌شود``) stays one token. Empty tokens are dropped.
    Assumes ``text`` is already digit/char folded.
    """
    tokens: list[str] = []
    current: list[str] = []
    for ch in text:
        if ch in _SEPARATORS:
            if current:
                tokens.append("".join(current))
                current = []
            continue
        current.append(ch)
    if current:
        tokens.append("".join(current))
    # Drop tokens that are only ZWNJ noise.
    return tuple(t for t in tokens if t.strip(_ZWNJ))


def normalize_input(text: str) -> NormalizedInput:
    """Fold digits + characters, collapse whitespace, and tokenize.

    Pure and deterministic. The returned ``text`` has single-spaced tokens (ZWNJ
    preserved within tokens) and the ``raw`` original is kept as evidence.
    """
    folded = _fold_text(text)
    tokens = tokenize(folded)
    collapsed = " ".join(tokens)
    return NormalizedInput(raw=text, text=collapsed, tokens=tokens)
