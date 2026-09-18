# jevql for Python

Python client for [jevql](https://github.com/kylemclaren/jevql), the psql-shaped
CLI that evaluates `jev()` on vanilla Postgres. Zero dependencies, Python 3.9+.

```bash
pip install jevql
```

The SDK does not reimplement the SQL parser or the two-pass planner. It drives
the `jevql` binary, so install that first:

```bash
brew install kylemclaren/tap/jevql      # or the release tarballs / go install
```

## Two transports, one result shape

**HTTP** — point it at a running `jevql serve` (one warm process, one cache):

```python
from jevql import Jevql

with Jevql(url="http://127.0.0.1:7433", token=None) as db:
    res = db.query("SELECT name, jev_prob(people, 'could work from home') AS p "
                   "FROM people WHERE country = 'PT' ORDER BY p DESC LIMIT 5")
    print(res.columns)        # ['name', 'p']
    for name, p in res.rows:  # rows are lists of JSON values
        print(name, p)
    print(res.stats.usd, res.stats.cache_hits)
```

**CLI** — spawn `jevql --json-table` per call (fine for scripts and notebooks):

```python
db = Jevql.cli(database_url="postgres://user:pass@localhost:5432/app",
               api_key="tsk_...")          # or rely on DATABASE_URL / TYPESAFE_API_KEY
rows = db.query_dicts("SELECT * FROM tickets WHERE jev(tickets, 'is about billing')")
```

Both return a `QueryResult`:

| field       | type            | notes                                        |
|-------------|-----------------|----------------------------------------------|
| `columns`   | `list[str]`     | output column names, in order                |
| `rows`      | `list[list]`    | JSON-typed values; timestamps are RFC 3339   |
| `row_count` | `int`           |                                              |
| `tag`       | `str`           | command tag, e.g. `SELECT 5` or `INSERT 0 1` |
| `jev`       | `bool`          | whether the statement used `jev_*`           |
| `stats`     | `Stats | None`  | judged, requests, cache_hits, tokens, usd, elapsed_ms |
| `explain`   | `Explain | None`| set by `explain()`                           |

`res.dicts()` / `query_dicts()` give `list[dict]` keyed by column.

## Explain before you spend

```python
ex = db.explain("SELECT * FROM people WHERE jev(people, 'could work from home')")
print(ex.collect_sql, ex.rows, ex.tokens, ex.usd)   # no TypeSafe calls are made
```

`query(sql, threshold=0.7, max_rows=500)` overrides the default `jev()`
threshold and the row guard for one call.

## Errors

Everything raises `jevql.JevqlError` with `.code` and `.status`:

| code        | meaning                                              |
|-------------|------------------------------------------------------|
| `sql`       | Postgres or jevql rejected the statement (HTTP 400 / exit 1) |
| `budget`    | `--max-rows` / `--max-chars` guard tripped (HTTP 402) |
| `api`       | TypeSafe API failure (HTTP 502 / exit 2)             |
| `auth`      | bad or missing bearer token (HTTP 401)               |
| `internal`  | unexpected server error (HTTP 500)                   |
| `transport` | could not reach `jevql serve` or run the binary      |

## Running the tests

```bash
cd sdk/python
pip install pytest
python -m pytest                       # mocks only
JEVQL_SERVE_URL=http://127.0.0.1:7433 python -m pytest   # plus live tests
```

Row contents are sent to TypeSafe by the binary; the same data-handling notes
as the CLI apply.
