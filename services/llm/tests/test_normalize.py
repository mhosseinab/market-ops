"""Input-normalization tests (PRD §11.1, CHAT-080/081).

The headline is the CHAT-081 PROPERTY: Persian and Latin digits normalize
identically. It is exercised over a large deterministic sample of digit strings
rendered in every accepted family, asserting all render to the SAME Latin form
and that normalization is idempotent. Tokenization (fa-IR collation facet) and
character folding are checked table-driven.
"""

from __future__ import annotations

import random

from llm.intents.normalize import (
    canonicalize_key,
    normalize_digits,
    normalize_input,
    tokenize,
)

_PERSIAN_ZERO = 0x06F0
_ARABIC_ZERO = 0x0660


def _to_family(latin: str, zero_code: int) -> str:
    """Render a Latin digit string in the digit family starting at ``zero_code``."""
    return "".join(chr(zero_code + (ord(c) - 0x30)) if c.isdigit() else c for c in latin)


def test_property_persian_and_latin_digits_normalize_identically() -> None:
    """CHAT-081 property: every Persian/Arabic digit string folds to its Latin twin.

    Deterministic sample (fixed seed) of numeric strings — plain integers,
    grouped, decimal, signed, and digit-with-text — each rendered Latin, Persian,
    and Arabic-Indic. All three MUST normalize to the identical Latin string.
    """
    rng = random.Random(20210817)
    samples: list[str] = ["0", "9", "1234567890", "007", "42"]
    for _ in range(2000):
        length = rng.randint(1, 12)
        digits = "".join(rng.choice("0123456789") for _ in range(length))
        template = rng.choice(["{d}", "SKU-{d}", "{d}٬{d}", "قیمت {d} ریال", "-{d}", "{d}.5"])
        samples.append(template.replace("{d}", digits))

    for latin in samples:
        persian = _to_family(latin, _PERSIAN_ZERO)
        arabic = _to_family(latin, _ARABIC_ZERO)

        norm_latin = normalize_digits(latin)
        norm_persian = normalize_digits(persian)
        norm_arabic = normalize_digits(arabic)

        # The core property: identical Latin output regardless of input family.
        assert norm_persian == norm_latin, (latin, persian)
        assert norm_arabic == norm_latin, (latin, arabic)
        # And no non-Latin digit survives.
        assert all(not _is_nonlatin_digit(ch) for ch in norm_persian)
        # Idempotent.
        assert normalize_digits(norm_persian) == norm_persian


def _is_nonlatin_digit(ch: str) -> bool:
    code = ord(ch)
    return (_PERSIAN_ZERO <= code <= _PERSIAN_ZERO + 9) or (
        _ARABIC_ZERO <= code <= _ARABIC_ZERO + 9
    )


def test_normalize_digits_leaves_non_digits_untouched() -> None:
    assert normalize_digits("قیمت buy-box $") == "قیمت buy-box $"


def test_normalize_input_folds_arabic_letters_to_persian() -> None:
    # Arabic Kaf/Yeh fold to Persian glyphs so shorthand typed either way matches.
    out = normalize_input("كيف")  # Arabic kaf + yeh + feh
    assert out.text == "کیف"  # Persian keheh + yeh + feh


def test_tokenize_preserves_zwnj_within_a_word() -> None:
    zwnj = "‌"
    tokens = tokenize(f"می{zwnj}شود قیمت")
    assert tokens == (f"می{zwnj}شود", "قیمت")


def test_tokenize_splits_on_persian_and_ascii_punctuation() -> None:
    assert tokenize("قیمت، بها؟ price!") == ("قیمت", "بها", "price")


def test_normalize_input_collapses_whitespace_and_keeps_raw() -> None:
    out = normalize_input("  قیمت   ۱۲۳  ")
    assert out.text == "قیمت 123"
    assert out.raw == "  قیمت   ۱۲۳  "
    assert out.tokens == ("قیمت", "123")


# --- canonicalize_key: the identifier match-key boundary (#29, CHAT-081) ------


def test_canonicalize_key_folds_digit_families_to_latin() -> None:
    # Persian and Arabic-Indic digit spellings fold to the identical Latin key.
    assert canonicalize_key("SKU-۱۲۳") == "SKU-123"
    assert canonicalize_key("SKU-١٢٣") == "SKU-123"


def test_canonicalize_key_folds_arabic_kaf_and_yeh() -> None:
    # Arabic kaf/yeh fold to Persian glyphs (reusing the shared _CHAR_FOLD table).
    assert canonicalize_key("كيف-1") == "کیف-1"


def test_canonicalize_key_drops_zwnj() -> None:
    zwnj = "‌"
    assert canonicalize_key(f"SKU-1{zwnj}23") == "SKU-123"


def test_canonicalize_key_preserves_case() -> None:
    # SKUs are case-sensitive; canonicalization must not lowercase.
    assert canonicalize_key("Sku-Abc") == "Sku-Abc"


def test_canonicalize_key_is_idempotent() -> None:
    for raw in ("SKU-۱۲۳", "كيف-1", "SKU-1‌23", "ۀخانه", "plain-123"):
        once = canonicalize_key(raw)
        assert canonicalize_key(once) == once, raw
