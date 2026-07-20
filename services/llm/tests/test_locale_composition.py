"""The bound-locale composition seam (issue #120, LOC-001/LOC-004, §11, §12.2).

The gateway binds and validates the active locale on every chat turn (landed in
``b6f3d5d``) and hands the LLM plane the server-authoritative bound locale as
read-only pass-through data. THIS is the consumption seam that commit escalated:
the plane must COMPOSE its response/failure envelope with the bound locale so a
Persian (``fa-IR``) turn selects the Persian response/failure catalog and an
English (``en``) turn the English one.

Locale is DATA (LOC-001): the composer selects the catalog by the passed tag and
never branches business logic on it. The closed set is validated and fails closed
on an unknown tag (never inferred from message text / digit shape / region /
account); a MISSING tag maps only via the ONE explicit authoritative fallback
policy (LOC-004: English).
"""

from __future__ import annotations

import json
from typing import Any

import pytest
from fastapi.testclient import TestClient
from llm.app import ChatRequest, create_app
from llm.config import ProviderKind, Settings, load_settings
from llm.envelope.composer import compose, compose_or_refuse, fail_closed
from llm.envelope.contract import UNSCOPED, CannotAnswer, Claim, ResponseEnvelope
from llm.envelope.models import EvidenceRef
from llm.localization import (
    FALLBACK_LOCALE_TAG,
    SUPPORTED_LOCALE_TAGS,
    UnknownLocaleError,
    resolve_locale,
)
from pydantic import SecretStr, ValidationError

_GATEWAY_TOKEN = "test-gateway-token"
AUTH_HEADERS = {"Authorization": f"Bearer {_GATEWAY_TOKEN}"}

GOOD_EVIDENCE = EvidenceRef(
    evidence_id="ev-1", captured_at="2026-07-17T10:00:00Z", quality="state.verified"
)


def _grounded_claim() -> Claim:
    return Claim(statement="lowest qualifying offer observed", evidence=[GOOD_EVIDENCE])


def mock_settings(**overrides: Any) -> Settings:
    base: dict[str, Any] = {
        "provider_kind": ProviderKind.MOCK,
        "gateway_token": SecretStr(_GATEWAY_TOKEN),
    }
    base.update(overrides)
    return Settings(**base)


# --- the closed set + fail-closed resolution (LOC-001) ----------------------


def test_supported_set_is_exactly_the_two_p0_locales() -> None:
    assert SUPPORTED_LOCALE_TAGS == frozenset({"en", "fa-IR"})
    assert FALLBACK_LOCALE_TAG == "en"  # LOC-004 authoritative fallback


@pytest.mark.parametrize("tag", ["en", "fa-IR"])
def test_resolve_locale_accepts_supported(tag: str) -> None:
    assert resolve_locale(tag) == tag


@pytest.mark.parametrize("tag", ["de-DE", "fa", "EN", "en-US", "", "fa_IR", "farsi"])
def test_resolve_locale_fails_closed_on_unknown(tag: str) -> None:
    # Never inferred, never coerced — an unsupported tag is rejected outright.
    with pytest.raises(UnknownLocaleError):
        resolve_locale(tag)


def test_resolve_locale_missing_maps_via_explicit_fallback_only() -> None:
    # A MISSING (None) tag is the ONLY case mapped, and only to the explicit
    # authoritative fallback policy (LOC-004) — never a guessed locale.
    assert resolve_locale(None) == FALLBACK_LOCALE_TAG


def test_resolve_locale_missing_honours_configured_fallback() -> None:
    assert resolve_locale(None, fallback="fa-IR") == "fa-IR"


def test_resolve_locale_rejects_an_invalid_configured_fallback() -> None:
    # A misconfigured fallback fails closed rather than silently passing through.
    with pytest.raises(UnknownLocaleError):
        resolve_locale(None, fallback="de-DE")


# --- config carries the authoritative fallback policy -----------------------


def test_settings_default_fallback_locale_is_english() -> None:
    assert load_settings().fallback_locale == "en"


def test_settings_rejects_an_unsupported_fallback_locale() -> None:
    with pytest.raises(ValidationError):
        Settings(fallback_locale="de-DE")


# --- compose tags the envelope with the bound (Persian/English) catalog -----


@pytest.mark.parametrize("tag", ["en", "fa-IR"])
def test_compose_tags_envelope_with_bound_locale(tag: str) -> None:
    env = compose(catalog=UNSCOPED, model_inference="a note", locale=tag)
    assert isinstance(env, ResponseEnvelope)
    assert env.locale == tag  # the response catalog the plane composed under


def test_compose_defaults_to_the_english_fallback_catalog() -> None:
    # Existing callers that pass no locale get the explicit fallback, never a guess.
    env = compose(catalog=UNSCOPED, model_inference="a note")
    assert env.locale == "en"


def test_compose_strict_raises_on_unknown_locale() -> None:
    with pytest.raises(UnknownLocaleError):
        compose(catalog=UNSCOPED, model_inference="a note", locale="de-DE")


# --- fail-closed refusal is composed in the bound failure catalog -----------


@pytest.mark.parametrize("tag", ["en", "fa-IR"])
def test_refusal_carries_the_bound_locale(tag: str) -> None:
    result = compose_or_refuse(
        catalog=UNSCOPED,
        observed_facts=[Claim(statement="unsupported claim", evidence=[])],
        locale=tag,
    )
    assert isinstance(result, CannotAnswer)
    assert result.locale == tag  # Persian failure catalog for fa-IR, English for en
    assert result.reason_key == "state.degraded.body"  # locale-neutral key
    # A refusal still carries no digits in any user-visible field (§4.6 containment).
    assert not any(ch.isdigit() for ch in result.message)


def test_fail_closed_helper_tags_locale() -> None:
    assert fail_closed(message="cannot answer", locale="fa-IR").locale == "fa-IR"


# --- unknown locale on the compose path fails closed, never raises ----------


def test_compose_or_refuse_unknown_locale_fails_closed() -> None:
    result = compose_or_refuse(
        catalog=UNSCOPED,
        model_inference="a note",
        observed_facts=[_grounded_claim()],
        locale="de-DE",
    )
    assert isinstance(result, CannotAnswer)
    assert "LOCALE_UNSUPPORTED" in result.violations
    # The refusal itself is tagged with the explicit fallback catalog (LOC-004),
    # never the rejected tag.
    assert result.locale == FALLBACK_LOCALE_TAG


# --- the envelope/refusal models reject a non-closed-set locale directly -----


def test_envelope_model_rejects_unsupported_locale_value() -> None:
    with pytest.raises(ValidationError):
        ResponseEnvelope(locale="de-DE")


def test_cannot_answer_model_rejects_unsupported_locale_value() -> None:
    deep_link = fail_closed(message="x").deep_link
    with pytest.raises(ValidationError):
        CannotAnswer(
            reason_key="state.degraded.body",
            message="x",
            deep_link=deep_link,
            locale="de-DE",
        )


# --- continuation preserves / explicitly transitions the bound locale --------


def test_continuation_preserves_the_bound_locale() -> None:
    first = compose(catalog=UNSCOPED, model_inference="the first turn", locale="fa-IR")
    second = compose(catalog=UNSCOPED, model_inference="the next turn", locale="fa-IR")
    assert first.locale == second.locale == "fa-IR"  # same binding, idempotent


def test_locale_transition_is_honoured_never_silently_relabelled() -> None:
    # The plane never overrides the server-authoritative bound locale: an explicit
    # switch on a later turn is composed under the NEW catalog, not the old one.
    fa = compose(catalog=UNSCOPED, model_inference="the first turn", locale="fa-IR")
    en = compose(catalog=UNSCOPED, model_inference="the next turn", locale="en")
    assert fa.locale == "fa-IR"
    assert en.locale == "en"


# --- request boundary: the plane reads + fails closed on the wire locale -----


def test_chat_request_reads_the_wire_locale() -> None:
    req = ChatRequest(message="hi", locale="fa-IR")
    assert req.locale == "fa-IR"


def test_chat_request_rejects_an_unknown_wire_locale() -> None:
    with pytest.raises(ValidationError):
        ChatRequest(message="hi", locale="de-DE")


def _frames(text: str) -> list[dict[str, Any]]:
    return [
        json.loads(block[len("data:") :].strip())
        for block in text.strip().split("\n\n")
        if block.strip().startswith("data:")
    ]


def test_chat_stream_echoes_the_bound_locale_on_the_conversation_frame() -> None:
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json={"message": "what changed today?", "locale": "fa-IR"},
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)
    conversation = next(f for f in frames if f["kind"] == "conversation")
    assert conversation["locale_tag"] == "fa-IR"


def test_chat_stream_defaults_missing_locale_to_english_fallback() -> None:
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.post("/chat", json={"message": "hello"}, headers=AUTH_HEADERS)
        frames = _frames(resp.text)
    conversation = next(f for f in frames if f["kind"] == "conversation")
    assert conversation["locale_tag"] == "en"


# --- the eval harness composes fixtures under their bound locale -------------


@pytest.mark.parametrize("tag", ["en", "fa-IR"])
def test_eval_harness_composes_fixture_under_its_bound_locale(tag: str) -> None:
    from llm.evals.scenario import compose_fixture

    case: dict[str, Any] = {
        "model_inference": "a short grounded note",
        "observed_facts": [
            {
                "statement": "lowest qualifying offer observed",
                "evidence": [
                    {
                        "evidence_id": "ev-1",
                        "captured_at": "2026-07-17T10:00:00Z",
                        "quality": "state.verified",
                    }
                ],
            }
        ],
        "locale": tag,
    }
    result = compose_fixture(case)
    assert isinstance(result, ResponseEnvelope)
    assert result.locale == tag


def test_eval_harness_fixture_with_unknown_locale_fails_closed() -> None:
    from llm.evals.scenario import compose_fixture

    result = compose_fixture({"model_inference": "note", "locale": "de-DE"})
    assert isinstance(result, CannotAnswer)
    assert "LOCALE_UNSUPPORTED" in result.violations


def test_chat_rejects_an_unknown_wire_locale_fail_closed() -> None:
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json={"message": "hello", "locale": "de-DE"},
            headers=AUTH_HEADERS,
        )
    # Fail closed: an unsupported locale is a 422 (never inferred to a default).
    assert resp.status_code == 422
