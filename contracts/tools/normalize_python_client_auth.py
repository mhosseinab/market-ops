"""Align generated Python client types with the OpenAPI security schemes.

openapi-python-client 0.29.0 reduces operation security to a boolean. It does
not inherit root security and its only authenticated client injects an HTTP
Authorization header. That makes cookie-secured operations appear to support
the bearer-oriented AuthenticatedClient. This post-generation hook restores
the authored contract's effective security modes without hand-editing gen/.
"""

from __future__ import annotations

import re
import sys
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from ruamel.yaml import YAML

HTTP_METHODS = frozenset({"delete", "get", "head", "options", "patch", "post", "put", "trace"})
CLIENT_ANNOTATION = re.compile(
    r"client: (?:AuthenticatedClient \| Client|AuthenticatedClient|Client)(?=[,)])"
)


def python_identifier(value: str) -> str:
    """Match openapi-python-client 0.29.0's operation/tag snake casing."""
    sanitized = re.sub(r"[^\w\. _-]+", "", value)
    if any(character.isupper() for character in sanitized):
        sanitized = " ".join(re.split("([A-Z]?[a-z]+)", sanitized))
    return "_".join(re.findall(r"[^\. _-]+", sanitized)).lower()


def security_scheme_names(requirements: Any) -> set[str]:
    if requirements is None:
        return set()
    if not isinstance(requirements, list):
        raise ValueError("security requirements must be a list")

    names: set[str] = set()
    for requirement in requirements:
        if not isinstance(requirement, Mapping):
            raise ValueError("each security requirement must be a mapping")
        names.update(str(name) for name in requirement)
    return names


def client_annotation(
    requirements: Any,
    security_schemes: Mapping[str, Any],
) -> str:
    names = security_scheme_names(requirements)
    if not names:
        return "Client"

    accepts_cookie = False
    accepts_bearer = False
    for name in names:
        raw_scheme = security_schemes.get(name)
        if not isinstance(raw_scheme, Mapping):
            raise ValueError(f"security requirement references unknown scheme {name!r}")

        scheme_type = raw_scheme.get("type")
        location = raw_scheme.get("in")
        http_scheme = raw_scheme.get("scheme")
        if scheme_type == "apiKey" and location == "cookie":
            accepts_cookie = True
        elif scheme_type == "http" and http_scheme == "bearer":
            accepts_bearer = True
        else:
            raise ValueError(
                f"generated Python client has no credential adapter for security scheme {name!r}"
            )

    if accepts_cookie and accepts_bearer:
        return "AuthenticatedClient | Client"
    if accepts_bearer:
        return "AuthenticatedClient"
    return "Client"


def normalize_module(module_path: Path, annotation: str) -> None:
    source = module_path.read_text(encoding="utf-8")
    normalized, substitutions = CLIENT_ANNOTATION.subn(f"client: {annotation}", source)
    if substitutions == 0:
        raise ValueError(f"no generated client annotations found in {module_path}")

    if annotation == "Client":
        normalized = normalized.replace(
            "from ...client import AuthenticatedClient, Client",
            "from ...client import Client",
        )
    elif annotation == "AuthenticatedClient":
        normalized = normalized.replace(
            "from ...client import AuthenticatedClient, Client",
            "from ...client import AuthenticatedClient",
        )

    module_path.write_text(normalized, encoding="utf-8")


def normalize_generated_client(spec_path: Path, package_path: Path) -> None:
    yaml = YAML(typ="safe")
    with spec_path.open(encoding="utf-8") as spec_file:
        document = yaml.load(spec_file)
    if not isinstance(document, Mapping):
        raise ValueError("OpenAPI document must be a mapping")

    components = document.get("components")
    paths = document.get("paths")
    if not isinstance(components, Mapping) or not isinstance(paths, Mapping):
        raise ValueError("OpenAPI document must define components and paths mappings")
    security_schemes = components.get("securitySchemes")
    if not isinstance(security_schemes, Mapping):
        raise ValueError("OpenAPI document must define security schemes")

    root_security = document.get("security", [])
    normalized_modules: set[Path] = set()
    for path_item in paths.values():
        if not isinstance(path_item, Mapping):
            continue
        for method, operation in path_item.items():
            if method not in HTTP_METHODS or not isinstance(operation, Mapping):
                continue

            operation_id = operation.get("operationId")
            tags = operation.get("tags")
            if not isinstance(operation_id, str) or not isinstance(tags, list) or not tags:
                raise ValueError(
                    "every generated operation must define operationId and at least one tag"
                )

            effective_security = operation.get("security", root_security)
            annotation = client_annotation(effective_security, security_schemes)
            module_path = (
                package_path
                / "api"
                / python_identifier(str(tags[0]))
                / f"{python_identifier(operation_id)}.py"
            )
            if not module_path.is_file():
                raise ValueError(
                    f"generated endpoint module missing for {operation_id}: {module_path}"
                )
            normalize_module(module_path, annotation)
            normalized_modules.add(module_path)

    generated_modules = set((package_path / "api").glob("*/*.py"))
    generated_modules = {path for path in generated_modules if path.name != "__init__.py"}
    unclassified = generated_modules - normalized_modules
    if unclassified:
        names = ", ".join(str(path) for path in sorted(unclassified))
        raise ValueError(f"generated endpoint modules were not classified: {names}")


def main() -> int:
    if len(sys.argv) != 3:
        print(
            "usage: normalize_python_client_auth.py OPENAPI_SPEC GENERATED_PACKAGE",
            file=sys.stderr,
        )
        return 2

    normalize_generated_client(Path(sys.argv[1]), Path(sys.argv[2]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
