# jevql (Python SDK)

Semantic SQL on vanilla Postgres. Write `WHERE jev(people, 'could work from home')`;
the jevql engine runs the SQL on Postgres, judges the rows with TypeSafe's Jev
model, and hands back a table. Nothing to install on the database server.

```bash
pip install jevql
```

That is the whole install. The wheel for your platform (macOS arm64 and x86_64,
Linux x86_64 and aarch64) bundles the jevql engine binary. No runtime
dependencies, Python 3.9+.

## Embedded engine (default)

`Jevql()` starts a private engine the first time you query and stops it when
you `close()` (or on interpreter exit). Connection and API settings come from
the same environment variables the CLI uses: `DATABASE_URL` or `PG*`,
`TYPESAFE_API_KEY`, `TYPESAFE_API_URL`, `JEV_THRESHOLD`.

```python
import os
from jevql import Jevql

with Jevql() as db:
    res = db.query(
        "SELECT name, jev_prob(people, 'could work from home') AS p "
        "FROM people WHERE country = 'PT' ORDER BY p DESC LIMIT 5",
        threshold=0.6,
    )
    res.columns        # ["name", "p"]
    res.rows           # [["Miguel Costa", 0.94], ...]
    res.stats.usd      # cost of that statement

    for row in db.query_dicts("SELECT * FROM tickets WHERE jev(tickets, 'is about billing')"):
        print(row["subject"])

    plan = db.explain("SELECT * FROM people WHERE jev(people, 'x')")   # no TypeSafe calls
```

Pass settings explicitly instead of through the environment:

```python
db = Jevql(
    database_url=os.environ["DATABASE_URL"],
    api_key=os.environ["TYPESAFE_API_KEY"],
    model="jev-latest",      # optional
    threshold=0.5,           # default jev() threshold
    max_rows=2500,           # abort before any API call if the collect is bigger
    cache_path="~/.cache/jevql/cache.db",  # or no_cache=True
)
```

The engine listens on a random loopback port with a random bearer token, and
exits by itself if your process dies.

## Remote server

For a shared engine (one warm cache, the API key held server-side), run
`jevql serve --listen 0.0.0.0:7433 --token secret` somewhere and point the
client at it:

```python
db = Jevql(url="http://jevql.internal:7433", token="secret")
```

## Errors

```python
from jevql import JevqlError

try:
    db.query("SELECT * FROM nope")
except JevqlError as e:
    print(e.code, e.status, e)   # "sql", 400, 'relation "nope" does not exist'
```

`code` is one of `sql`, `budget` (a cost guard fired before any API call),
`api` (TypeSafe error), `auth`, `internal`, or `transport` (engine or network
problem).

## Engine discovery

`Jevql()` looks for the engine binary in this order:

1. `Jevql(engine_path="/path/to/jevql")`
2. the `JEVQL_ENGINE_PATH` environment variable
3. the binary bundled in this package (`jevql/_engine/jevql`)
4. a `jevql` on `PATH` (for example from `brew install kylemclaren/tap/jevql`)

A source checkout has no bundled binary; use 2 or 4.

## Types

`QueryResult`, `Stats` and `Explain` are dataclasses with `from_dict`.
`QueryResult.dicts()` zips columns and rows.

## Building the platform wheels

```bash
python scripts/build_wheels.py --version 0.2.0 --tarballs dist/tarballs --out dist/wheels
```

reads `jevql_<version>_<os>_<arch>.tar.gz` release tarballs and produces one
tagged wheel per platform. The release workflow does this automatically.
