"""Deterministic OpenAI-compatible mock chat model.

Used for ALL tests and in CI — there are NO paid model calls anywhere in the
test path (§12.5). The mock speaks the same LangChain ``BaseChatModel``
contract as the real ``ChatOpenAI`` transport, so the graph/agent code under
test is byte-for-byte the production path with only the endpoint swapped.

Scripts make behavior explicit and reproducible:

* ``answer`` — emit a tool call for the ``response_format`` schema, producing a
  validated structured response.
* ``say`` — emit plain assistant content (used for token streaming).
* ``loop_tool`` — ALWAYS request a tool call, so an unbounded run is forced;
  the hard bounds (recursion limit / tool-call limit) must stop it.

A script may also set ``finish_reason`` to simulate the provider's completion
finish reason. ``finish_reason="length"`` models an output truncated at the
token ceiling (``max_output_tokens``); the token-ceiling guard must fail closed
on it (§12.4, no silent truncation).
"""

from __future__ import annotations

from collections.abc import Callable, Iterator, Sequence
from dataclasses import dataclass, field
from typing import Any

from langchain_core.callbacks import CallbackManagerForLLMRun
from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage, AIMessageChunk, BaseMessage, HumanMessage
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult


@dataclass
class MockScript:
    """A deterministic script for :class:`MockChatModel`."""

    mode: str = "answer"  # "answer" | "say" | "loop_tool"
    content: str = "This is a deterministic mock response."
    # For "answer": the tool name to call (the response_format schema tool) and
    # the args to return as the structured response.
    response_tool_name: str = "AssistantAnswer"
    response_args: dict[str, Any] = field(
        default_factory=lambda: {"summary": "This is a deterministic mock answer."}
    )
    # For "loop_tool": the tool to keep calling.
    loop_tool_name: str = "read_observation"
    # Simulated provider finish reason attached to every generated message.
    # ``None`` leaves it unset; ``"length"`` models a token-ceiling truncation.
    finish_reason: str | None = None
    # Optional content-sensitive intent classifier (mock provider only). When set
    # AND the response schema tool is IntentClassification, the mock derives the
    # intent from the LAST human message's text instead of a fixed label — so a
    # containment test drives the actual message through classification. Never
    # used with a real endpoint (the model classifies for real there).
    intent_classifier: Callable[[str], str] | None = None


class MockChatModel(BaseChatModel):
    """A ``BaseChatModel`` that returns scripted, deterministic outputs.

    It ignores the input messages' *content* (determinism) but honors the bound
    tools so ``bind_tools`` behaves like the real transport.
    """

    script: MockScript = MockScript()

    model_config = {"arbitrary_types_allowed": True}

    @property
    def _llm_type(self) -> str:
        return "deterministic-mock-openai-compatible"

    def bind_tools(self, tools: Sequence[Any], **kwargs: Any) -> MockChatModel:  # noqa: ANN401
        # The mock does not need the tool schemas; return self so the graph's
        # tool-binding path is identical to production.
        return self

    def _ai_message(self, messages: list[BaseMessage] | None = None) -> AIMessage:
        s = self.script
        metadata = {"finish_reason": s.finish_reason} if s.finish_reason is not None else {}
        if s.mode == "loop_tool":
            return AIMessage(
                content="",
                tool_calls=[{"name": s.loop_tool_name, "args": {}, "id": "mock-loop"}],
                response_metadata=metadata,
            )
        if s.mode == "answer":
            return AIMessage(
                content="",
                tool_calls=[
                    {
                        "name": s.response_tool_name,
                        "args": self._response_args(messages),
                        "id": "mock-answer",
                    }
                ],
                response_metadata=metadata,
            )
        return AIMessage(content=s.content, response_metadata=metadata)

    def _response_args(self, messages: list[BaseMessage] | None) -> dict[str, Any]:
        """The structured response args, content-derived for intent when configured."""
        s = self.script
        if (
            s.response_tool_name == "IntentClassification"
            and s.intent_classifier is not None
        ):
            text = _last_human_text(messages)
            return {"intent": s.intent_classifier(text), "rationale": "mock-keyword"}
        return s.response_args

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        generation_info = (
            {"finish_reason": self.script.finish_reason}
            if self.script.finish_reason is not None
            else None
        )
        generation = ChatGeneration(
            message=self._ai_message(messages), generation_info=generation_info
        )
        return ChatResult(generations=[generation])

    def _stream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> Iterator[ChatGenerationChunk]:
        # Stream the plain content word-by-word for the "say" script; other
        # scripts fall back to a single non-streaming chunk.
        if self.script.mode == "say":
            for word in self.script.content.split(" "):
                yield ChatGenerationChunk(message=AIMessageChunk(content=word + " "))
            return
        content = self._ai_message(messages).content
        yield ChatGenerationChunk(message=AIMessageChunk(content=content))


def _last_human_text(messages: list[BaseMessage] | None) -> str:
    """Extract the last human message's text (for content-aware mock classifying)."""
    if not messages:
        return ""
    for message in reversed(messages):
        if isinstance(message, HumanMessage):
            content = message.content
            return content if isinstance(content, str) else str(content)
    last = messages[-1].content
    return last if isinstance(last, str) else str(last)
