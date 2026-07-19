#!/usr/bin/env python3
"""Offline guard for the collector self-telemetry scrape seam (issue #153, S33).

PRD §19.3 / docs/14 require the collection layer's OWN health — queue depth,
dropped telemetry, exporter failures, backpressure — to be observable. Prometheus
scrapes that self-telemetry through the ``otel-collector-internal`` job, but the
collector only exposes it if ``service.telemetry.metrics`` binds its metrics
listener to an interface Prometheus can reach over the compose network. The
0.119.0 default is ``localhost:8888`` (feature gate
``telemetry.UseLocalHostAsDefaultMetricsAddress``) — reachable ONLY inside the
collector container, so the advertised job silently stays down.

This is the OFFLINE half of the acceptance bar (runs in ``task obs:validate``
without Docker): it proves the config CANNOT reachably drift back to the loopback
default and that the bound port is exactly the port the scrape job targets. The
live half — ``up{job="otel-collector-internal"} == 1`` plus a real ``otelcol_``
series — is ``tools/obs/check_collector_internal.sh`` (needs the dev stack).

Run: ``python3 deploy/otel-collector/collector_internal_telemetry_test.py``.
"""

from __future__ import annotations

import pathlib
import sys

import yaml

HERE = pathlib.Path(__file__).parent
COLLECTOR_CONFIG = HERE / "config.yaml"
PROM_CONFIG = HERE.parent / "prometheus" / "prometheus.yml"
INTERNAL_JOB = "otel-collector-internal"
# Compose service name the collector is reachable by on the compose network.
COLLECTOR_SERVICE = "otel-collector"

# Hosts that are reachable ONLY inside the collector container. Binding the
# internal listener to any of these leaves the scrape job down — the exact
# #153 defect. An empty host (":8888") or a wildcard is compose-reachable.
LOOPBACK_HOSTS = {"localhost", "127.0.0.1", "::1", "[::1]"}


def _split_host_port(addr: str) -> tuple[str, int]:
    """Split ``host:port`` / ``:port`` / ``[::]:port`` into (host, port)."""
    addr = addr.strip()
    if addr.startswith("["):  # bracketed IPv6, e.g. [::]:8888
        host, _, port = addr.rpartition("]:")
        return host.lstrip("["), int(port)
    host, _, port = addr.rpartition(":")
    return host, int(port)


def collector_internal_bind(cfg: dict) -> tuple[str, int]:
    """Return (host, port) the collector's internal metrics listener binds to.

    Supports both syntaxes the 0.119.0 image accepts:
      * legacy  ``service.telemetry.metrics.address: HOST:PORT``
      * readers ``service.telemetry.metrics.readers[].pull.exporter.prometheus``
                with ``host``/``port`` (the otelconf form the 0.119.0 default
                config itself uses).

    Raises AssertionError if no explicit listener is configured — meaning the
    collector falls back to the loopback default, which is the bug.
    """
    metrics = (((cfg or {}).get("service") or {}).get("telemetry") or {}).get("metrics")
    assert metrics, (
        "service.telemetry.metrics is not configured — the collector falls back to "
        "the 0.119.0 loopback default (localhost:8888), unreachable by Prometheus "
        "over the compose network (issue #153)"
    )

    addr = metrics.get("address")
    if addr:
        return _split_host_port(str(addr))

    readers = metrics.get("readers") or []
    for reader in readers:
        prom = (((reader or {}).get("pull") or {}).get("exporter") or {}).get("prometheus")
        if prom and prom.get("host") is not None and prom.get("port") is not None:
            return str(prom["host"]), int(prom["port"])

    raise AssertionError(
        "service.telemetry.metrics is present but binds no explicit "
        "address/readers[].pull.prometheus host+port — cannot prove the listener "
        "is compose-reachable (issue #153)"
    )


def internal_scrape_target(prom_cfg: dict) -> tuple[str, int]:
    """Return (host, port) the otel-collector-internal scrape job targets."""
    jobs = {j.get("job_name"): j for j in (prom_cfg or {}).get("scrape_configs", [])}
    job = jobs.get(INTERNAL_JOB)
    assert job, f"prometheus.yml defines no scrape job '{INTERNAL_JOB}'"
    targets: list[str] = []
    for sc in job.get("static_configs", []):
        targets.extend(sc.get("targets", []))
    assert len(targets) == 1, (
        f"job '{INTERNAL_JOB}' expected exactly one target, got {targets!r}"
    )
    return _split_host_port(targets[0])


def main() -> int:
    errors: list[str] = []

    cfg = yaml.safe_load(COLLECTOR_CONFIG.read_text())
    prom_cfg = yaml.safe_load(PROM_CONFIG.read_text())

    try:
        bind_host, bind_port = collector_internal_bind(cfg)
    except AssertionError as exc:
        print("COLLECTOR SELF-TELEMETRY VALIDATION FAILED:")
        print("  " + str(exc))
        return 1

    # 1. The listener must NOT bind to a loopback-only interface, or Prometheus
    #    (a separate container) can never reach it.
    if bind_host in LOOPBACK_HOSTS:
        errors.append(
            f"internal metrics listener binds loopback host '{bind_host}:{bind_port}' — "
            f"unreachable by the Prometheus container (issue #153); bind 0.0.0.0"
        )

    # 2. The scrape job must target the collector by its compose service name and
    #    the SAME port the listener binds — otherwise the target is unreachable.
    tgt_host, tgt_port = internal_scrape_target(prom_cfg)
    if tgt_host != COLLECTOR_SERVICE:
        errors.append(
            f"job '{INTERNAL_JOB}' targets host '{tgt_host}', not the collector "
            f"compose service '{COLLECTOR_SERVICE}' — not reachable on the compose network"
        )
    if tgt_port != bind_port:
        errors.append(
            f"port mismatch: collector binds :{bind_port} but job '{INTERNAL_JOB}' "
            f"scrapes :{tgt_port} — the scrape target is unreachable"
        )

    if errors:
        print("COLLECTOR SELF-TELEMETRY VALIDATION FAILED:")
        for e in errors:
            print("  " + e)
        return 1

    print(
        f"OK: collector internal telemetry binds {bind_host}:{bind_port}; "
        f"job '{INTERNAL_JOB}' scrapes {tgt_host}:{tgt_port} (compose-reachable)."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
