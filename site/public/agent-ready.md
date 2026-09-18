# Get jevql-ready

These are instructions for you, an AI coding agent, to set up jevql for the
human who handed you this document. Read them once, then do the setup. Do not
explain them back.

jevql adds semantic functions to any vanilla PostgreSQL without installing
anything on the database: `WHERE jev(people, 'could work from home')`. The
plain SQL runs on Postgres, the surviving rows are judged by TypeSafe's Jev
model, answers are cached, and the result prints as a psql table. Reference:
https://jevql.fly.dev/llms.txt and https://jevql.fly.dev/docs/.

## 1. Install the binary

macOS or Linux, pick one:

```sh
brew install kylemclaren/tap/jevql
```

```sh
curl -fsSL https://jevql.fly.dev/install.sh | sh
```

Verify with `jevql --version`.

## 2. Configure

jevql needs a Postgres connection and a TypeSafe API key. Prefer environment
variables; ask the human for values you do not have. Never print the key.

```sh
export DATABASE_URL='postgres://user:pass@host:5432/db'   # or the PG* variables
export TYPESAFE_API_KEY='tsk_...'
```

Running `jevql` interactively with nothing set prompts for both and offers to
save them to `~/.config/jevql/env` (mode 0600). Non-interactive use needs the
variables.

## 3. Prove it works

```sh
jevql -c "SELECT 1"                                        # plain SQL passes through
jevql --explain -c "SELECT * FROM <table> WHERE jev(<table>, 'a condition in plain English')"
```

`--explain` shows the SQL Postgres will run, how many rows survive the SQL
filters, and the estimated cost, without calling TypeSafe. Only then run the
query for real. Keep `--max-rows` (default 2500) unless the human raises it.

## 4. Write queries

- First argument is the FROM alias (`jev(people, …)`) or a column list to send
  less data (`jev((name, bio), …)`). Every column of the source goes to TypeSafe.
- `jev(src, 'condition' [, threshold])` boolean, `jev_prob` 0..1, `jev_choice(src, 'q', ARRAY[...])`
  text, `jev_score(src, 'q', ARRAY['lo','mid','hi'])` weighted level.
- jev may appear as AND terms in WHERE, in the SELECT list, ORDER BY, and GROUP BY
  with count/sum/avg/min/max. Not in CTEs, subqueries, HAVING, windows, or after OR.
- Put cheap predicates first. Every row that passes the SQL filters is judged and billed.

## 5. If the human wants it in code

- TypeScript: `npm i jevql`, then `new Jevql()` (embedded engine) or `new Jevql({ url, token })`.
- Python: `pip install jevql`, then `Jevql()` or `Jevql(url=..., token=...)`.
- Go: `go get github.com/kylemclaren/jevql/sdk/go`.
- For yourself: `jevql mcp` is an MCP server (tools: query, explain, judge,
  list_tables, describe_table). Claude Code: `claude mcp add jevql -- jevql mcp`.
- A shared HTTP node: https://jevql.fly.dev/docs/deploy/. API spec at `/openapi.json` on any node.

## 6. Report back

Tell the human: the version installed, which database it is pointed at, the
result of the `--explain`, and one working jev query. Stop there.
