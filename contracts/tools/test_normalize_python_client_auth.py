from pathlib import Path

import pytest
from normalize_python_client_auth import client_annotation, normalize_document

SECURITY_SCHEMES = {
    "cookieAuth": {"type": "apiKey", "in": "cookie"},
    "bearerAuth": {"type": "http", "scheme": "bearer"},
}


@pytest.mark.parametrize(
    ("requirements", "expected"),
    [
        ([], "Client"),
        ([{}], "Client"),
        ([{"cookieAuth": []}], "Client"),
        ([{"bearerAuth": []}], "AuthenticatedClient"),
        (
            [{"cookieAuth": []}, {"bearerAuth": []}],
            "AuthenticatedClient | Client",
        ),
        (
            [{}, {"bearerAuth": []}],
            "AuthenticatedClient | Client",
        ),
    ],
)
def test_client_annotation_preserves_security_alternatives(
    requirements: object, expected: str
) -> None:
    assert client_annotation(requirements, SECURITY_SCHEMES) == expected


def test_client_annotation_rejects_compound_and_requirement() -> None:
    with pytest.raises(ValueError, match="compound AND"):
        client_annotation(
            [{"cookieAuth": [], "bearerAuth": []}],
            SECURITY_SCHEMES,
        )


def test_client_annotation_rejects_unknown_scheme() -> None:
    with pytest.raises(ValueError, match="unknown scheme"):
        client_annotation([{"unknownAuth": []}], SECURITY_SCHEMES)


def test_client_annotation_rejects_missing_requirements() -> None:
    with pytest.raises(ValueError, match="security requirements must be a list"):
        client_annotation(None, SECURITY_SCHEMES)


def test_normalizer_inherits_root_security(tmp_path: Path) -> None:
    package = tmp_path / "gateway_client"
    module = package / "api" / "system" / "get_status.py"
    module.parent.mkdir(parents=True)
    module.write_text(
        "from ...client import AuthenticatedClient, Client\n"
        "def sync(*, client: AuthenticatedClient | Client): ...\n",
        encoding="utf-8",
    )

    normalize_document(
        {
            "components": {"securitySchemes": SECURITY_SCHEMES},
            "paths": {"/status": {"get": {"operationId": "getStatus", "tags": ["system"]}}},
            "security": [{"cookieAuth": []}],
        },
        package,
    )

    assert "client: Client" in module.read_text(encoding="utf-8")


def test_normalizer_rejects_missing_generated_module(tmp_path: Path) -> None:
    package = tmp_path / "gateway_client"
    (package / "api").mkdir(parents=True)

    with pytest.raises(ValueError, match="generated endpoint module missing"):
        normalize_document(
            {
                "components": {"securitySchemes": SECURITY_SCHEMES},
                "paths": {"/status": {"get": {"operationId": "getStatus", "tags": ["system"]}}},
                "security": [{"cookieAuth": []}],
            },
            package,
        )


def test_normalizer_rejects_unclassified_generated_module(tmp_path: Path) -> None:
    package = tmp_path / "gateway_client"
    module = package / "api" / "system" / "unexpected.py"
    module.parent.mkdir(parents=True)
    module.write_text(
        "from ...client import AuthenticatedClient, Client\n"
        "def sync(*, client: AuthenticatedClient | Client): ...\n",
        encoding="utf-8",
    )

    with pytest.raises(ValueError, match="were not classified"):
        normalize_document(
            {
                "components": {"securitySchemes": SECURITY_SCHEMES},
                "paths": {},
                "security": [{"cookieAuth": []}],
            },
            package,
        )
