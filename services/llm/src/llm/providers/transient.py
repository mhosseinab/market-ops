"""Transport-failure taxonomy for the owned OpenAI-compatible provider boundary.

The §12.4 single retry lives at the graph node, but the node can only act on
failures it recognizes. The production ``ChatOpenAI`` transport raises the
OpenAI-compatible / httpx exception hierarchy, so THIS module is the single
place those raw exceptions are *classified* — never guessed elsewhere:

* ``TransientTurnError`` — an explicitly-classified retryable transport failure
  (timeout, connection reset, rate limit, retryable server status). Eligible for
  the ONE §12.4 node retry. Also the type a tool transport may raise for its own
  retryable failure; the retry count stays SOLELY at the node.
* ``NonRetryableProviderError`` — an explicitly-classified non-retryable
  provider failure (auth, permission, not-found, validation, other 4xx). NEVER
  retried; the graph maps it to the structured ``MODEL_PROVIDER_ERROR`` failure.

Classification mirrors the OpenAI SDK's own retry policy (``408/409/429`` and
``>= 500`` status codes, plus connection/timeout errors are retryable; every
other explicit 4xx is not) so behaviour is aligned with the transport, not
invented. This module imports ONLY ``openai`` + ``httpx`` (both hard deps) and
nothing from the orchestrator, so it introduces no import cycle.
"""

from __future__ import annotations

from typing import Literal

import httpx
import openai

Classification = Literal["retryable", "non_retryable"]

# HTTP status codes the OpenAI SDK itself retries (see ``_should_retry``): a
# request timeout, a lock timeout, a rate limit, and any server-side 5xx.
_RETRYABLE_STATUS: frozenset[int] = frozenset({408, 409, 429})

# Connection/transport-level failures that never reached a status code — always
# transient. ``openai.APIConnectionError`` covers ``APITimeoutError``; the httpx
# types cover a raw transport error surfacing before the SDK wraps it.
_RETRYABLE_TRANSPORT: tuple[type[BaseException], ...] = (
    openai.APIConnectionError,
    httpx.TimeoutException,
    httpx.ConnectError,
    httpx.NetworkError,
    httpx.RemoteProtocolError,
)


class TransientTurnError(Exception):
    """A transient model/tool failure eligible for the single §12.4 node retry."""


class NonRetryableProviderError(Exception):
    """A provider failure explicitly classified as NON-retryable (§12.4).

    Auth, permission, not-found, unprocessable, and other non-retryable 4xx
    responses. The graph maps it to the ``MODEL_PROVIDER_ERROR`` structured
    failure without retrying — a bad request or credential never improves on a
    second identical attempt.
    """


def classify_provider_error(exc: BaseException) -> Classification | None:
    """Classify a raw provider/transport exception, or ``None`` if it is neither.

    ``None`` means "not a provider transport failure" (e.g. a programming error):
    the caller must let it propagate rather than dress it up as transient.
    """
    if isinstance(exc, _RETRYABLE_TRANSPORT):
        return "retryable"
    if isinstance(exc, openai.APIStatusError):
        status = getattr(exc, "status_code", None)
        if isinstance(status, int) and (status in _RETRYABLE_STATUS or status >= 500):
            return "retryable"
        return "non_retryable"
    if isinstance(exc, (openai.APIError, httpx.HTTPError)):
        # A provider/transport error we did not positively classify as retryable
        # (e.g. a response-validation or protocol error) is failed closed as
        # non-retryable rather than retried blindly.
        return "non_retryable"
    return None
