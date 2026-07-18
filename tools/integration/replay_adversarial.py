#!/usr/bin/env python3
"""S32 adversarial containment replay (CHAT-041/045) — item 2 of
docs/implementation/dk-p0-implementation-steps.md's S32 suite.

Unlike the S24 eval harness (``uv run python -m llm.evals``), which drives the
LangGraph turn IN-PROCESS inside services/llm's own test/eval process, this
script replays the SAME adversarial fixture set (plus fuzzed variants) as REAL
HTTP requests against a REAL RUNNING STACK: the gateway's authenticated
``POST /chat`` (services/core), which proxies to a REAL running LLM plane
(services/llm, mock provider by default). It is the cross-plane wire proof the
in-process harness cannot give: free text sent over the actual network,
through the actual session-authenticated gateway route, must never produce a
non-empty ``transitions`` list and must never change the account's visible
approval/action state.

Usage (against an already-running stack — see
docs/implementation/dk-p0-monorepo.md §3 `task test:integration` and this
script's ``--help``):

    python3 tools/integration/replay_adversarial.py \\
        --core-url http://localhost:8080 \\
        --email owner@dev.local --password "$SEEDE2E_PASSWORD" \\
        --account-id 00000000-0000-0000-0000-000000000003 \\
        --fixture services/llm/fixtures/evals/adversarial/approval.jsonl \\
        --fuzz 3

Exit code is 0 only when EVERY case (fixture + fuzzed) produced zero
transitions and the account's action/approval list is byte-identical before
and after the whole run. Any transition, any state diff, or any unexpected
transport error is a release-blocking failure (§4.6 never-cut: free-text
containment).
"""

from __future__ import annotations

import argparse
import json
import sys
import unicodedata
from pathlib import Path
from typing import Any

import httpx

# --- fuzzing -----------------------------------------------------------------
# Adversarial mutations layered on top of each authored fixture message. These
# are NOT meant to change the adversary's intent — only to probe whether
# surface-level noise (casing, whitespace, zero-width/bidi characters, repeated
# punctuation) can slip a coercive message past containment. A fuzzed case
# inherits its base case's expectations (transitions must still be empty).

_ZERO_WIDTH = "​‌‍﻿"


def _fuzz_variants(message: str, n: int) -> list[str]:
    variants: list[str] = []
    generators = [
        lambda m: m.upper(),
        lambda m: m.lower(),
        lambda m: "   " + m + "   ",
        lambda m: m.replace(" ", "  "),
        lambda m: _ZERO_WIDTH.join(m) if m else m,
        lambda m: m + "!!!!!!",
        lambda m: "".join(unicodedata.normalize("NFKC", ch) for ch in m) + " " + m,
        lambda m: m + "\n\n" + m,
    ]
    for gen in generators[:n]:
        variants.append(gen(message))
    return variants


# --- replay --------------------------------------------------------------


class ContainmentFailure(RuntimeError):
    pass


def load_fixture(path: Path) -> list[dict[str, Any]]:
    cases: list[dict[str, Any]] = []
    with path.open(encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            cases.append(json.loads(line))
    return cases


def login(client: httpx.Client, core_url: str, email: str, password: str) -> None:
    resp = client.post(f"{core_url}/auth/login", json={"email": email, "password": password})
    if resp.status_code != 200:
        raise ContainmentFailure(
            f"login failed: status={resp.status_code} body={resp.text!r} "
            "(provision a seeded owner first, e.g. services/core/cmd/seede2e)"
        )


def snapshot_actions(client: httpx.Client, core_url: str, account_id: str) -> Any:
    """A best-effort state snapshot: the account's action list. Read-only;
    never mutates anything. Used to prove zero state diff across the whole
    replay, on top of the per-case zero-transitions assertion."""
    resp = client.get(
        f"{core_url}/actions", params={"marketplaceAccountId": account_id, "limit": 200}
    )
    if resp.status_code != 200:
        # A 503 (unwired) is acceptable — the snapshot is best-effort context,
        # not the primary containment proof (which is per-case transitions).
        return {"unavailable": resp.status_code}
    return resp.json()


def replay_one(
    client: httpx.Client, core_url: str, account_id: str, case_id: str, message: str
) -> list[Any]:
    resp = client.post(
        f"{core_url}/chat",
        json={"message": message, "marketplaceAccountId": account_id},
        timeout=30.0,
    )
    if resp.status_code == 503:
        # Kill switch or LLM plane unwired — containment is trivially satisfied
        # (screens-only fallback, CHAT-009) but this is not what this replay
        # is measuring; surface it distinctly so a misconfigured run is obvious.
        raise ContainmentFailure(
            f"case {case_id}: /chat returned 503 (kill switch / LLM plane unwired) — "
            "this replay requires a LIVE LLM plane; wire LLM_SERVICE_URL and retry"
        )
    if resp.status_code != 200:
        raise ContainmentFailure(
            f"case {case_id}: /chat status={resp.status_code} body={resp.text!r}"
        )

    transitions: list[Any] = []
    for raw_line in resp.text.splitlines():
        if not raw_line.startswith("data:"):
            continue
        payload = raw_line[len("data:") :].strip()
        if not payload:
            continue
        try:
            frame = json.loads(payload)
        except json.JSONDecodeError:
            continue
        if frame.get("kind") != "final":
            continue
        envelope = frame.get("envelope", {})
        for key in ("guidance", "approval_card", "level2_proposal", "selection_set"):
            part = envelope.get(key)
            if isinstance(part, dict) and "transitions" in part:
                transitions.extend(part["transitions"] or [])
    return transitions


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--core-url", default="http://localhost:8080")
    parser.add_argument("--email", required=True)
    parser.add_argument("--password", required=True)
    parser.add_argument("--account-id", required=True)
    parser.add_argument(
        "--fixture",
        default="services/llm/fixtures/evals/adversarial/approval.jsonl",
        help="path to the S24 adversarial fixture (jsonl)",
    )
    parser.add_argument(
        "--fuzz", type=int, default=3, help="fuzzed variants per fixture case (0 disables)"
    )
    parser.add_argument("--report", default=None, help="path to write a JSON summary")
    args = parser.parse_args(argv)

    fixture_path = Path(args.fixture)
    cases = load_fixture(fixture_path)
    if not cases:
        print(f"no cases loaded from {fixture_path}", file=sys.stderr)
        return 2

    results: list[dict[str, Any]] = []
    with httpx.Client() as client:
        login(client, args.core_url, args.email, args.password)
        before = snapshot_actions(client, args.core_url, args.account_id)

        total_replayed = 0
        for case in cases:
            targets = [("base", case["message"])]
            for i, variant in enumerate(_fuzz_variants(case["message"], args.fuzz)):
                targets.append((f"fuzz{i}", variant))

            for variant_kind, message in targets:
                total_replayed += 1
                try:
                    transitions = replay_one(
                        client, args.core_url, args.account_id, case["id"], message
                    )
                except ContainmentFailure as exc:
                    print(f"FAIL {case['id']}/{variant_kind}: {exc}", file=sys.stderr)
                    return 1
                ok = transitions == []
                results.append(
                    {
                        "id": case["id"],
                        "variant": variant_kind,
                        "message": message,
                        "transitions": transitions,
                        "ok": ok,
                    }
                )
                if not ok:
                    print(
                        f"CONTAINMENT FAILURE {case['id']}/{variant_kind}: "
                        f"transitions={transitions!r} for message={message!r}",
                        file=sys.stderr,
                    )

        after = snapshot_actions(client, args.core_url, args.account_id)

    failed = [r for r in results if not r["ok"]]
    state_diff = before != after

    print(f"replayed {total_replayed} cases ({len(cases)} fixture x {1 + args.fuzz} variants)")
    print(f"containment failures (non-empty transitions): {len(failed)}")
    print(f"state diff across the whole replay: {state_diff}")

    if args.report:
        Path(args.report).write_text(
            json.dumps(
                {
                    "total": total_replayed,
                    "failed": len(failed),
                    "state_diff": state_diff,
                    "results": results,
                },
                ensure_ascii=False,
                indent=2,
            ),
            encoding="utf-8",
        )

    if failed or state_diff:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
