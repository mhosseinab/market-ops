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
"""

from __future__ import annotations

from collections.abc import Iterator, Sequence
from dataclasses import dataclass, field
from typing import Any

from langchain_core.callbacks import CallbackManagerForLLMRun
from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage, AIMessageChunk, BaseMessage
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

    def _ai_message(self) -> AIMessage:
        s = self.script
        if s.mode == "loop_tool":
            return AIMessage(
                content="",
                tool_calls=[{"name": s.loop_tool_name, "args": {}, "id": "mock-loop"}],
            )
        if s.mode == "answer":
            return AIMessage(
                content="",
                tool_calls=[
                    {"name": s.response_tool_name, "args": s.response_args, "id": "mock-answer"}
                ],
            )
        return AIMessage(content=s.content)

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        return ChatResult(generations=[ChatGeneration(message=self._ai_message())])

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
        yield ChatGenerationChunk(message=AIMessageChunk(content=self._ai_message().content))
