from __future__ import annotations

from typing import Any, Dict, List, Mapping, Optional

from .errors import JevqlError
from .models import Explain, QueryResult
from .transport import CliTransport, HttpTransport, Transport


class Jevql:
    """High-level client. Construct with ``url=`` for ``jevql serve`` or use
    :meth:`Jevql.cli` to drive the binary directly."""

    def __init__(
        self,
        url: Optional[str] = None,
        *,
        token: Optional[str] = None,
        timeout: float = 60.0,
        transport: Optional[Transport] = None,
    ):
        if transport is None:
            if not url:
                raise ValueError("Jevql(url=...) or Jevql.cli(...) is required")
            transport = HttpTransport(url, token=token, timeout=timeout)
        self.transport = transport

    @classmethod
    def cli(
        cls,
        binary: str = "jevql",
        database_url: Optional[str] = None,
        api_key: Optional[str] = None,
        api_url: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        cwd: Optional[str] = None,
        timeout: Optional[float] = None,
    ) -> "Jevql":
        return cls(
            transport=CliTransport(
                binary=binary,
                database_url=database_url,
                api_key=api_key,
                api_url=api_url,
                env=env,
                cwd=cwd,
                timeout=timeout,
            )
        )

    def query(self, sql: str, *, threshold: Optional[float] = None, max_rows: Optional[int] = None) -> QueryResult:
        return QueryResult.from_dict(self.transport.run(sql, threshold=threshold, max_rows=max_rows))

    def query_dicts(self, sql: str, **kw: Any) -> List[Dict[str, Any]]:
        return self.query(sql, **kw).dicts()

    def explain(self, sql: str) -> Explain:
        res = QueryResult.from_dict(self.transport.run(sql, explain=True))
        if res.explain is None:
            raise JevqlError("statement has no jev_* calls; nothing to explain", code="sql")
        return res.explain

    def health(self) -> Dict[str, Any]:
        return self.transport.health()

    def close(self) -> None:
        self.transport.close()

    def __enter__(self) -> "Jevql":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()
