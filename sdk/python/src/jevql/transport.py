from __future__ import annotations

import json
import urllib.error
import urllib.request
from abc import ABC, abstractmethod
from typing import Any, Dict, Mapping, Optional

from .engine import Engine
from .errors import STATUS_TO_CODE, JevqlError


class Transport(ABC):
    """Something that can run a statement and return the QueryResult document."""

    @abstractmethod
    def run(
        self,
        sql: str,
        *,
        threshold: Optional[float] = None,
        max_rows: Optional[int] = None,
        explain: bool = False,
    ) -> Dict[str, Any]:
        """Return the raw QueryResult dict for one statement."""

    def health(self) -> Dict[str, Any]:
        return {"ok": True}

    def close(self) -> None:  # pragma: no cover - default no-op
        return None


class HttpTransport(Transport):
    """Talks to a running jevql server (``jevql serve``) over HTTP."""

    def __init__(self, url: str, token: Optional[str] = None, timeout: float = 60.0):
        self.url = url.rstrip("/")
        self.token = token
        self.timeout = timeout

    def _headers(self) -> Dict[str, str]:
        h = {"Content-Type": "application/json", "Accept": "application/json"}
        if self.token:
            h["Authorization"] = f"Bearer {self.token}"
        return h

    def _request(self, method: str, path: str, body: Optional[Mapping[str, Any]] = None) -> Dict[str, Any]:
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.url + path, data=data, method=method, headers=self._headers())
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read()
                status = resp.status
        except urllib.error.HTTPError as e:
            raw = e.read()
            status = e.code
            payload = _decode(raw)
            code = payload.get("code") if isinstance(payload, dict) else None
            message = payload.get("error") if isinstance(payload, dict) else None
            raise JevqlError(
                str(message or f"HTTP {status}"),
                code=str(code or STATUS_TO_CODE.get(status, "internal")),
                status=status,
            ) from None
        except urllib.error.URLError as e:
            raise JevqlError(f"cannot reach jevql server at {self.url}: {e.reason}", code="transport") from None
        payload = _decode(raw)
        if not isinstance(payload, dict):
            raise JevqlError("malformed response from jevql server", code="transport", status=status)
        return payload

    def run(self, sql, *, threshold=None, max_rows=None, explain=False):
        body: Dict[str, Any] = {"sql": sql}
        if threshold is not None:
            body["threshold"] = float(threshold)
        if max_rows is not None:
            body["max_rows"] = int(max_rows)
        if explain:
            body["explain"] = True
        return self._request("POST", "/v1/query", body)

    def health(self) -> Dict[str, Any]:
        return self._request("GET", "/v1/health")


class EmbeddedTransport(Transport):
    """Starts a private engine on first use and talks to it over HTTP."""

    def __init__(self, engine: Engine, timeout: float = 60.0):
        self.engine = engine
        self.timeout = timeout
        self._http: Optional[HttpTransport] = None

    def _client(self) -> HttpTransport:
        url = self.engine.start()
        if self._http is None or self._http.url != url:
            self._http = HttpTransport(url, token=self.engine.token, timeout=self.timeout)
        return self._http

    def run(self, sql, *, threshold=None, max_rows=None, explain=False):
        return self._client().run(sql, threshold=threshold, max_rows=max_rows, explain=explain)

    def health(self) -> Dict[str, Any]:
        return self._client().health()

    def close(self) -> None:
        self.engine.stop()
        self._http = None


def _decode(raw: bytes) -> Any:
    if not raw:
        return {}
    try:
        return json.loads(raw.decode())
    except (UnicodeDecodeError, ValueError):
        return {"error": raw.decode(errors="replace").strip()}
