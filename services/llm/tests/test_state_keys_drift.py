"""CHAT-022 copy-lint: canonical state keys exist in the packages/locale catalog.

The grounding walker only accepts state terms drawn from
:data:`CANONICAL_STATE_KEYS`. This test guards those keys against drift: every
one MUST be a real key in the shipped fa-IR catalog, so a rename/removal in the
catalog breaks this test rather than silently letting the Python allow-list point
at a term the UI no longer uses. No invented synonyms.
"""

from __future__ import annotations

import re
from pathlib import Path

from llm.envelope.grounding import CANONICAL_QUALITY_KEYS, CANONICAL_STATE_KEYS

# services/llm/tests/<file> → repo root is four parents up.
_REPO_ROOT = Path(__file__).resolve().parents[3]
_CATALOG = _REPO_ROOT / "packages" / "locale" / "src" / "catalog" / "fa-IR.ts"

_KEY_RE = re.compile(r'"([A-Za-z0-9_.]+)"\s*:')


def _catalog_keys() -> set[str]:
    text = _CATALOG.read_text(encoding="utf-8")
    return set(_KEY_RE.findall(text))


def test_catalog_file_exists() -> None:
    assert _CATALOG.is_file(), f"missing canonical catalog: {_CATALOG}"


def test_every_canonical_state_key_exists_in_catalog() -> None:
    catalog = _catalog_keys()
    missing = CANONICAL_STATE_KEYS - catalog
    assert not missing, f"canonical state keys not present in fa-IR catalog: {sorted(missing)}"


def test_quality_keys_are_a_catalog_backed_subset_of_state_keys() -> None:
    catalog = _catalog_keys()
    assert CANONICAL_QUALITY_KEYS <= CANONICAL_STATE_KEYS
    missing = CANONICAL_QUALITY_KEYS - catalog
    assert not missing, f"canonical quality keys not present in fa-IR catalog: {sorted(missing)}"
