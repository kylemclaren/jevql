# Deploying a jevql node

A node is `jevql serve` in a container: one HTTP API (and MCP endpoint) in
front of one Postgres, with a shared answer cache, protected by a bearer token.

## Fly.io

```bash
fly launch --config deploy/fly.toml --no-deploy   # name, region, app and volume
fly secrets set DATABASE_URL='postgres://user:pass@host:5432/db' \
                TYPESAFE_API_KEY='tsk_...' \
                JEVQL_TOKEN="$(openssl rand -hex 32)"
fly deploy
```

Fly terminates TLS at its edge, so the token only ever travels over HTTPS.
The volume declared in the config keeps the answer cache across deploys.

## Anywhere else

```bash
docker build -f deploy/Dockerfile -t jevql .
docker run -p 8080:8080 -v jevql_data:/data \
  -e DATABASE_URL=... -e TYPESAFE_API_KEY=... -e JEVQL_TOKEN=... jevql
```

Put it behind TLS. `jevql serve` refuses to listen on a non-loopback address
without a token unless you pass `--insecure`.

## Open node

For a demo you can drop the token: set `JEVQL_INSECURE=1`, and add
`JEVQL_CORS=<origins>` (browser callers), `JEVQL_RATE_LIMIT=<per-minute per IP>`
and `JEVQL_MAX_ROWS=<n>` so nobody can run up the bill. Use a read-only database
role. The public playground at https://jevql.fly.dev/playground is set up this way.

## Talking to it

```bash
curl -H "Authorization: Bearer $JEVQL_TOKEN" https://my-jevql.fly.dev/v1/health
curl -H "Authorization: Bearer $JEVQL_TOKEN" -H "Content-Type: application/json" \
  -d "{\"sql\":\"SELECT name FROM people WHERE jev(people, 'could work from home')\"}" \
  https://my-jevql.fly.dev/v1/query
```

SDKs: `new Jevql({ url: "https://my-jevql.fly.dev", token })` and
`Jevql(url=..., token=...)`. MCP clients: `https://my-jevql.fly.dev/mcp`
with the same bearer token. Full API: `https://my-jevql.fly.dev/openapi.json`.
