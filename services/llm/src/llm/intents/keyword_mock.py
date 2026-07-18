"""A deterministic, content-sensitive intent classifier for the mock provider.

This is a DEV/CI stand-in for the small model — used ONLY with the deterministic
mock provider, never in production (the OpenAI-compatible endpoint classifies for
real, and §12.5's eval harness measures that real accuracy at the gate). Its one
job is to let CI drive the ACTUAL message text through the real classify pipeline
(normalize → classify → route) so a containment test proves the phrasing — not a
fixture label — is what routes to guidance-only.

It is intentionally a general lexicon over approval/confirmation language in
Persian, English, and mixed shorthand, not a per-message lookup: a message with
no approve/confirm signal falls through to Question. If an adversarial phrasing
carries no recognizable approval signal, it classifies as Question and the
containment test's structural layer still holds — but the lexicon is broad on
purpose so coercive phrasings are recognized AND contained.
"""

from __future__ import annotations

from llm.intents.models import IntentClass
from llm.intents.normalize import normalize_input

# Approval-intent lexicon (route → ApproveAction). Latin tokens matched on folded,
# lowercased, space-joined text; Persian tokens matched as substrings.
_APPROVE_TOKENS: tuple[str, ...] = (
    # Latin stems (substring match covers approve/approval/approved/…).
    "approv", "apply", "applied", "execut", "push it", "push live", "ship it",
    "ship ", "go ahead", "do it", "just do", "green light", "green-light", "yes",
    "yep", "yeah", "okay", "ok ", "overrid", "bypass", "force", "authoriz",
    "consent", "sign off", "signed off", "go live",
    # Persian / mixed
    "تأیید", "تایید", "اعمال", "اجرا", "موافق", "قبول", "انجامش", "انجام بده",
    "برو جلو", "بزن بریم", "دور بزن", "اجازه", "بله", "همین الان اعمال",
)

# Confirmation-intent lexicon (route → ConfirmResult).
_CONFIRM_TOKENS: tuple[str, ...] = (
    "confirm", "finaliz", "reconcil", "accepted", "acknowledge", "mark it accepted",
    "mark reconciled", "close out",
    # Persian / mixed
    "قطعی", "ثبتش", "ثبت کن", "نتیجه را", "نتیجه رو", "تأیید نهایی", "مختومه", "بپذیر",
)


def default_keyword_intent(text: str) -> str:
    """Classify ``text`` into an intent-class value by approval/confirm lexicon.

    Returns the ``IntentClass`` value string (the mock emits it as the model's
    structured choice). Confirm is checked before approve only when a message is
    purely about finalizing a result; otherwise approval language dominates.
    """
    folded = normalize_input(text).text.lower()

    has_approve = any(tok in folded for tok in _APPROVE_TOKENS)
    has_confirm = any(tok in folded for tok in _CONFIRM_TOKENS)

    if has_confirm and not has_approve:
        return IntentClass.CONFIRM_RESULT.value
    if has_approve:
        return IntentClass.APPROVE_ACTION.value
    if has_confirm:
        return IntentClass.CONFIRM_RESULT.value
    # No approve/confirm signal ⇒ a benign, tool-capable question.
    return IntentClass.QUESTION.value
