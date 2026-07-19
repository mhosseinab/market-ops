"""Provider factual-support measured against an INDEPENDENT oracle (issue #118).

CHAT-005 / CHAT-020 require model-authored operational claims to be supported by
authoritative evidence, and the §12.5 factual-support number to be a measurement
of *the configured provider* — an evaluator must tell a qualifying model apart
from one that fabricates, omits, swaps, or misattributes commercial facts.

The composer/grounding disposition check (``score_composer_contract``) is a
valuable DETERMINISTIC contract — a well-evidenced envelope composes, a degraded
one fails closed — but it never invokes a provider, so it is provider-independent
fixture self-consistency, NOT factual accuracy. This module supplies the missing
measurement, mirroring the data-channel injection evaluator (issue #112):

* the authoritative typed tool responses (the facts a real turn would fetch) are
  delivered ONLY through the eval-only ``read_runner_overrides`` READ seam, kept
  SEPARATE from the ground-truth oracle;
* the configured provider is driven through the REAL wired ``TurnGraph``
  (classify → contain → agent → read tools → terminal envelope) — the mock in CI
  (a controlled, scripted provider whose behaviour is the evaluator's knob) or a
  configured ``ChatOpenAI(base_url=…)`` endpoint at the deferred S35 paid gate;
* the generated terminal :class:`~llm.envelope.models.AssistantAnswer` is scored
  against the INDEPENDENT oracle: every expected claim's amount, evidence
  reference, and capture time must be present (recall), no unsupported amount or
  evidence may be added (precision), and a case whose required read/tool path was
  not exercised FAILS — never a vacuous pass.

Like the injection gate, the evaluator proves it is not a rubber stamp: a
``constant``/``fabricate``/``swap_*``/``extra_claim`` provider (the self-check
knob) fails factual support, so a provider that fabricates commercial facts can
never receive the same score as a correct one. The factual score is therefore
provider-dependent — changing the provider changes it.
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass, field
from enum import StrEnum
from typing import Any

from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage, BaseMessage, ToolMessage
from langchain_core.outputs import ChatGeneration, ChatResult

from llm.config import ProviderKind, Settings
from llm.envelope.models import Money
from llm.intents import IntentClassifier
from llm.intents.keyword_mock import default_keyword_intent
from llm.metrics import ContainmentMetrics
from llm.orchestrator.agent import build_agent
from llm.orchestrator.graph import TurnResult, build_turn_graph
from llm.providers.base import build_chat_model
from llm.providers.mock import MockChatModel, MockScript
from llm.tools.registry import READ_TOOL_NAMES, build_registry


class FactualBehavior(StrEnum):
    """The controlled-provider behaviour — the evaluator's self-check knob.

    ``FAITHFUL`` relays the authoritative reads exactly (a qualifying provider).
    Every other value is a distinct failure mode the oracle MUST catch, so the
    suite is not a rubber stamp: fabrication, omission, wrong currency/unit,
    wrong timestamp, wrong evidence reference, wrong entity, and unsupported
    extra claims each fail factual support.
    """

    FAITHFUL = "faithful"
    CONSTANT = "constant"  # ignores the reads, answers a fixed string (omission)
    EMPTY = "empty"  # never reads, empty answer (required path not exercised)
    FABRICATE = "fabricate"  # invents amounts absent from the oracle
    SWAP_CURRENCY = "swap_currency"  # right number, wrong currency/unit
    SWAP_TIMESTAMP = "swap_timestamp"  # right claim, wrong capture time
    SWAP_EVIDENCE = "swap_evidence"  # right claim, wrong evidence reference
    SWAP_ENTITY = "swap_entity"  # reads a different entity than required
    EXTRA_CLAIM = "extra_claim"  # faithful PLUS an unsupported added claim


# ----------------------------------------------------------------------------
# Typed fixture: authoritative reads (provider input) vs oracle (ground truth).
# ----------------------------------------------------------------------------


@dataclass(frozen=True)
class _Fact:
    """One authoritative typed value a READ tool returns for an entity."""

    mantissa: int
    currency: str
    exponent: int
    evidence_id: str
    captured_at: str
    quality: str

    @classmethod
    def from_dict(cls, spec: dict[str, Any]) -> _Fact:
        amount = spec["amount"]
        return cls(
            mantissa=int(amount["mantissa"]),
            currency=str(amount["currency"]),
            exponent=int(amount.get("exponent", 0)),
            evidence_id=str(spec["evidence_id"]),
            captured_at=str(spec["captured_at"]),
            quality=str(spec.get("quality", "state.supported")),
        )


@dataclass(frozen=True)
class _Read:
    """A required READ and the authoritative facts it returns for the entity."""

    tool: str
    facts: tuple[_Fact, ...]


@dataclass(frozen=True)
class ExpectedClaim:
    """One INDEPENDENT ground-truth claim the answer must support.

    Held SEPARATELY from the tool-response payloads: the authoritative amount, the
    evidence reference that backs it, and the capture time. A generated answer
    supports it only when the amount AND its (evidence_id, captured_at) reference
    are both present — claim-specific evidence support, not a bare number match.
    """

    mantissa: int
    currency: str
    exponent: int
    evidence_id: str
    captured_at: str

    @classmethod
    def from_dict(cls, spec: dict[str, Any]) -> ExpectedClaim:
        amount = spec["amount"]
        return cls(
            mantissa=int(amount["mantissa"]),
            currency=str(amount["currency"]),
            exponent=int(amount.get("exponent", 0)),
            evidence_id=str(spec["evidence_id"]),
            captured_at=str(spec["captured_at"]),
        )

    @property
    def amount_key(self) -> tuple[int, str, int]:
        return (self.mantissa, self.currency, self.exponent)

    @property
    def evidence_key(self) -> tuple[str, str]:
        return (self.evidence_id, self.captured_at)


@dataclass(frozen=True)
class FactualCase:
    """A provider factual-support case: prompt + authoritative reads + oracle."""

    case_id: str
    suite: str
    message: str
    account_id: str
    entity_id: str
    reads: tuple[_Read, ...]
    oracle: tuple[ExpectedClaim, ...]

    @classmethod
    def from_row(cls, row: dict[str, Any]) -> FactualCase:
        reads = tuple(
            _Read(
                tool=str(r["tool"]),
                facts=tuple(_Fact.from_dict(f) for f in r.get("facts", [])),
            )
            for r in row.get("reads", [])
        )
        return cls(
            case_id=str(row["id"]),
            suite=str(row.get("suite", "factual")),
            message=str(row["message"]),
            account_id=str(row["marketplace_account_id"]),
            entity_id=str(row["entity_id"]),
            reads=reads,
            oracle=tuple(ExpectedClaim.from_dict(c) for c in row.get("oracle", [])),
        )

    @property
    def required_tools(self) -> frozenset[str]:
        return frozenset(r.tool for r in self.reads)


# ----------------------------------------------------------------------------
# The authoritative READ seam + a delivery ledger (real observed signals).
# ----------------------------------------------------------------------------


@dataclass
class ReadLedger:
    """Observed data-path signals for one case — never assumed.

    Records which READ tools ran, the entity each was called with, and whether
    the tool actually returned authoritative facts (an entity mismatch returns
    none). The scorer uses it so a case can never pass without exercising its
    required read path against the correct entity.
    """

    expected_entity: str
    called_with: dict[str, str] = field(default_factory=dict)
    facts_returned: set[str] = field(default_factory=set)

    def tool_read_correct_entity(self, tool: str) -> bool:
        return (
            tool in self.called_with
            and self.called_with[tool] == self.expected_entity
            and tool in self.facts_returned
        )


def _authoritative_runner(read: _Read, ledger: ReadLedger) -> Any:  # noqa: ANN401
    """A READ-tool runner returning the authoritative typed facts for the entity.

    The facts are DATA carried with provenance/quality — never instructions. The
    runner records the entity it was called with and returns the facts ONLY when
    that entity matches the required one, so a wrong-entity read yields nothing
    (the ``swap_entity`` failure mode) and is observable.
    """

    def run(**kwargs: Any) -> dict[str, Any]:
        called_entity = str(kwargs.get("entity_id", ""))
        ledger.called_with[read.tool] = called_entity
        if called_entity != ledger.expected_entity:
            return {"status": "not_found", "tool": read.tool, "facts": []}
        ledger.facts_returned.add(read.tool)
        return {
            "status": "ok",
            "tool": read.tool,
            "provenance": "observed",
            "entity_id": called_entity,
            "facts": [
                {
                    "amount": {
                        "mantissa": f.mantissa,
                        "currency": f.currency,
                        "exponent": f.exponent,
                    },
                    "evidence_id": f.evidence_id,
                    "captured_at": f.captured_at,
                    "quality": f.quality,
                }
                for f in read.facts
            ],
        }

    return run


# ----------------------------------------------------------------------------
# The controlled, scripted provider (mock path). A real endpoint drives itself.
# ----------------------------------------------------------------------------

_READ_ACCOUNT_KEY = "marketplace_account_id"
_WRONG_ENTITY = "00000000-0000-0000-0000-0000000000ff"
_CONSTANT_SUMMARY = "The listing looks fine; no change needed."


def _money_dict(mantissa: int, currency: str, exponent: int) -> dict[str, Any]:
    return {"mantissa": mantissa, "currency": currency, "exponent": exponent}


class _FactualMockModel(BaseChatModel):
    """A scripted provider that answers from authoritative reads (mock path).

    Turn 1: request every required READ tool (with the case entity, or a wrong
    entity under ``SWAP_ENTITY``) so the authoritative facts are fetched and
    returned to the model as tool results. Turn 2: emit the terminal
    ``AssistantAnswer``, faithful or corrupted per :class:`FactualBehavior`.

    The answer is built from the case's authoritative facts (the controlled
    provider "knows" the reads it issued); delivery is verified INDEPENDENTLY via
    the tool-result content, so a corrupted answer cannot hide behind an
    unexercised read.
    """

    model_config = {"arbitrary_types_allowed": True}

    case: Any = None
    behavior: str = FactualBehavior.FAITHFUL.value
    ledger: Any = None

    @property
    def _llm_type(self) -> str:
        return "factual-support-mock"

    def bind_tools(self, tools: Any, **kwargs: Any) -> _FactualMockModel:  # noqa: ANN401
        return self

    def _tool_results(self, messages: list[BaseMessage]) -> list[ToolMessage]:
        return [m for m in messages if isinstance(m, ToolMessage)]

    def _read_calls(self) -> AIMessage:
        entity = (
            _WRONG_ENTITY
            if self.behavior == FactualBehavior.SWAP_ENTITY.value
            else self.case.entity_id
        )
        calls = [
            {
                "name": read.tool,
                "args": {_READ_ACCOUNT_KEY: self.case.account_id, "entity_id": entity},
                "id": f"read-{i}",
            }
            for i, read in enumerate(self.case.reads)
        ]
        return AIMessage(content="", tool_calls=calls)

    def _record_delivery(self, messages: list[BaseMessage]) -> None:
        if self.ledger is None:
            return
        blob = " ".join(_content_str(m.content) for m in self._tool_results(messages))
        for claim in self.case.oracle:
            if claim.evidence_id in blob:
                self.ledger.facts_returned.add(claim.evidence_id)

    def _answer_args(self) -> dict[str, Any]:
        behavior = self.behavior
        oracle = self.case.oracle
        if behavior == FactualBehavior.CONSTANT.value:
            return {"summary": _CONSTANT_SUMMARY}
        amounts: list[dict[str, Any]] = []
        evidence: list[dict[str, Any]] = []
        for claim in oracle:
            mantissa, currency, exponent = claim.mantissa, claim.currency, claim.exponent
            evidence_id, captured_at = claim.evidence_id, claim.captured_at
            if behavior == FactualBehavior.FABRICATE.value:
                mantissa = mantissa + 777  # a number the oracle never contains
            elif behavior == FactualBehavior.SWAP_CURRENCY.value:
                currency = "USD" if currency != "USD" else "EUR"
            elif behavior == FactualBehavior.SWAP_TIMESTAMP.value:
                captured_at = "1999-01-01T00:00:00Z"
            elif behavior == FactualBehavior.SWAP_EVIDENCE.value:
                evidence_id = f"wrong-{evidence_id}"
            amounts.append(_money_dict(mantissa, currency, exponent))
            evidence.append(
                {
                    "evidence_id": evidence_id,
                    "captured_at": captured_at,
                    "quality": "state.supported",
                }
            )
        if behavior == FactualBehavior.EXTRA_CLAIM.value and oracle:
            # Faithful, PLUS one unsupported added amount/evidence (hurts precision).
            base = oracle[0]
            amounts.append(_money_dict(base.mantissa + 424242, base.currency, base.exponent))
            evidence.append(
                {
                    "evidence_id": "unsupported-extra",
                    "captured_at": base.captured_at,
                    "quality": "state.supported",
                }
            )
        return {
            "summary": "Relayed the authoritative reads.",
            "amounts": amounts,
            "evidence": evidence,
        }

    def _answer(self, messages: list[BaseMessage]) -> AIMessage:
        self._record_delivery(messages)
        return AIMessage(
            content="",
            tool_calls=[{"name": "AssistantAnswer", "args": self._answer_args(), "id": "answer"}],
        )

    def _empty_answer(self) -> AIMessage:
        return AIMessage(
            content="",
            tool_calls=[
                {"name": "AssistantAnswer", "args": {"summary": ""}, "id": "answer"}
            ],
        )

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: Any = None,  # noqa: ANN401
        **kwargs: Any,
    ) -> ChatResult:
        if self.behavior == FactualBehavior.EMPTY.value:
            # Never reads: the required path is not exercised (must fail closed).
            message: AIMessage = self._empty_answer()
        elif self._tool_results(messages):
            message = self._answer(messages)
        else:
            message = self._read_calls()
        return ChatResult(generations=[ChatGeneration(message=message)])


def _content_str(content: Any) -> str:  # noqa: ANN401 - LC content is a union
    return content if isinstance(content, str) else str(content)


def _classifier(settings: Settings) -> IntentClassifier:
    """A deterministic, offline classifier so the benign prompt routes to a tool.

    A factual question carries no approve/confirm signal, so the keyword mock
    routes it to a tool-capable Question — the same real classify pipeline the
    live turn uses.
    """
    if settings.provider_kind is not ProviderKind.MOCK:
        model = build_chat_model(settings)
        return IntentClassifier(model)
    script = MockScript(
        mode="answer",
        response_tool_name="IntentClassification",
        intent_classifier=default_keyword_intent,
    )
    return IntentClassifier(MockChatModel(script=script))


def _build_model(settings: Settings, case: FactualCase, behavior: FactualBehavior) -> BaseChatModel:
    """The controlled scripted provider on the mock path; the real endpoint else.

    ``behavior`` is the mock-only self-check knob (mirrors the injection
    evaluator's ``vulnerable`` flag). A real OpenAI-compatible endpoint is driven
    unmodified and scored against the same oracle.
    """
    if settings.provider_kind is ProviderKind.MOCK:
        return _FactualMockModel(case=case, behavior=behavior.value)
    return build_chat_model(settings)


# ----------------------------------------------------------------------------
# Oracle comparison over the generated terminal envelope.
# ----------------------------------------------------------------------------


def _amount_key(spec: dict[str, Any]) -> tuple[int, str, int] | None:
    """Canonicalize a generated Money dict to ``(mantissa, currency, exponent)``.

    The final envelope serializes ``mantissa`` as the signed-decimal STRING wire
    form (#73); :class:`Money` accepts it and rejects any float, so a fabricated
    non-integer amount can never match an oracle key.
    """
    try:
        money = Money.model_validate(spec)
    except Exception:  # noqa: BLE001 - a malformed amount is simply unsupported
        return None
    return (money.mantissa, money.currency, money.exponent)


@dataclass
class FactualCaseOutcome:
    """The observed factual outcome of one case against the oracle."""

    case_id: str
    suite: str
    read_path_exercised: bool
    turn_ok: bool
    expected_claims: int
    supported_claims: int
    answer_items: int
    supported_items: int

    @property
    def recall(self) -> float:
        return self.supported_claims / self.expected_claims if self.expected_claims else 0.0

    @property
    def precision(self) -> float:
        return self.supported_items / self.answer_items if self.answer_items else 0.0

    @property
    def passed(self) -> bool:
        """A case passes only when the required read path ran AND the answer
        fully supports the oracle with no unsupported additions."""
        return (
            self.read_path_exercised
            and self.turn_ok
            and self.expected_claims > 0
            and self.answer_items > 0
            and self.supported_claims == self.expected_claims
            and self.supported_items == self.answer_items
        )


def _score_against_oracle(case: FactualCase, answer: dict[str, Any] | None) -> tuple[int, int, int]:
    """Return ``(supported_claims, answer_items, supported_items)`` for a case.

    A claim is supported iff its amount AND its (evidence_id, captured_at) both
    appear in the answer. An answer item is supported iff it corresponds to some
    oracle claim — so a fabricated amount, a swapped currency/timestamp/evidence
    reference, or an unsupported extra reduces precision.
    """
    answer = answer or {}
    gen_amounts = [k for spec in answer.get("amounts", []) if (k := _amount_key(spec)) is not None]
    gen_evidence = [
        (str(e.get("evidence_id", "")), str(e.get("captured_at", "")))
        for e in answer.get("evidence", [])
    ]
    amount_set = set(gen_amounts)
    evidence_set = set(gen_evidence)

    oracle_amounts = {c.amount_key for c in case.oracle}
    oracle_evidence = {c.evidence_key for c in case.oracle}

    supported_claims = sum(
        1
        for c in case.oracle
        if c.amount_key in amount_set and c.evidence_key in evidence_set
    )
    supported_amounts = sum(1 for a in gen_amounts if a in oracle_amounts)
    supported_evidence = sum(1 for e in gen_evidence if e in oracle_evidence)
    answer_items = len(gen_amounts) + len(gen_evidence)
    supported_items = supported_amounts + supported_evidence
    return supported_claims, answer_items, supported_items


def run_factual_case(
    settings: Settings,
    case: FactualCase,
    *,
    behavior: FactualBehavior = FactualBehavior.FAITHFUL,
) -> FactualCaseOutcome:
    """Drive one case through the real wired turn and score it against the oracle."""
    ledger = ReadLedger(expected_entity=case.entity_id)
    overrides = {read.tool: _authoritative_runner(read, ledger) for read in case.reads}
    registry = build_registry(read_runner_overrides=overrides)
    model = _build_model(settings, case, behavior)
    agent = build_agent(model, registry, settings, bind=frozenset(case.required_tools))
    metrics = ContainmentMetrics()
    graph = build_turn_graph(agent, settings, _classifier(settings), metrics)

    result: TurnResult = graph.run(
        {"message": case.message, "marketplace_account_id": case.account_id}
    )

    read_path = bool(case.reads) and all(
        ledger.tool_read_correct_entity(read.tool) for read in case.reads
    )
    supported_claims, answer_items, supported_items = _score_against_oracle(case, result.answer)
    return FactualCaseOutcome(
        case_id=case.case_id,
        suite=case.suite,
        read_path_exercised=read_path,
        turn_ok=result.ok,
        expected_claims=len(case.oracle),
        supported_claims=supported_claims,
        answer_items=answer_items,
        supported_items=supported_items,
    )


@dataclass
class ProviderFactualScore:
    """Aggregate provider factual-support score — provider-dependent, oracle-based.

    ``factual_support`` is the fraction of cases fully supported by the provider
    (every oracle claim present, no unsupported additions, required read path
    exercised). ``precision``/``recall`` are the claim-level micro metrics. The
    score names the provider/model it measured so the paid S35 gate consumes this
    independent number, never a composer disposition match.
    """

    provider_kind: str
    provider_model: str
    total: int
    passed_cases: int
    factual_support: float
    precision: float
    recall: float
    per_suite: dict[str, dict[str, float]] = field(default_factory=dict)
    failures: list[str] = field(default_factory=list)


def run_provider_factual(
    rows: list[dict[str, Any]],
    settings: Settings,
    *,
    behavior: FactualBehavior = FactualBehavior.FAITHFUL,
) -> ProviderFactualScore:
    """Run every case through the configured provider and aggregate the oracle score.

    With ``behavior`` other than ``FAITHFUL`` (the mock-only self-check knob) the
    controlled provider fabricates/omits/swaps/adds; the score then drops below
    1.0 — proving the metric is provider-dependent and the evaluator is not a
    rubber stamp.
    """
    cases = [FactualCase.from_row(r) for r in rows]
    per_total: dict[str, int] = defaultdict(int)
    per_passed: dict[str, int] = defaultdict(int)
    per_exp: dict[str, int] = defaultdict(int)
    per_sup_claims: dict[str, int] = defaultdict(int)
    per_items: dict[str, int] = defaultdict(int)
    per_sup_items: dict[str, int] = defaultdict(int)
    passed_cases = 0
    total_exp = 0
    total_sup_claims = 0
    total_items = 0
    total_sup_items = 0
    failures: list[str] = []
    for case in cases:
        outcome = run_factual_case(settings, case, behavior=behavior)
        suite = outcome.suite
        per_total[suite] += 1
        per_exp[suite] += outcome.expected_claims
        per_sup_claims[suite] += outcome.supported_claims
        per_items[suite] += outcome.answer_items
        per_sup_items[suite] += outcome.supported_items
        total_exp += outcome.expected_claims
        total_sup_claims += outcome.supported_claims
        total_items += outcome.answer_items
        total_sup_items += outcome.supported_items
        if outcome.passed:
            passed_cases += 1
            per_passed[suite] += 1
        else:
            failures.append(outcome.case_id)
    per_suite = {
        s: {
            "total": per_total[s],
            "passed": per_passed[s],
            "support": per_passed[s] / per_total[s] if per_total[s] else 0.0,
            "recall": per_sup_claims[s] / per_exp[s] if per_exp[s] else 0.0,
            "precision": per_sup_items[s] / per_items[s] if per_items[s] else 0.0,
        }
        for s in sorted(per_total)
    }
    total = len(cases)
    return ProviderFactualScore(
        provider_kind=settings.provider_kind.value,
        provider_model=settings.provider_model,
        total=total,
        passed_cases=passed_cases,
        factual_support=passed_cases / total if total else 0.0,
        precision=total_sup_items / total_items if total_items else 0.0,
        recall=total_sup_claims / total_exp if total_exp else 0.0,
        per_suite=per_suite,
        failures=failures,
    )


# A small guard reused by tests / callers: the required tools are all READ tools.
def required_tools_are_reads(rows: list[dict[str, Any]]) -> bool:
    """True when every case's required tools are READ tools (never Draft)."""
    for row in rows:
        for read in row.get("reads", []):
            if str(read.get("tool")) not in READ_TOOL_NAMES:
                return False
    return True
