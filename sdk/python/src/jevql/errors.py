from __future__ import annotations

from typing import Optional

#: Error codes reported by jevql. ``transport`` is used for local failures
#: (connection refused, binary missing, malformed output).
CODES = ("sql", "budget", "api", "auth", "internal", "transport")

STATUS_TO_CODE = {
    400: "sql",
    401: "auth",
    402: "budget",
    500: "internal",
    502: "api",
}


class JevqlError(Exception):
    """Raised for any error from jevql or the transport."""

    def __init__(self, message: str, code: str = "internal", status: Optional[int] = None):
        super().__init__(message)
        self.message = message
        self.code = code if code in CODES else "internal"
        self.status = status

    def __str__(self) -> str:  # pragma: no cover - trivial
        return self.message

    def __repr__(self) -> str:  # pragma: no cover - trivial
        return f"JevqlError(code={self.code!r}, status={self.status!r}, message={self.message!r})"
