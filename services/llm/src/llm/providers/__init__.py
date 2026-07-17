"""OpenAI-compatible model access (PRD §12.1).

One owned transport port. ``build_chat_model`` returns a LangChain
``BaseChatModel``: either the deterministic in-process mock (all tests, CI) or a
configured OpenAI-compatible endpoint via ``langchain-openai``. No vendor SDK
branch exists.
"""

from llm.providers.base import build_chat_model
from llm.providers.mock import MockChatModel, MockScript

__all__ = ["build_chat_model", "MockChatModel", "MockScript"]
