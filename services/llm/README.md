# LLM Service

This package (`services/llm`) implements the Python FastAPI LLM plane for the DK Marketplace Intelligence platform. It provides a read-only and Draft-only LLM service with strict resource bounding and fail-closed security guarantees.

## Architecture

*   **Frameworks**: Built on **FastAPI** for HTTP/SSE transport and **LangGraph** / **LangChain** for orchestrating conversational turns.
*   **No Database Access**: The service has no database credentials (PRD §19.3). Conversation state and durability are entirely the responsibility of the gateway. The state managed here is strictly per-request and in-process.
*   **Orchestration**: LangGraph acts as the sole orchestrator. A conversation turn is modeled as a `StateGraph` that manages intent classification and agent execution.
*   **Provider Abstraction**: It exclusively uses an OpenAI-compatible interface. There is no vendor SDK branch. By configuration, it binds to either an in-process deterministic `Mock` provider (for tests/CI) or a remote `OpenAI_Compatible` endpoint.
*   **Containment & Observability**: Includes explicit intent classification, early containment for unclassifiable intents, and observability tools (Sentry Spotlight, LangSmith) configurable via environment variables.

### Module Map

The package is organized into focused subpackages. The diagram below shows how they depend on each other at runtime — arrows point from a consumer to the module it uses.

```mermaid
graph TD
    APP[app.py<br/>FastAPI app + /chat SSE endpoint]
    ASGI[asgi.py<br/>Uvicorn entrypoint]
    CFG[config.py<br/>LLM_* settings + bounds]
    OBS[observability.py<br/>Sentry / LangSmith]
    MET[metrics.py<br/>Containment metrics]

    GRAPH[orchestrator/graph.py<br/>TurnGraph: classify → contain → agent]
    AGENT[orchestrator/agent.py<br/>Leaf agent + middlewares]
    CANCEL[orchestrator/cancellation.py<br/>CancelToken context var]

    CLS[intents/classifier.py<br/>IntentClassifier]
    INTMOD[intents/models.py<br/>8 classes + routing]
    NORM[intents/normalize.py<br/>Input normalization]
    KEYWORD[intents/keyword_mock.py<br/>Deterministic mock router]
    CAP[intents/capabilities.py<br/>Per-intent tool grants]

    DISP[flows/dispatch.py<br/>contain guidance-only]
    FLOWS[flows/*<br/>briefing, investigation, simulation, monitoring, actions, blockers, gateway_draft]
    PORTS[flows/ports.py<br/>DraftPort + read ports]
    DL[flows/deep_links.py<br/>Recovery route allowlist]

    ENV[envelope/models.py<br/>Money, AssistantAnswer, TurnFailure, ChatStreamEvent]
    COMP[envelope/composer.py<br/>Envelope assembly]
    GROUND[envelope/grounding.py<br/>Evidence grounding]
    CONTRACT[envelope/contract.py<br/>Shared contract helpers]

    REG[tools/registry.py<br/>Read + Draft tool registry]
    BIND[tools/binding.py<br/>Agent tool binding]
    CTX[contextres/resolver.py<br/>Entity/context resolution]

    PROV[providers/base.py<br/>build_chat_model port]
    MOCK[providers/mock.py<br/>Deterministic mock]
    OAI[providers/openai_compatible.py<br/>ChatOpenAI adapter]
    TRA[providers/transient.py<br/>Transient vs non-retryable taxonomy]

    EVAL[evals/*<br/>Offline eval harness + datasets]

    APP --> CFG
    APP --> OBS
    APP --> MET
    APP --> GRAPH
    APP --> CLS
    APP --> REG
    APP --> AGENT
    APP --> ENV
    ASGI --> APP

    GRAPH --> CLS
    GRAPH --> DISP
    GRAPH --> AGENT
    GRAPH --> ENV
    GRAPH --> DL
    GRAPH --> TRA

    CLS --> INTMOD
    CLS --> NORM
    CLS --> KEYWORD
    INTMOD --> CAP

    DISP --> INTMOD
    DISP --> DL
    FLOWS --> PORTS
    FLOWS --> DL

    AGENT --> REG
    AGENT --> ENV
    AGENT --> CANCEL

    REG --> BIND
    PORTS --> CTX

    PROV --> MOCK
    PROV --> OAI
    OAI --> TRA

    ENV --> DL
    COMP --> ENV
    GROUND --> ENV
```

### Request Lifecycle

The diagram below traces a single `/chat` turn from the inbound HTTP request through SSE-framed output. It reflects the actual control flow in `app.py::_stream_turn`, `TurnGraph.astream_turn`, `_classify_and_contain`, and `_astream_agent`.

```mermaid
sequenceDiagram
    participant GW as Go Gateway
    participant FA as FastAPI app.py
    participant AUTH as require_gateway_credential
    participant TG as TurnGraph.astream_turn
    participant CLS as IntentClassifier
    participant DISP as flows.dispatch.contain
    participant AG as Leaf Agent
    participant REG as Tool Registry
    participant PR as Provider (Mock / OpenAI)
    participant SSE as SSE transport

    GW->>FA: POST /chat (Authorization: Bearer ...)
    FA->>AUTH: route dependency
    AUTH->>AUTH: hmac.compare_digest(provided, expected)
    alt missing/mismatched
        AUTH-->>GW: 401 Unauthorized (fail closed)
    end
    alt chat_disabled_for(account)
        FA-->>GW: 503 CHAT_DISABLED (structured)
    end

    FA->>SSE: StreamingResponse(text/event-stream)
    SSE-->>GW: event: conversation {conversation_id}

    FA->>TG: astream_turn(TurnState)
    TG->>CLS: classify(message)
    CLS->>CLS: normalize → small-model IntentClassification → route
    alt unclassifiable
        TG-->>SSE: failure INTENT_UNCLASSIFIED
    end

    TG->>DISP: contain(intent)
    alt ApproveAction / ConfirmResult
        DISP-->>TG: GuidanceOnly (no transition)
        TG-->>SSE: final {guidance} (no tokens)
    else tool-capable
        TG->>AG: astream(messages, recursion_limit)
        loop until structured_response
            AG->>REG: tool call (read / draft)
            REG->>PR: read gateway / Draft port
            AG->>PR: model.generate
            PR-->>AG: AIMessageChunk tokens
            AG-->>SSE: token {text}
        end
        AG-->>TG: structured_response (AssistantAnswer)
        TG->>TG: envelope_from_structured (JSON mode, Money string mantissa)
        TG-->>SSE: final {envelope}
    end

    alt hard bound trips (recursion / tool limit / timeout / token ceiling)
        AG-->>TG: mapped TurnFailure
        TG-->>SSE: failure {code, deep_link}
    end

    SSE-->>GW: stream complete
```

### LangGraph Turn Topology

The P0 turn is a single-node `StateGraph` (post-P0 adds specialist agent nodes without re-architecture). The node runs the deterministic classify-and-contain gate first; only a tool-capable intent reaches the embedded leaf agent.

```mermaid
graph LR
    START([START]) --> TURN
    subgraph Turn Node [containment_node]
        direction TB
        C1[Classify intent<br/>small model, no tools]
        C2{Containment<br/>decision}
        C1 --> C2
        C2 -- guidance-only --> G[GuidanceOnly<br/>no transition]
        C2 -- unclassifiable --> F1[Fail closed<br/>INTENT_UNCLASSIFIED]
        C2 -- tool-capable --> A[Leaf agent<br/>read/Draft only]
    end
    TURN --> END([END])
```

### Containment & Routing Decision

The intent taxonomy and the deterministic routing function (`route_intent`) together form the structural firewall. `GUIDANCE_ONLY_INTENTS` is the single source: ApproveAction and ConfirmResult can never reach a tool.

```mermaid
graph TD
    MSG[User free-text message] --> N[normalize_input]
    N --> IC[IntentClassifier<br/>small model, tools=[]]
    IC --> RC[route_intent<br/>pure / total]
    RC --> DISP{disposition}

    DISP -- GUIDANCE_ONLY<br/>Approve / Confirm --> GO[GuidanceOnly<br/>deep_link → /actions]
    GO --> NOX[NO token stream<br/>NO transition]

    DISP -- TOOL_CAPABLE<br/>Question, Simulation,<br/>PrepareAction, ReviewAction,<br/>Administration, Navigation --> AGENT[Leaf agent<br/>read + Draft tools]
    AGENT --> DRAFT[Terminal write<br/>at most a Draft]
```

### Hard Bounds & Failure Mapping

Every §12.4 hard bound is enforced inside the agent's middleware chain and mapped to a single structured `TurnFailure` shape with a deterministic recovery `deep_link`. The same mapping is used on both the buffered (`invoke`) and streamed (`astream`) paths.

```mermaid
graph TD
    INV[agent.graph.invoke / astream] --> MB{{Middleware chain}}
    MB --> RL[GraphRecursionError<br/>recursion_limit]
    MB --> TCL[ToolCallLimitExceededError<br/>global + per-tool run limits]
    MB --> TT[ToolTimeoutError<br/>per_tool_timeout_seconds]
    MB --> TC[TokenCeilingError<br/>finish_reason=length]
    MB --> NR[NonRetryableProviderError<br/>4xx / auth / validation]
    MB --> TR[TransientTurnError<br/>5xx / timeout / reset]

    RL --> MAP[graph._map_hard_bound]
    TCL --> MAP
    TT --> MAP
    TC --> MAP
    NR --> MAP

    MAP --> TF[TurnFailure<br/>code + message + deep_link]
    TF --> FRAME[SSE failure frame]

    TR --> RT{emitted token yet?}
    RT -- no --> RTY[Retry once<br/>node_transient_retries=1]
    RT -- yes --> FC[Fail closed<br/>no stacked retry]
    RTY --> MAP
    FC --> TF
```

### Provider Transport

There is exactly one port (`build_chat_model`) that constructs a chat model. Selection is by configuration — there is never a vendor-SDK branch. The production adapter classifies provider errors at the owned boundary and disables the SDK's hidden retry loop so the graph node remains the sole retry authority.

```mermaid
graph LR
    CFG[Settings.provider_kind] --> PORT[providers.base.build_chat_model]
    PORT -- MOCK --> MK[MockChatModel<br/>in-process, deterministic]
    PORT -- OPENAI_COMPATIBLE --> OAI[TransientClassifyingChatOpenAI<br/>ChatOpenAI base_url=...]
    OAI --> CLS[classify_provider_error]
    OAI --> MR[max_retries=0<br/>SDK retry disabled]
    MK --> TEST[Tests / CI<br/>no paid calls]
    OAI --> EP[Configured OpenAI-compatible endpoint]
```

### Envelope & Money Safety

The typed `AssistantAnswer` is the agent's `response_format`. `Money` enforces int64 mantissa and serializes as a signed decimal STRING on the wire so JS `JSON.parse` cannot lose precision above 2^53.

```mermaid
graph TD
    SR[structured_response] --> ES[envelope_from_structured<br/>model_dump mode=json]
    ES --> AA[AssistantAnswer]
    AA --> SUM[summary: str]
    AA --> EV[evidence: EvidenceRef[]]
    AA --> AM[amounts: Money[]]
    AA --> RV[raw_values: RawEvidenceValue[]]
    AA --> MD[missing_data: str[]]

    AM --> M[Money]
    M --> MI[mantissa: int64<br/>rejects float / bool]
    M --> SE[serializer → str<br/>^-?[0-9]+]
    M --> CC[currency: ISO-4217]
    M --> EX[exponent: int]

    ES --> SSE[final SSE frame<br/>exclude_none=True]
```

## Data Flow

1.  **Authentication**: Inbound requests to the `/chat` endpoint must carry a valid `Authorization: Bearer <token>` (issued by the Go gateway). Missing or mismatched credentials immediately fail closed with a `401 Unauthorized`. Comparison is constant-time (`hmac.compare_digest`).
2.  **Kill Switch**: If `chat_disabled_for(account)` is true, the endpoint returns a structured `503 CHAT_DISABLED` and streams nothing else; `/healthz` and `/registry/manifest` stay fully functional.
3.  **Turn Initialization**: An authenticated `ChatRequest` initiates a stream. A `conversation` SSE frame is emitted first; then an in-process `TurnState` (JSON-safe, no framework objects) is created.
4.  **Intent & Containment**: The TurnGraph first evaluates the user's message through an `IntentClassifier` (small model, no tools). If the intent is unclassifiable, it terminates early and yields a structured failure. If it's a guidance-only intent (ApproveAction / ConfirmResult), it yields guidance immediately — pointing at the external structured control — without invoking the agent and without any token stream.
5.  **Agent Execution & Streaming**: For tool-capable intents, the message is routed to the LangGraph leaf agent, which is streamed incrementally:
    *   **Tokens** (`token`): Free-text chunks generated by the assistant are forwarded directly as SSE frames. Tool-call argument chunks and `ToolMessage` echoes are filtered out so a token can never carry an authoritative number reconstructed from the stream.
    *   **Final Envelope** (`final`): Once the tool calls complete, the typed, validated `AssistantAnswer` is serialized through `model_dump(mode="json")` so `Money.mantissa` reaches the wire as a signed-decimal STRING, and sent as the terminal `final` frame.
    *   **Failure** (`failure`): If any hard bound trips (graph recursion, tool-call limit, per-tool timeout, token ceiling, non-retryable provider error) or a transient error fails after exactly one retry, a typed failure frame with a deterministic recovery `deep_link` is emitted.
6.  **Cancellation**: A client disconnect closes the upstream async generator; `CancelledError` / `GeneratorExit` re-raise so no further work runs.
7.  **Serialization**: The response is streamed back via Server-Sent Events (SSE). The `Money` representation is strictly managed, serializing mantissas as signed decimal strings to prevent JS-number precision loss on the client side.

## Design Objectives

*   **Bounded Execution**: The system implements rigorous hard bounds to prevent unbounded resource consumption or runaway loops:
    *   `graph_recursion_limit` (turn-level step limit, default 24).
    *   `tool_call_run_limit` (global per-turn limit, default 12) & `per_tool_call_run_limit` (default 4).
    *   `per_tool_timeout_seconds` (time limit for external tool lookup, default 15s).
    *   `max_output_tokens` (strict token ceiling, default 1024; truncating leads to failure, not silent data loss).
    *   `node_transient_retries` (exactly one node-level retry; never stacked with SDK retry).
*   **Fail-Closed Security**: The service does not attempt to guess or coerce behavior. Unauthenticated requests, missing/misordered configs, invalid intents, or exceeding hard bounds result in deterministic structured failures with explicit deep links to fallback UI screens. The recovery route allowlist (`flows/deep_links.py`) prevents a failure fallback from becoming an open redirect.
*   **Vendor Agnosticism**: Configuration specifies the model provider base URL, ensuring the code is not locked to any single vendor's SDK. All external LLMs must conform to the OpenAI API structure, reached through exactly one port (`providers/base.py::build_chat_model`).
*   **Precision Safety**: Financial values are handled strictly using a `Money` Pydantic model enforcing `int64` ranges and wire-form string serialization. The model strictly prevents floating-point coercion, preserving money invariants.
*   **Structural Containment**: The tool registry admits only `READ` and `DRAFT` kinds; a name-level guard rejects any tool name containing a state-changing verb (`approve`, `execute`, `confirm`, `commit`, `publish`, `guardrail`, `permission`, `grant`, `floor`, `cooldown`, `movement_cap`, `override`, `authorize`). Two defenses, one source.

## Operational Notes

*   **Endpoints**:
    *   `GET  /healthz` — public liveness probe.
    *   `GET  /registry/manifest` — the read/Draft-only tool manifest (gateway-authenticated).
    *   `POST /chat` — a conversation turn streamed as SSE (gateway-authenticated).
*   **Configuration**: All runtime parameters are read from `LLM_*` environment variables via `pydantic-settings` (`services/llm/src/llm/config.py`). Defaults are safe for tests and local dev (mock provider, observability off, chat enabled).
*   **Evals**: Offline adversarial, factual, injection, and cost evals live under `services/llm/src/llm/evals` and run via `python -m llm.evals`. They never call a paid endpoint by default.

## Provider Error Classification and Retry Decision Tree

The diagram below illustrates the exact provider error classification and retry decision tree (from `providers/transient.py`). It shows how raw exceptions from the OpenAI-compatible or HTTP transport are deterministically mapped to retryable vs non-retryable failures.

```mermaid
graph TD
    Error[Provider / Transport Exception] --> Classify[classify_provider_error]
    
    Classify --> IsTransport{Is Transport Error?}
    IsTransport -->|Yes: Timeout, Connect, Network| Retryable[TransientTurnError]
    IsTransport -->|No| IsAPIStatus{Is APIStatusError?}
    
    IsAPIStatus -->|Yes| CheckStatus{Status Code?}
    CheckStatus -->|408, 409, 429, >=500| Retryable
    CheckStatus -->|Other 4xx| NonRetryable[NonRetryableProviderError]
    
    IsAPIStatus -->|No| IsOtherError{Is APIError / HTTPError?}
    IsOtherError -->|Yes| NonRetryable
    IsOtherError -->|No| Unknown[Propagate Exception Unchanged]
    
    Retryable --> NodeRetry{Has Emitted Tokens?}
    NodeRetry -->|No| Retry[Retry exactly once at Node]
    NodeRetry -->|Yes| FailClosed[Fail closed, no stacked retry]
    
    NonRetryable --> MapFailure[Map to MODEL_PROVIDER_ERROR]
    MapFailure --> TerminalFailure[Terminal SSE Failure Frame]
```
