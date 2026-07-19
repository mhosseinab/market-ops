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

import asyncio
import json
from collections.abc import AsyncIterator, Callable, Iterator, Sequence
from dataclasses import dataclass, field
from typing import Any

from langchain_core.callbacks import (
    AsyncCallbackManagerForLLMRun,
    CallbackManagerForLLMRun,
)
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
    # Natural-language content chunks streamed (as ``AIMessageChunk`` content)
    # BEFORE the terminal structured tool call, modelling a provider that streams
    # its answer token-by-token (#23). Empty ⇒ no incremental content. These are
    # free-text tokens ONLY — the structured Money-bearing answer rides the
    # terminal tool-call chunk, never a reconstructed number in a token.
    stream_chunks: tuple[str, ...] = ()
    # Async delay before EACH streamed chunk (seconds). Lets a test prove a token
    # is delivered while the run is still in flight (progressive delivery).
    chunk_delay_seconds: float = 0.0
    # Optional observability hook: a mutable list the async stream appends to per
    # emitted content chunk, so a cancellation test can prove upstream emission
    # stopped after the consumer disconnected. Not part of the model contract.
    emit_counter: list[int] | None = None


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
        if s.response_tool_name == "IntentClassification" and s.intent_classifier is not None:
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

    def _content_chunks(self) -> tuple[str, ...]:
        """The natural-language content chunks to stream before the terminal chunk."""
        s = self.script
        if s.stream_chunks:
            return s.stream_chunks
        if s.mode == "say":
            return tuple(word + " " for word in s.content.split(" "))
        return ()

    def _terminal_chunk(self, messages: list[BaseMessage] | None) -> ChatGenerationChunk:
        """The final streamed chunk: carries the tool call (answer/loop) + metadata.

        Streaming a structured answer means the response-schema tool call rides a
        ``tool_call_chunks`` entry (empty text content), so the token stream never
        contains the Money-bearing args as free text (#73, §9.1). ``finish_reason``
        rides ``response_metadata`` so the token-ceiling guard fails closed on a
        truncated streamed completion exactly as it does on a buffered one (§12.4).
        """
        s = self.script
        metadata = {"finish_reason": s.finish_reason} if s.finish_reason is not None else {}
        if s.mode == "answer":
            args = json.dumps(self._response_args(messages))
            return ChatGenerationChunk(
                message=AIMessageChunk(
                    content="",
                    id="mock-stream",
                    tool_call_chunks=[
                        {
                            "name": s.response_tool_name,
                            "args": args,
                            "id": "mock-answer",
                            "index": 0,
                        }
                    ],
                    response_metadata=metadata,
                )
            )
        if s.mode == "loop_tool":
            return ChatGenerationChunk(
                message=AIMessageChunk(
                    content="",
                    id="mock-stream",
                    tool_call_chunks=[
                        {"name": s.loop_tool_name, "args": "{}", "id": "mock-loop", "index": 0}
                    ],
                    response_metadata=metadata,
                )
            )
        return ChatGenerationChunk(
            message=AIMessageChunk(content="", id="mock-stream", response_metadata=metadata)
        )

    def _stream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> Iterator[ChatGenerationChunk]:
        for chunk in self._content_chunks():
            yield ChatGenerationChunk(message=AIMessageChunk(content=chunk, id="mock-stream"))
        yield self._terminal_chunk(messages)

    async def _astream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[ChatGenerationChunk]:
        """Async token streaming — the path the graph's ``astream`` drives (#23).

        Natural-language content chunks are emitted incrementally (with an optional
        per-chunk delay so a test can observe a token mid-run), then the terminal
        tool-call / metadata chunk closes the completion.
        """
        s = self.script
        for chunk in self._content_chunks():
            if s.chunk_delay_seconds:
                await asyncio.sleep(s.chunk_delay_seconds)
            if s.emit_counter is not None:
                s.emit_counter.append(1)
            yield ChatGenerationChunk(message=AIMessageChunk(content=chunk, id="mock-stream"))
        yield self._terminal_chunk(messages)


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
