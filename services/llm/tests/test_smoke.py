"""Smoke test proving the LLM-plane package imports and pytest is wired."""

from llm import __version__


def test_version_is_present() -> None:
    assert __version__ == "0.0.0"
