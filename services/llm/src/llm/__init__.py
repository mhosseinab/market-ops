"""DK Marketplace Intelligence — Python FastAPI LLM plane (read/Draft-only, no DB).

The LLM plane explains, drafts, and asks — it never decides (PRD §12). Model
access is OpenAI-compatible only; the agent stack is LangGraph (sole
orchestrator) + LangChain ``create_agent`` leaf nodes. The tool registry is
read + Draft-only (§12.3, CHAT-003). No database credential (§19.3).
"""

from typing import Any

__all__ = ["__version__", "create_app"]

__version__: str = "0.0.0"


def create_app(*args: Any, **kwargs: Any) -> Any:
    """Lazy re-export of the FastAPI app factory (keeps package import cheap)."""
    from llm.app import create_app as _create_app

    return _create_app(*args, **kwargs)
