"""Provider boundary tests (§12.1): OpenAI-compatible only, config-selected."""

from __future__ import annotations

from llm.config import ProviderKind, Settings
from llm.providers.base import build_chat_model
from llm.providers.mock import MockChatModel
from pydantic import SecretStr


def test_mock_is_default_provider() -> None:
    """CI / tests use the deterministic mock — no paid calls anywhere."""
    settings = Settings()
    assert settings.provider_kind is ProviderKind.MOCK
    model = build_chat_model(settings)
    assert isinstance(model, MockChatModel)


def test_openai_compatible_uses_configured_base_url() -> None:
    """The real transport is ChatOpenAI(base_url=...) — one owned port."""
    settings = Settings(
        provider_kind=ProviderKind.OPENAI_COMPATIBLE,
        provider_base_url="http://mock-openai:8080/v1",
        provider_api_key=SecretStr("ref-only"),
        provider_model="qualified-model",
        provider_timeout_seconds=12.0,
        max_output_tokens=256,
    )
    model = build_chat_model(settings)
    # It is a langchain-openai ChatOpenAI bound to the configured endpoint.
    from langchain_openai import ChatOpenAI

    assert isinstance(model, ChatOpenAI)
    assert str(model.openai_api_base) == "http://mock-openai:8080/v1"
    assert model.model_name == "qualified-model"


def test_no_vendor_sdk_branch_in_provider_module() -> None:
    """The provider module imports no vendor SDK beyond langchain-openai."""
    import llm.providers.base as base_module

    src = _module_source(base_module)
    banned_imports = (
        "import anthropic",
        "import google.generativeai",
        "import cohere",
        "import boto3",
    )
    for banned in banned_imports:
        assert banned not in src, f"provider must not branch to a vendor SDK: {banned!r}"


def _module_source(module) -> str:  # type: ignore[no-untyped-def]
    import inspect

    return inspect.getsource(module)
