#!/usr/bin/env python3
"""Assert dev-stack published ports bind to loopback (issue #10).

Privileged/admin dev services in deploy/compose.dev.yml must not be reachable
beyond the developer's localhost by default. This is a deterministic, docker-free
guard: it parses a Compose file, expands ``${VAR:-default}`` host-IP prefixes
against the current environment, and reports any published port whose host IP is
not a loopback address.

Stdlib only — no PyYAML — so it always runs in `task ci:local` regardless of the
uv environment. The parser targets the narrow `ports:` list structure Compose
uses; it deliberately does not attempt to be a general YAML parser.

Usage:
    check_compose_ports.py <compose-file> [<compose-file> ...]

Exit status:
    0  every published port binds to a loopback host IP under the current env
    1  one or more published ports bind to a non-loopback (all-interfaces) IP
    2  usage / file error
"""

from __future__ import annotations

import os
import re
import sys
from dataclasses import dataclass

# A published port with no host IP (e.g. "5432:5432") binds every interface;
# these loopback hosts are the only acceptable host IPs for the dev stack.
LOOPBACK_HOSTS = {"127.0.0.1", "::1", "localhost"}

_SERVICE_RE = re.compile(r"^  ([A-Za-z0-9_.-]+):\s*(#.*)?$")
_PORTS_RE = re.compile(r"^    ports:\s*(#.*)?$")
_PORT_ITEM_RE = re.compile(r"^      -\s+(.*\S)\s*$")
_VAR_RE = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}")


@dataclass(frozen=True)
class PublishedPort:
    service: str
    raw: str
    host_ip: str | None  # None => no host IP => all interfaces


def _expand(value: str, env: dict[str, str]) -> str:
    """Expand ``${VAR}`` / ``${VAR:-default}`` using ``env``."""

    def repl(m: re.Match[str]) -> str:
        name, default = m.group(1), m.group(2)
        got = env.get(name)
        if got is not None and got != "":
            return got
        return default if default is not None else ""

    return _VAR_RE.sub(repl, value)


def _strip_port_entry(entry: str) -> str:
    """Strip surrounding quotes and any trailing inline comment."""
    entry = entry.strip()
    if entry and entry[0] in "'\"":
        quote = entry[0]
        end = entry.find(quote, 1)
        if end != -1:
            return entry[1:end]
    # Unquoted: drop an inline comment if present.
    hashpos = entry.find("#")
    if hashpos != -1:
        entry = entry[:hashpos]
    return entry.strip()


def _host_ip(mapping: str) -> str | None:
    """Return the host IP of a "[ip:]host:container[/proto]" mapping, or None.

    Compose long/short mappings can be:
      "container"                       -> published on all interfaces (None)
      "host:container"                  -> all interfaces (None)
      "ip:host:container"               -> bound to ip
    IPv6 host IPs appear bracketed (e.g. "[::1]:5432:5432").
    """
    mapping = mapping.strip()
    # Bracketed IPv6 host IP.
    if mapping.startswith("["):
        end = mapping.find("]")
        if end != -1:
            return mapping[1:end]
    parts = mapping.split(":")
    # Drop a trailing /proto on the container part (doesn't affect host ip).
    if len(parts) >= 3:
        return parts[0]
    # 1 or 2 parts => no explicit host IP => all interfaces.
    return None


def parse_published_ports(text: str, env: dict[str, str]) -> list[PublishedPort]:
    """Parse published ports from a Compose file body."""
    ports: list[PublishedPort] = []
    service: str | None = None
    in_ports = False
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        svc = _SERVICE_RE.match(line)
        if svc:
            service = svc.group(1)
            in_ports = False
            continue
        if _PORTS_RE.match(line):
            in_ports = True
            continue
        if in_ports:
            item = _PORT_ITEM_RE.match(line)
            if item:
                raw = _strip_port_entry(item.group(1))
                expanded = _expand(raw, env)
                ports.append(
                    PublishedPort(
                        service=service or "<unknown>",
                        raw=raw,
                        host_ip=_host_ip(expanded),
                    )
                )
                continue
            # Any line that is not a 6-space list item ends the ports block.
            in_ports = False
    return ports


def violations(ports: list[PublishedPort]) -> list[PublishedPort]:
    return [p for p in ports if p.host_ip not in LOOPBACK_HOSTS]


def check_file(path: str, env: dict[str, str]) -> list[PublishedPort]:
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    return violations(parse_published_ports(text, env))


def main(argv: list[str]) -> int:
    files = argv[1:]
    if not files:
        print("usage: check_compose_ports.py <compose-file> [...]", file=sys.stderr)
        return 2
    env = dict(os.environ)
    failed = False
    for path in files:
        try:
            bad = check_file(path, env)
        except OSError as exc:
            print(f"check_compose_ports: cannot read {path}: {exc}", file=sys.stderr)
            return 2
        if bad:
            failed = True
            print(
                f"{path}: {len(bad)} published port(s) bind beyond loopback "
                f"(host IP must be one of {sorted(LOOPBACK_HOSTS)}):",
                file=sys.stderr,
            )
            for p in bad:
                shown = p.host_ip if p.host_ip is not None else "0.0.0.0 (all interfaces)"
                print(f"  - {p.service}: '{p.raw}' -> host {shown}", file=sys.stderr)
        else:
            print(f"{path}: OK — every published port binds to loopback")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
