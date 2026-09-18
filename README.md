# jevQL

A psql-shaped client for **vanilla PostgreSQL** that understands `jev()`.

```sql
SELECT name, city, jev_prob(people, 'could work from home') AS p
FROM people
WHERE jev(people, 'could work from home')
  AND country = 'PT'
ORDER BY p DESC
LIMIT 20;
```

The database only ever sees ordinary SQL. `jev_*` calls are evaluated by the
CLI using [TypeSafe](https://typesafe.ai)'s System One model (Jev). No
`CREATE EXTENSION`, no superuser, no wire-protocol proxy.

## Install

Homebrew (macOS and Linux):

```bash
brew install kylemclaren/tap/jevql
```

Prebuilt binaries for macOS and Linux (arm64 and amd64) are attached to each
[GitHub release](https://github.com/kylemclaren/jevql/releases) as
`jevql_<version>_<os>_<arch>.tar.gz` with a `checksums.txt`.

From source, with Go 1.23+ and a C compiler (libpg_query is bundled via
[`pg_query_go`](https://github.com/pganalyze/pg_query_go) and needs CGO):

```bash
CGO_ENABLED=1 go install github.com/kylemclaren/jevql/cmd/jevql@latest
```

The first build compiles libpg_query and takes about a minute. On macOS,
`xcode-select --install` provides the compiler. If the build complains about
CGO, make sure `CGO_ENABLED=1` is set and that `cc` is on your `PATH`.

## Use

```bash
export TYPESAFE_API_KEY=tsk_...
export DATABASE_URL=postgres://user:pass@localhost:5432/app

jevql                                  # REPL
jevql -c "SELECT * FROM people WHERE jev(people, 'could work from home')"
jevql -f query.sql
jevql --explain -c "..."               # plan + cost estimate, no API calls
jevql "postgres://..." -c "SELECT 1"   # plain statements pass straight through
```

Run `jevql` with nothing configured and it asks for a database URL and a
TypeSafe API key (hidden input) and offers to save them to
`~/.config/jevql/env` (mode `0600`). That file holds `KEY=VALUE` lines and
only fills in environment variables that are not already set, so exported
variables and flags always win. `TYPESAFE_API_URL` can be added there too.
Inside the REPL, `\set TYPESAFE_API_KEY tsk_...` sets the key for the session.

Statements without `jev_*` are sent to Postgres unchanged. Any psql
connection style works: a URI, `-h/-p/-U/-d`, `PG*` environment variables, or
`DATABASE_URL`.

### SQL surface

The first argument is a **FROM alias** (`people`, or `p` in `FROM people p`).
All of that relation's columns are packed into a JSON row object and sent to
TypeSafe. To send fewer columns use the column-list form or `--columns`:

```sql
jev( (name, job_title, bio), 'could work from home' )
```

| Call | Returns |
|---|---|
| `jev(alias, 'condition')` | boolean, `p >= threshold` |
| `jev(alias, 'condition', 0.7)` | boolean with an explicit threshold |
| `jev_prob(alias, 'condition')` | float8 in 0..1 |
| `jev_choice(alias, 'question', ARRAY['a','b'])` | text |
| `jev_score(alias, 'question', ARRAY['lo','mid','hi'])` | float8 weighted level index |
| `jev_score_norm(...)` | float8 in 0..1 |
| `jev_confidence(alias, 'question' [, 'noul'\|'choice'\|'score', ARRAY[...]])` | float8 |
| `jev_eval(alias, 'question' [, kind, ARRAY[...]])` | raw answer JSON |

Threshold precedence: explicit argument, then `--threshold`, then
`JEV_THRESHOLD`, then `0.5`. `NOT jev(...)` is supported as `p < threshold`.

### What v1 supports

A single `SELECT` (a `WITH` is fine as long as the CTE has no `jev_*`):

- `jev()` as an `AND` term in `WHERE`, `NOT jev()` too.
- `jev_*` as a whole SELECT-list expression, and in `ORDER BY`.
- `GROUP BY` over `jev_choice(...)` (or any mix of jev / column keys) with
  `count(*)`, `count(x)`, `sum`, `avg`, `min`, `max`. Aggregation happens in
  the CLI, so `sum`/`avg` need numeric columns.
- `ORDER BY`, `LIMIT`, `OFFSET`. They are pushed to Postgres when the
  collected rows are exactly the final rows (no jev filter, no grouping, no
  jev sort key); otherwise they run in the CLI after judging.
- Joins. `jev(p, ...)` and `jev(c, ...)` on different aliases in one query.

It errors with a message rather than guessing on: `jev_*` in
INSERT/UPDATE/DELETE/DDL, in a CTE or subquery, in `HAVING`, under a window
function, `OR jev(...)`, comparisons like `jev_prob(...) > 0.7` (use
`jev(alias, q, 0.7)`), `SELECT DISTINCT`, set operations, and tables with
`bytea` columns unless you narrow the columns.

### How it runs

1. **Collect.** The statement is parsed with libpg_query (no regexes). Every
   non-jev predicate stays in the SQL that goes to Postgres; jev predicates,
   GROUP BY and jev-dependent ORDER BY/LIMIT are stripped. The SELECT list is
   extended with the columns needed to build row objects.
2. **Judge.** Row objects are canonicalised, deduplicated and looked up in a
   local sqlite cache. Misses are packed `--batch-size` rows per request per
   question and sent with `--concurrency` workers. Ctrl-C cancels in flight.
3. **Project.** Booleans, probabilities, choices and scores are attached in
   process, then filter, group, sort, limit, and print.

`--explain` prints the collect SQL, the row count after SQL filters, batch
count, a rough token estimate and cost. It makes no TypeSafe requests.

### Flags

```
-c string          run one statement and exit
-f file            run file and exit
-h host  -p port  -U user  -d dbname  -w / -W
--api-key          else TYPESAFE_API_KEY
--api-url          default https://api.typesafe.ai/v1/systemone (or TYPESAFE_API_URL)
--model            default jev-latest
--threshold        default 0.5 (or JEV_THRESHOLD)
--batch-size       default 40
--concurrency      default 6
--max-rows         default 2500   abort before any HTTP call if collect exceeds this
--max-chars        default 0      abort if row objects exceed this many chars (0 = off)
--cache            path, default ~/.cache/jevql/cache.db
--no-cache
--explain          plan + cost estimate, no TypeSafe calls
--timing           like psql \timing
--csv / --json     output format (default aligned table)
--expanded / -x    like psql \x
-v                 verbose: collect SQL, batches, tokens, cost
--columns a,b      columns to send for alias-form jev(alias, ...)
```

REPL meta commands: `\q` `\timing` `\x` `\d [rel]` `\dt` `\dn` `\l` `\i file`
`\set JEV_THRESHOLD 0.7` `\cache [stats|clear]` `\explain <query>` `help`.
Other backslash commands print "not implemented" instead of being sent to
the server.

Exit codes: `0` ok, `1` SQL or usage error, `2` TypeSafe API or budget error.

After a jev query the REPL (or `-v`) prints a footer:

```
jev: 129 judged, 4 req, 82 cache hits, 21.0k in tokens, $0.0009, 1.02s
```

### Cache

Answers are cached in sqlite (`modernc.org/sqlite`, pure Go) keyed by
`sha256(model + kind + question + options + canonical_row_json)`. Canonical
JSON has sorted keys, RFC 3339 timestamps in UTC, no whitespace, and NULL
columns included as `null`. There is no expiry in v1; use `\cache clear` or
`--no-cache`.

## Security and honesty

- **Row contents go to TypeSafe.** Every column of the judged alias (or the
  column list you give) is sent over HTTPS to the API. Do not use this on
  data you cannot share with TypeSafe. Use the column-list form or
  `--columns` to send the minimum.
- The API key comes from `TYPESAFE_API_KEY`, `--api-key`, the interactive
  prompt, or `~/.config/jevql/env`, and is never printed. The saved config
  file stores the key and the database URL (including its password) in plain
  text with mode `0600`; delete it if that is not acceptable. Point `--api-url` at a proxy if your environment injects
  credentials.
- This is **not** a Postgres extension. The server never learns `jev()`; if
  you send `WHERE jev(...)` through psql or JDBC it will fail with
  "function jev(...) does not exist" until it goes through this CLI or a real
  extension such as `pg-jev`.
- Every jev query is a **full scan of the post-SQL-filter row set**. Put cheap,
  indexed predicates in SQL first, and use `--explain` to see how many rows
  and dollars a query costs before running it. `--max-rows` (default 2500)
  aborts before any HTTP call.
- The cache file holds row-content hashes and TypeSafe answers. It is created
  with mode `0600` in `~/.cache/jevql`, but it is not encrypted; delete it
  with `\cache clear` if that matters.
- This is not a wire-compatible psql replacement for GUI tools or
  applications. Cost is estimated at $0.042 per million input tokens; the
  `usage` figures in the footer come from the API.

## Development

```bash
go build ./cmd/jevql
go test ./...                      # parse, rewrite (golden), typesafe mock, cache
PGTEST_URL=postgres://... go test ./internal/exec/   # loads testdata/people.sql into schema jevql_test
go test ./internal/rewrite/ -update                 # regenerate testdata/golden
```

Layout:

```
cmd/jevql/         main
internal/app/        flags, connection, REPL, meta commands, spinner
internal/parse/      pg_query walk, jev call extraction, statement analysis
internal/rewrite/    collect SQL builder and column layout
internal/exec/       two-pass execution: collect, judge, project
internal/typesafe/   HTTP client, batching, response parsing
internal/cache/      sqlite answer cache
internal/psqlout/    aligned / expanded / csv / json output
internal/canon/      canonical JSON for row objects
internal/stats/      the jev footer
testdata/            people.sql, queries/*.sql, golden/*.collect.sql
```

Prior art: [`realZachi/pg-jev`](https://github.com/realZachi/pg-jev) (the
extension whose function names this mirrors) and
[`EugeneBoondock/jevsql`](https://github.com/EugeneBoondock/jevsql) (a
two-pass client).

## Releasing

Tag a version and push it: `git tag v0.2.0 && git push origin v0.2.0`. The
release workflow builds native CGO binaries for darwin/linux on arm64/amd64,
attaches them plus `checksums.txt` to a GitHub release, and regenerates
`Formula/jevql.rb` in [kylemclaren/homebrew-tap](https://github.com/kylemclaren/homebrew-tap)
using `scripts/brew-formula.sh`. That step needs a `HOMEBREW_TAP_TOKEN`
repository secret with push access to the tap.
