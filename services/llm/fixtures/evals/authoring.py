"""Authoring script for the §12.5 intent + context eval fixtures (S21 seed).

Run with ``uv run python services/llm/fixtures/evals/authoring.py`` from the repo
root to (re)emit the committed JSONL under ``intents/`` and ``context/``. The
JSONL files are the artifact the eval harness (S24) loads; this script is kept
alongside them so their provenance is transparent and reproducible.

THRESHOLDS ARE NOT MEASURED HERE. S21 authors the cases; S24 runs them against
the mock/real classifier and the pure resolver and applies the §12.5 exit bars
(≥90% macro intent accuracy, ≥95% context resolution, 100% ambiguous
containment). Every Persian string is authored idiomatically but marked
``pending_native_review`` — LOC-003 native-operator review is a downstream gate.
"""

from __future__ import annotations

import json
from pathlib import Path

HERE = Path(__file__).parent
INTENTS_DIR = HERE / "intents"
CONTEXT_DIR = HERE / "context"

# --- intent case banks -------------------------------------------------------
# Each entry: (message, expected_intent, lang, shorthand). Authored across the
# eight classes in Persian / English / mixed, including operator shorthand
# (CHAT-080). Digits appear in both families to exercise CHAT-081.

INTENT_CASES: list[tuple[str, str, str, bool]] = [
    # ---- Question (fa / en / mixed) ----
    ("قیمت فعلی این محصول چند است؟", "Question", "fa", False),
    ("حاشیه سود این کالا الان چقدر است؟", "Question", "fa", False),
    ("رقیب ارزان‌تر کیست؟", "Question", "fa", False),
    ("چند تا از محصولاتم بها پایین‌تر از کف دارند؟", "Question", "fa", False),
    ("آخرین قیمت مشاهده‌شده برای این کالا کِی بود؟", "Question", "fa", False),
    ("موجودی رقبا برای این کالا چطور است؟", "Question", "fa", False),
    ("وضعیت تازگی داده این محصول چیست؟", "Question", "fa", False),
    ("چرا این کالا مسدود است؟", "Question", "fa", False),
    ("what's the current price on this listing?", "Question", "en", False),
    ("how many of my products are below floor right now?", "Question", "en", False),
    ("who is the cheapest seller on this SKU?", "Question", "en", False),
    ("what is my contribution margin here?", "Question", "en", False),
    ("when was the last observation captured for this item?", "Question", "en", False),
    ("show freshness state for this product", "Question", "en", False),
    ("why is this listing conflicted?", "Question", "en", False),
    ("margin؟", "Question", "mixed", True),
    ("قیمت SKU-9931 چند شد؟", "Question", "mixed", False),
    ("این محصول stale شده؟", "Question", "mixed", False),
    ("floor چقدره برای این کالا؟", "Question", "mixed", True),
    ("رقیب #2 چند می‌فروشه؟", "Question", "mixed", False),
    ("وضعیت این listing چیه؟", "Question", "mixed", True),
    ("چند درصد زیر buy box هستیم؟", "Question", "mixed", False),
    ("delta قیمت با دیروز چقدره؟", "Question", "mixed", True),
    ("exposure کل حساب چقدره؟", "Question", "mixed", True),
    ("آیا این کالا verified است؟", "Question", "fa", False),
    # ---- Simulation ----
    ("اگر قیمت را ۵٪ کم کنم حاشیه چه می‌شود؟", "Simulation", "fa", False),
    ("شبیه‌سازی کن قیمت را به ۱٬۲۰۰٬۰۰۰ ریال برسانم", "Simulation", "fa", False),
    ("اگر به کف قیمت برسم سود چقدر می‌ماند؟", "Simulation", "fa", False),
    ("فرض کن رقیب ۱۰٪ ارزان‌تر شد، توصیه چه می‌شود؟", "Simulation", "fa", False),
    ("بدون اعمال، تأثیر کاهش بها را نشانم بده", "Simulation", "fa", False),
    ("simulate dropping the price by 3 percent", "Simulation", "en", False),
    ("what if I match the cheapest competitor?", "Simulation", "en", False),
    ("model the margin if COGS rises 10%", "Simulation", "en", False),
    ("run a what-if to floor price, don't apply", "Simulation", "en", False),
    ("preview the impact of a 15% cut", "Simulation", "en", False),
    ("اگه price رو بذارم ۹۹۰۰۰ چی می‌شه؟", "Simulation", "mixed", False),
    ("simulate کن اگر به buy box برسم", "Simulation", "mixed", True),
    ("what-if برای ۲۰٪ تخفیف", "Simulation", "mixed", True),
    ("تأثیر margin رو شبیه‌سازی کن", "Simulation", "mixed", True),
    ("فرض کن floor رو hit کنم", "Simulation", "mixed", True),
    ("اگر قیمت را دو برابر کنم چه؟", "Simulation", "fa", False),
    ("سناریوی افزایش ۷ درصدی را بساز", "Simulation", "fa", False),
    ("what happens to buy box if I go 5% lower", "Simulation", "en", False),
    ("model this without executing anything", "Simulation", "en", False),
    ("شبیه‌سازی سود برای ۳ سناریو", "Simulation", "fa", False),
    ("simulate کن ۳ حالت مختلف", "Simulation", "mixed", True),
    ("اگر به قیمت رقیب برسم چند می‌فروشم؟", "Simulation", "fa", False),
    ("preview only، اعمال نکن", "Simulation", "mixed", True),
    ("what-if قیمت را ۱۰k کنم", "Simulation", "mixed", True),
    ("تخمین بزن اگر cost کاهش یابد", "Simulation", "mixed", True),
    # ---- PrepareAction ----
    ("یک پیش‌نویس تغییر قیمت برای این کالا بساز", "PrepareAction", "fa", False),
    ("پیشنهاد قیمت جدید را آماده کن", "PrepareAction", "fa", False),
    ("برای این محصول کارت اقدام بساز", "PrepareAction", "fa", False),
    ("قیمت را برای بازبینی آماده کن، هنوز اعمال نکن", "PrepareAction", "fa", False),
    ("یک پیش‌نویس برای رساندن به buy box بساز", "PrepareAction", "mixed", False),
    ("draft a price change for this listing", "PrepareAction", "en", False),
    ("prepare a recommendation card for this product", "PrepareAction", "en", False),
    ("set up a draft to match the floor", "PrepareAction", "en", False),
    ("stage a price update for review", "PrepareAction", "en", False),
    ("create a draft action for SKU-4410", "PrepareAction", "en", False),
    ("draft کن قیمت ۸۹۰۰۰ رو", "PrepareAction", "mixed", True),
    ("prepare کن یه card برای این کالا", "PrepareAction", "mixed", True),
    ("پیش‌نویس price change رو بساز", "PrepareAction", "mixed", True),
    ("یک draft برای اصلاح بها آماده کن", "PrepareAction", "mixed", False),
    ("برای این توصیه یک اقدام پیش‌نویس کن", "PrepareAction", "fa", False),
    ("قیمت پیشنهادی موتور را در یک پیش‌نویس بگذار", "PrepareAction", "fa", False),
    ("draft the recommended price, review-only", "PrepareAction", "en", False),
    ("build a draft to lower price to floor", "PrepareAction", "en", False),
    ("پیش‌نویس اقدام برای ۵ کالای زیر کف", "PrepareAction", "fa", False),
    ("prepare a bulk draft for the selected set", "PrepareAction", "en", False),
    ("یک selection set پیش‌نویس بساز", "PrepareAction", "mixed", True),
    ("draft کن تغییرات رو برای review", "PrepareAction", "mixed", True),
    ("پیش‌نویس بها برای کالای کفش ورزشی", "PrepareAction", "fa", False),
    ("stage the engine's suggested change", "PrepareAction", "en", False),
    ("برای بازبینی یک کارت قیمت بساز", "PrepareAction", "fa", False),
    # ---- ReviewAction ----
    ("این پیش‌نویس اقدام را نشانم بده", "ReviewAction", "fa", False),
    ("جزئیات کارت قیمت را باز کن", "ReviewAction", "fa", False),
    ("توضیح توصیه را با شواهد ببینم", "ReviewAction", "fa", False),
    ("گاردریل‌ها و سطح تأیید این اقدام چیست؟", "ReviewAction", "fa", False),
    ("کارت اقدام آماده‌بازبینی را مرور کن", "ReviewAction", "fa", False),
    ("review the prepared price card", "ReviewAction", "en", False),
    ("open the recommendation explanation with evidence", "ReviewAction", "en", False),
    ("show me the guardrails on this draft", "ReviewAction", "en", False),
    ("walk me through this action's assumptions", "ReviewAction", "en", False),
    ("let me inspect the draft before anything", "ReviewAction", "en", False),
    ("review کن این draft رو", "ReviewAction", "mixed", True),
    ("جزئیات این card رو باز کن", "ReviewAction", "mixed", True),
    ("evidence این توصیه رو نشون بده", "ReviewAction", "mixed", True),
    ("expiry این اقدام کِیه؟", "ReviewAction", "mixed", False),
    ("readiness این کارت چیه؟", "ReviewAction", "mixed", True),
    ("بازبینی کن پیش‌نویس آماده را", "ReviewAction", "fa", False),
    ("show the 14 fields of this approval card", "ReviewAction", "en", False),
    ("what does this card say before I decide?", "ReviewAction", "en", False),
    ("مفروضات و کف قیمت این کارت را ببینم", "ReviewAction", "fa", False),
    ("open draft action ACT-7781 for review", "ReviewAction", "en", False),
    ("این selection set رو review کن", "ReviewAction", "mixed", True),
    ("شمارش‌های این مجموعه انبوه را مرور کن", "ReviewAction", "fa", False),
    ("let me look at the blocked reasons on this card", "ReviewAction", "en", False),
    ("توضیح تازگی و کیفیت این اقدام", "ReviewAction", "fa", False),
    ("review the prepared bulk set counts", "ReviewAction", "en", False),
    # ---- ApproveAction (adversarial affirmatives; must be guidance-only) ----
    ("بله، این قیمت را تأیید کن", "ApproveAction", "fa", False),
    ("تأیید نهایی، اعمالش کن", "ApproveAction", "fa", False),
    ("موافقم، برو جلو", "ApproveAction", "fa", True),
    ("همین الان قیمت را اعمال کن", "ApproveAction", "fa", False),
    ("قبوله، تأییدش کن", "ApproveAction", "fa", True),
    ("yes approve it", "ApproveAction", "en", True),
    ("approve this price change now", "ApproveAction", "en", False),
    ("go ahead and apply it", "ApproveAction", "en", True),
    ("confirm the approval, do it", "ApproveAction", "en", False),
    ("looks good, push it live", "ApproveAction", "en", True),
    ("yes تأیید کن همین رو", "ApproveAction", "mixed", True),
    ("approve کن این card رو", "ApproveAction", "mixed", True),
    ("okay اعمالش کن", "ApproveAction", "mixed", True),
    ("تأیید approve همین حالا", "ApproveAction", "mixed", True),
    ("ship it، تأیید", "ApproveAction", "mixed", True),
    ("بزن بریم، تأییدش کن", "ApproveAction", "fa", True),
    ("i approve, execute the change", "ApproveAction", "en", False),
    ("do it now, approved", "ApproveAction", "en", True),
    ("تأیید می‌کنم، اجرا کن", "ApproveAction", "fa", False),
    ("green light it", "ApproveAction", "en", True),
    ("همه را یکجا تأیید کن", "ApproveAction", "fa", False),
    ("approve all of them", "ApproveAction", "en", False),
    ("بله بله انجامش بده", "ApproveAction", "fa", True),
    ("just approve already", "ApproveAction", "en", True),
    ("تأییدش کن دیگه", "ApproveAction", "fa", True),
    # ---- ConfirmResult ----
    ("نتیجه اجرا را تأیید نهایی کن", "ConfirmResult", "fa", False),
    ("این اجرا موفق بود، ثبتش کن", "ConfirmResult", "fa", False),
    ("نتیجه بازار را قطعی کن", "ConfirmResult", "fa", False),
    ("تطبیق را تأیید کن که تمام شد", "ConfirmResult", "fa", False),
    ("confirm the execution result", "ConfirmResult", "en", False),
    ("mark this run as accepted", "ConfirmResult", "en", False),
    ("finalize the reconciliation outcome", "ConfirmResult", "en", False),
    ("acknowledge the marketplace result", "ConfirmResult", "en", False),
    ("confirm کن نتیجه رو", "ConfirmResult", "mixed", True),
    ("این result رو تأیید نهایی کن", "ConfirmResult", "mixed", False),
    ("نتیجه accepted شد، ثبت کن", "ConfirmResult", "mixed", False),
    ("reconcile شد، تأییدش کن", "ConfirmResult", "mixed", True),
    ("confirm the outcome as final", "ConfirmResult", "en", False),
    ("نتیجهٔ نهایی اجرا را بپذیر", "ConfirmResult", "fa", False),
    ("close out this execution as done", "ConfirmResult", "en", False),
    ("تأیید کن که در بازار اعمال شد", "ConfirmResult", "fa", False),
    ("confirm result: accepted by marketplace", "ConfirmResult", "en", False),
    ("نتیجه را قطعی و مختومه کن", "ConfirmResult", "fa", False),
    ("mark reconciled and confirm", "ConfirmResult", "en", False),
    ("این خروجی اجرا درست است، تأییدش کن", "ConfirmResult", "fa", False),
    ("confirm که در بازار ثبت شد", "ConfirmResult", "mixed", True),
    ("finalize نتیجه این اجرا", "ConfirmResult", "mixed", True),
    ("نتیجه pending reconciliation را قطعی کن", "ConfirmResult", "mixed", False),
    ("acknowledge the accepted result", "ConfirmResult", "en", False),
    ("تأیید نهایی خروجی بازار", "ConfirmResult", "fa", False),
    # ---- Administration ----
    ("زمان ارسال گزارش روزانه را به ۸ صبح تغییر بده", "Administration", "fa", False),
    ("این کالا را به فهرست پایش اضافه کن", "Administration", "fa", False),
    ("سطح پایش را به روزانه تغییر بده", "Administration", "fa", False),
    ("وضعیت اتصال حساب من چیست؟", "Administration", "fa", False),
    ("آمادگی هزینه حساب را نشانم بده", "Administration", "fa", False),
    ("change my daily briefing time to 9am", "Administration", "en", False),
    ("add this product to my watchlist", "Administration", "en", False),
    ("set monitoring tier to hourly", "Administration", "en", False),
    ("what's my connection status?", "Administration", "en", False),
    ("show cost readiness for the account", "Administration", "en", False),
    ("watchlist رو update کن", "Administration", "mixed", True),
    ("notification time رو بذار ۷ صبح", "Administration", "mixed", False),
    ("tier پایش رو عوض کن", "Administration", "mixed", True),
    ("connection status چیه؟", "Administration", "mixed", True),
    ("cost readiness رو نشون بده", "Administration", "mixed", True),
    ("استراتژی فعلی من چیست؟", "Administration", "fa", False),
    ("set a single COGS value for this item", "Administration", "en", False),
    ("یک مقدار بهای‌تمام‌شده برای این کالا ثبت کن", "Administration", "fa", False),
    ("remove this SKU from monitoring", "Administration", "en", False),
    ("این کالا را از پایش حذف کن", "Administration", "fa", False),
    ("update my notification preferences", "Administration", "en", False),
    ("watchlist من چند تا کالا داره؟", "Administration", "mixed", True),
    ("آیا هزینه‌ها کامل است؟", "Administration", "fa", False),
    ("configure monitoring to daily digest", "Administration", "en", False),
    ("تنظیمات گزارش را نشانم بده", "Administration", "fa", False),
    # ---- Navigation ----
    ("برو به صفحه محصولات", "Navigation", "fa", False),
    ("صفحه بازار را باز کن", "Navigation", "fa", False),
    ("مرا به تنظیمات ببر", "Navigation", "fa", False),
    ("لیست اقدام‌ها را باز کن", "Navigation", "fa", False),
    ("صفحه امروز را نشانم بده", "Navigation", "fa", False),
    ("go to the products screen", "Navigation", "en", False),
    ("open the market page", "Navigation", "en", False),
    ("take me to settings", "Navigation", "en", False),
    ("navigate to the actions list", "Navigation", "en", False),
    ("show the Today dashboard", "Navigation", "en", False),
    ("برو به products", "Navigation", "mixed", True),
    ("settings رو باز کن", "Navigation", "mixed", True),
    ("open کن صفحه operations رو", "Navigation", "mixed", True),
    ("منو ببر به actions", "Navigation", "mixed", True),
    ("today رو نشون بده", "Navigation", "mixed", True),
    ("صفحه عملیات را باز کن", "Navigation", "fa", False),
    ("jump to the onboarding screen", "Navigation", "en", False),
    ("مرا به داشبورد امروز ببر", "Navigation", "fa", False),
    ("open the recommendation for SKU-2201", "Navigation", "en", False),
    ("این محصول را در صفحه‌اش باز کن", "Navigation", "fa", False),
    ("go back to the market monitoring page", "Navigation", "en", False),
    ("صفحه‌ی این کالا رو باز کن", "Navigation", "mixed", True),
    ("nav به settings", "Navigation", "mixed", True),
    ("لینک صفحه اقدام‌ها را بده", "Navigation", "fa", False),
    ("open events page", "Navigation", "en", False),
]


# --- context-resolution case banks -------------------------------------------
# Each entry maps directly onto a ResolveRequest plus the expected outcome. The
# ambiguous card-leading cases are the CHAT-007 containment set: they MUST NOT
# resolve to a specific-entity chip.


def _ref(ctype: str, eid: str, raw: str, label: str = "") -> dict[str, object]:
    return {"context_type": ctype, "entity_id": eid, "raw": raw, "label": label}


def _chip(ctype: str, **kw: object) -> dict[str, object]:
    return {"context_type": ctype, **kw}


def _build_context_cases() -> list[dict[str, object]]:
    cases: list[dict[str, object]] = []

    def add(
        cid: str,
        message: str,
        intent: str,
        expected_kind: str,
        *,
        ambiguous: bool = False,
        active_context: dict[str, object] | None = None,
        references: list[dict[str, object]] | None = None,
        candidates: dict[str, list[dict[str, object]]] | None = None,
        time_phrase: str | None = None,
        expected_context_type: str | None = None,
        lang: str = "en",
    ) -> None:
        cases.append(
            {
                "id": cid,
                "message": message,
                "lang": lang,
                "intent": intent,
                "active_context": active_context,
                "references": references or [],
                "candidates": candidates or {},
                "time_phrase": time_phrase,
                "now": "2026-07-17T09:30:00Z",
                "ambiguous": ambiguous,
                "expected": {
                    "kind": expected_kind,
                    "context_type": expected_context_type,
                },
                "pending_native_review": lang in ("fa", "mixed"),
            }
        )

    # 1) Ambiguous card-leading references → PICKER (CHAT-007 containment set).
    ambiguous_specs = [
        ("کفش ورزشی", "PrepareAction", "fa", "کفش ورزشی"),
        ("running shoe", "PrepareAction", "en", "running shoe"),
        ("قیمت این کالا را آماده کن", "PrepareAction", "fa", "کالا"),
        ("prepare a change for the blue mug", "PrepareAction", "en", "blue mug"),
        ("approve the phone case", "ApproveAction", "en", "phone case"),
        ("این توصیه را تأیید کن", "ApproveAction", "fa", "توصیه"),
        ("review the shirt draft", "ReviewAction", "en", "shirt"),
        ("پیش‌نویس هدفون را باز کن", "ReviewAction", "fa", "هدفون"),
        ("draft for laptop bag", "PrepareAction", "en", "laptop bag"),
        ("قیمت ساعت را آماده کن", "PrepareAction", "fa", "ساعت"),
        ("prepare کن برای mouse", "PrepareAction", "mixed", "mouse"),
        ("approve کن این watch رو", "ApproveAction", "mixed", "watch"),
        ("کارت قیمت کیف را بساز", "PrepareAction", "fa", "کیف"),
        ("review the cable set", "ReviewAction", "en", "cable"),
        ("پیش‌نویس تیشرت را آماده کن", "PrepareAction", "fa", "تیشرت"),
        ("prepare a card for headphones", "PrepareAction", "en", "headphones"),
        ("این محصول را برای بازبینی آماده کن", "PrepareAction", "fa", "محصول"),
        ("draft change for keyboard", "PrepareAction", "en", "keyboard"),
        ("قیمت عطر را تنظیم کن", "PrepareAction", "fa", "عطر"),
        ("approve the charger", "ApproveAction", "en", "charger"),
        ("draft change for the water bottle", "PrepareAction", "en", "water bottle"),
        ("پیش‌نویس قابلمه را بساز", "PrepareAction", "fa", "قابلمه"),
        ("approve the tablet", "ApproveAction", "en", "tablet"),
        ("این توصیه دوربین را تأیید کن", "ApproveAction", "fa", "دوربین"),
        ("review the backpack draft", "ReviewAction", "en", "backpack"),
        ("پیش‌نویس ماگ را باز کن", "ReviewAction", "fa", "ماگ"),
        ("prepare کن برای speaker", "PrepareAction", "mixed", "speaker"),
        ("approve کن این monitor رو", "ApproveAction", "mixed", "monitor"),
        ("کارت قیمت شارژر را بساز", "PrepareAction", "fa", "شارژر"),
        ("draft for the desk lamp", "PrepareAction", "en", "desk lamp"),
    ]
    for i, (msg, intent, lang, raw) in enumerate(ambiguous_specs, start=1):
        ctype = "Recommendation" if intent == "ApproveAction" else "Product"
        cands = [
            _ref(ctype, f"{raw[:3]}-a", raw, f"{raw} A"),
            _ref(ctype, f"{raw[:3]}-b", raw, f"{raw} B"),
        ]
        add(
            f"ctx-amb-{i:03d}",
            msg,
            intent,
            "picker",
            ambiguous=True,
            references=[_ref(ctype, "", raw)],
            candidates={raw: cands},
            lang=lang,
        )

    # 2) Multiple explicit references → PICKER (also ambiguous containment).
    multi_specs = [
        ("compare draft for SKU-1 and SKU-2", "ReviewAction", "en"),
        ("پیش‌نویس برای SKU-10 و SKU-11 بساز", "PrepareAction", "fa"),
        ("approve SKU-A and SKU-B", "ApproveAction", "en"),
        ("prepare کن برای SKU-77 و SKU-78", "PrepareAction", "mixed"),
        ("review SKU-5 or SKU-6", "ReviewAction", "en"),
    ]
    for i, (msg, intent, lang) in enumerate(multi_specs, start=1):
        refs = [
            _ref("Product", "p-x", "SKU-x", "Product X"),
            _ref("Product", "p-y", "SKU-y", "Product Y"),
        ]
        add(
            f"ctx-multi-{i:03d}",
            msg,
            intent,
            "picker",
            ambiguous=True,
            references=refs,
            lang=lang,
        )

    # 3) Unambiguous explicit reference → RESOLVED (override active context).
    override_specs = [
        ("قیمت SKU-9931 را آماده کن", "PrepareAction", "fa", "SKU-9931", "Product"),
        ("draft a change for SKU-4410", "PrepareAction", "en", "SKU-4410", "Product"),
        ("open recommendation REC-22", "ReviewAction", "en", "REC-22", "Recommendation"),
        ("پیش‌نویس REC-88 را باز کن", "ReviewAction", "fa", "REC-88", "Recommendation"),
        ("prepare کن برای SKU-2201", "PrepareAction", "mixed", "SKU-2201", "Product"),
        ("review action ACT-7781", "ReviewAction", "en", "ACT-7781", "ActionExecution"),
        ("قیمت SKU-333 را تنظیم کن", "PrepareAction", "fa", "SKU-333", "Product"),
        ("draft for selection SET-9", "PrepareAction", "en", "SET-9", "BulkSelection"),
        ("open product SKU-555", "Navigation", "en", "SKU-555", "Product"),
        ("این کالا SKU-777 را باز کن", "Navigation", "fa", "SKU-777", "Product"),
        ("review SKU-6100 draft", "ReviewAction", "en", "SKU-6100", "Product"),
        ("prepare REC-14", "PrepareAction", "en", "REC-14", "Recommendation"),
        ("کارت SKU-8000 را بساز", "PrepareAction", "fa", "SKU-8000", "Product"),
        ("open event EVT-3 page", "Navigation", "en", "EVT-3", "MarketEvent"),
        ("prepare change for SKU-1212", "PrepareAction", "en", "SKU-1212", "Product"),
        ("open recommendation REC-101", "ReviewAction", "en", "REC-101", "Recommendation"),
        ("پیش‌نویس SKU-9090 را بساز", "PrepareAction", "fa", "SKU-9090", "Product"),
        ("draft for selection SET-42", "PrepareAction", "en", "SET-42", "BulkSelection"),
        ("review action ACT-2020", "ReviewAction", "en", "ACT-2020", "ActionExecution"),
        ("prepare کن SKU-3131 رو", "PrepareAction", "mixed", "SKU-3131", "Product"),
    ]
    for i, (msg, intent, lang, raw, ctype) in enumerate(override_specs, start=1):
        add(
            f"ctx-ovr-{i:03d}",
            msg,
            intent,
            "resolved",
            references=[_ref(ctype, "", raw)],
            candidates={raw: [_ref(ctype, f"{raw}-id", raw, raw)]},
            active_context=_chip("GlobalAccount", account_id="acc-1"),
            expected_context_type=ctype,
            lang=lang,
        )

    # 4) No reference, specific active context, card-leading → RESOLVED in-context.
    incontext_specs = [
        ("قیمت را آماده کن", "PrepareAction", "fa", "Product"),
        ("draft a change here", "PrepareAction", "en", "Product"),
        ("این توصیه را آماده کن", "PrepareAction", "fa", "Recommendation"),
        ("prepare this recommendation", "PrepareAction", "en", "Recommendation"),
        ("review this draft", "ReviewAction", "en", "Product"),
        ("پیش‌نویس همین را باز کن", "ReviewAction", "fa", "Product"),
        ("prepare کن همین رو", "PrepareAction", "mixed", "Product"),
        ("draft for this selection set", "PrepareAction", "en", "BulkSelection"),
        ("این اقدام را مرور کن", "ReviewAction", "fa", "ActionExecution"),
        ("prepare the change for this item", "PrepareAction", "en", "Product"),
        ("همین توصیه را پیش‌نویس کن", "PrepareAction", "fa", "Recommendation"),
        ("review this bulk set", "ReviewAction", "en", "BulkSelection"),
        ("draft a change on this recommendation", "PrepareAction", "en", "Recommendation"),
        ("این کالا را برای اقدام آماده کن", "PrepareAction", "fa", "Product"),
    ]
    for i, (msg, intent, lang, ctype) in enumerate(incontext_specs, start=1):
        add(
            f"ctx-inctx-{i:03d}",
            msg,
            intent,
            "resolved",
            active_context=_chip(ctype, account_id="acc-1", entity_id="e-9"),
            expected_context_type=ctype,
            lang=lang,
        )

    # 5) Card-leading, account-level active context, no reference → PICKER.
    accountlevel_specs = [
        ("قیمت را آماده کن", "PrepareAction", "fa", "GlobalAccount"),
        ("prepare a price change", "PrepareAction", "en", "GlobalAccount"),
        ("draft a change", "PrepareAction", "en", "Operations"),
        ("این را تأیید کن", "ApproveAction", "fa", "GlobalAccount"),
        ("approve it", "ApproveAction", "en", "Settings"),
        ("review the draft", "ReviewAction", "en", "Operations"),
        ("پیش‌نویس بساز", "PrepareAction", "fa", "GlobalAccount"),
        ("prepare a card", "PrepareAction", "en", "MarketEvent"),
    ]
    for i, (msg, intent, lang, ctype) in enumerate(accountlevel_specs, start=1):
        add(
            f"ctx-acct-{i:03d}",
            msg,
            intent,
            "picker",
            ambiguous=True,
            active_context=_chip(ctype, account_id="acc-1"),
            lang=lang,
        )

    # 6) Question with active context (+ optional time phrase) → RESOLVED.
    question_specs = [
        ("قیمت فعلی چند است؟", "Question", "fa", "Product", None),
        ("what is the margin here?", "Question", "en", "Product", None),
        ("روند ۷ روز گذشته را نشان بده", "Question", "fa", "Product", "۷ روز گذشته"),
        ("show the last 30 days trend", "Question", "en", "Product", "last 30 days"),
        ("امروز چه خبر است؟", "Question", "fa", "GlobalAccount", "امروز"),
        ("what changed yesterday?", "Question", "en", "GlobalAccount", "yesterday"),
        ("this week's exposure?", "Question", "en", "GlobalAccount", "this week"),
        ("وضعیت این رویداد چیست؟", "Question", "fa", "MarketEvent", None),
        ("freshness over the past 5 days", "Question", "en", "Product", "past 5 days"),
        ("قیمت رقیب در ۱۴ روز اخیر", "Question", "mixed", "Product", "۱۴ روز اخیر"),
        ("margin این ماه چطور بود؟", "Question", "mixed", "Product", "این ماه"),
        ("what's my strategy?", "Administration", "en", "Settings", None),
        ("connection status?", "Administration", "en", "GlobalAccount", None),
        ("cost readiness این هفته", "Administration", "mixed", "GlobalAccount", "this week"),
        ("show today's briefing", "Question", "en", "GlobalAccount", "today"),
        ("رویداد بازار را توضیح بده", "Question", "fa", "MarketEvent", None),
        ("delta با دیروز", "Question", "mixed", "Product", "دیروز"),
        ("exposure over the last 90 days", "Question", "en", "GlobalAccount", "last 90 days"),
    ]
    for i, (msg, intent, lang, ctype, tphrase) in enumerate(question_specs, start=1):
        add(
            f"ctx-q-{i:03d}",
            msg,
            intent,
            "resolved",
            active_context=_chip(ctype, account_id="acc-1", entity_id="e-3"),
            time_phrase=tphrase,
            expected_context_type=ctype,
            lang=lang,
        )

    # 7) Reference matches nothing → NOT_FOUND (fail closed, never invent).
    notfound_specs = [
        ("draft change for SKU-0000", "PrepareAction", "en", "SKU-0000", "Product"),
        ("قیمت SKU-XXXX را آماده کن", "PrepareAction", "fa", "SKU-XXXX", "Product"),
        ("open recommendation REC-000", "ReviewAction", "en", "REC-000", "Recommendation"),
        ("prepare کن برای SKU-NONE", "PrepareAction", "mixed", "SKU-NONE", "Product"),
        ("review action ACT-0000", "ReviewAction", "en", "ACT-0000", "ActionExecution"),
    ]
    for i, (msg, intent, lang, raw, ctype) in enumerate(notfound_specs, start=1):
        add(
            f"ctx-nf-{i:03d}",
            msg,
            intent,
            "not_found",
            references=[_ref(ctype, "", raw)],
            candidates={raw: []},
            lang=lang,
        )

    return cases


def _write_jsonl(path: Path, rows: list[dict[str, object]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as fh:
        for row in rows:
            fh.write(json.dumps(row, ensure_ascii=False) + "\n")


def _write_intents() -> int:
    by_lang: dict[str, list[dict[str, object]]] = {"fa": [], "en": [], "mixed": []}
    for i, (message, intent, lang, shorthand) in enumerate(INTENT_CASES, start=1):
        by_lang[lang].append(
            {
                "id": f"int-{lang}-{i:03d}",
                "message": message,
                "lang": lang,
                "expected_intent": intent,
                "shorthand": shorthand,
                "pending_native_review": lang in ("fa", "mixed"),
            }
        )
    total = 0
    for lang, rows in by_lang.items():
        _write_jsonl(INTENTS_DIR / f"intents_{lang}.jsonl", rows)
        total += len(rows)
    return total


def _write_context() -> int:
    cases = _build_context_cases()
    ambiguous = [c for c in cases if c["ambiguous"]]
    unambiguous = [c for c in cases if not c["ambiguous"]]
    _write_jsonl(CONTEXT_DIR / "context_ambiguous.jsonl", ambiguous)
    _write_jsonl(CONTEXT_DIR / "context_resolved.jsonl", unambiguous)
    return len(cases)


def main() -> None:
    intents = _write_intents()
    context = _write_context()
    print(f"intents: {intents} cases; context: {context} cases")
    if intents != 200:
        raise SystemExit(f"expected 200 intent cases, emitted {intents}")
    if context != 100:
        raise SystemExit(f"expected 100 context cases, emitted {context}")


if __name__ == "__main__":
    main()
