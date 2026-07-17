from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="LoginRequest")


@_attrs_define
class LoginRequest:
    """Email/password credential presented to open a session.

    Attributes:
        email (str): Login identifier (the user's email).
        password (str): Plaintext password, presented only over TLS and never stored; verified against the argon2id hash
            and discarded.
    """

    email: str
    password: str

    def to_dict(self) -> dict[str, Any]:
        email = self.email

        password = self.password

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "email": email,
                "password": password,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        email = d.pop("email")

        password = d.pop("password")

        login_request = cls(
            email=email,
            password=password,
        )

        return login_request
