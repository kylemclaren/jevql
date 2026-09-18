from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List, Mapping, Optional


@dataclass
class Stats:
    """Judging statistics for a statement that used jev_*."""

    collect_rows: int = 0
    judged: int = 0
    requests: int = 0
    cache_hits: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    usd: float = 0.0
    elapsed_ms: float = 0.0

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "Stats":
        return cls(
            collect_rows=int(d.get("collect_rows", 0) or 0),
            judged=int(d.get("judged", 0) or 0),
            requests=int(d.get("requests", 0) or 0),
            cache_hits=int(d.get("cache_hits", 0) or 0),
            input_tokens=int(d.get("input_tokens", 0) or 0),
            output_tokens=int(d.get("output_tokens", 0) or 0),
            usd=float(d.get("usd", 0.0) or 0.0),
            elapsed_ms=float(d.get("elapsed_ms", 0.0) or 0.0),
        )


@dataclass
class Explain:
    """The --explain report: plan and cost estimate, no TypeSafe calls."""

    collect_sql: str = ""
    rows: int = 0
    sources: int = 0
    questions: int = 0
    judgements: int = 0
    batches: int = 0
    avg_row_chars: float = 0.0
    tokens: int = 0
    usd: float = 0.0
    server_order: bool = False
    question_list: List[str] = field(default_factory=list)

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "Explain":
        return cls(
            collect_sql=str(d.get("collect_sql", "") or ""),
            rows=int(d.get("rows", 0) or 0),
            sources=int(d.get("sources", 0) or 0),
            questions=int(d.get("questions", 0) or 0),
            judgements=int(d.get("judgements", 0) or 0),
            batches=int(d.get("batches", 0) or 0),
            avg_row_chars=float(d.get("avg_row_chars", 0.0) or 0.0),
            tokens=int(d.get("tokens", 0) or 0),
            usd=float(d.get("usd", 0.0) or 0.0),
            server_order=bool(d.get("server_order", False)),
            question_list=[str(q) for q in (d.get("question_list") or [])],
        )


@dataclass
class QueryResult:
    """One statement's result."""

    columns: List[str] = field(default_factory=list)
    rows: List[List[Any]] = field(default_factory=list)
    row_count: int = 0
    tag: str = ""
    jev: bool = False
    stats: Optional[Stats] = None
    explain: Optional[Explain] = None

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "QueryResult":
        rows = [list(r) for r in (d.get("rows") or [])]
        stats = d.get("stats")
        explain = d.get("explain")
        return cls(
            columns=[str(c) for c in (d.get("columns") or [])],
            rows=rows,
            row_count=int(d.get("row_count", len(rows)) or 0),
            tag=str(d.get("tag", "") or ""),
            jev=bool(d.get("jev", False)),
            stats=Stats.from_dict(stats) if isinstance(stats, Mapping) else None,
            explain=Explain.from_dict(explain) if isinstance(explain, Mapping) else None,
        )

    def dicts(self) -> List[Dict[str, Any]]:
        """Rows as dicts keyed by column name."""
        return [dict(zip(self.columns, r)) for r in self.rows]

    def __len__(self) -> int:
        return len(self.rows)

    def __iter__(self):
        return iter(self.rows)
