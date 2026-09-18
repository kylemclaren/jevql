from __future__ import annotations

from typing import Any, Dict, List, Optional

from .engine import Engine
from .errors import JevqlError
from .models import Explain, QueryResult
from .transport import EmbeddedTransport, HttpTransport, Transport


class Jevql:
    """Client for jevql.

    ``Jevql()`` runs a private, embedded engine (the bundled jevql binary) and
    reads connection settings from the environment (``DATABASE_URL``, ``PG*``,
    ``TYPESAFE_API_KEY``) or from the keyword options.

    ``Jevql(url="http://host:7433", token="...")`` talks to a shared
    ``jevql serve`` instead.
    """

    def __init__(
        self,
        url: Optional[str] = None,
        token: Optional[str] = None,
        *,
        database_url: Optional[str] = None,
        api_key: Optional[str] = None,
        api_url: Optional[str] = None,
        model: Optional[str] = None,
        threshold: Optional[float] = None,
        max_rows: Optional[int] = None,
        cache_path: Optional[str] = None,
        no_cache: bool = False,
        engine_path: Optional[str] = None,
        timeout: float = 60.0,
        transport: Optional[Transport] = None,
    ):
        if transport is not None:
            self.transport = transport
        elif url:
            self.transport = HttpTransport(url, token=token, timeout=timeout)
        else:
            engine = Engine(
                engine_path=engine_path,
                database_url=database_url,
                api_key=api_key,
                api_url=api_url,
                model=model,
                threshold=threshold,
                max_rows=max_rows,
                cache_path=cache_path,
                no_cache=no_cache,
            )
            self.transport = EmbeddedTransport(engine, timeout=timeout)

    @property
    def embedded(self) -> bool:
        return isinstance(self.transport, EmbeddedTransport)

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
