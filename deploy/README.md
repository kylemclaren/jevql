# Deploying a jevql node

A node is `jevql serve` in a container: one HTTP API (and MCP endpoint) in
front of one Postgres, with a shared answer cache, protected by a bearer token.

## Fly.io

```bash
cp deploy/fly.toml fly.toml && sed -i 's/^app = .*/app = "my-jevql"/' fly.toml
fly apps create my-jevql
fly volumes create jevql_data --size 1 --region iad
fly secrets set DATABASE_URL='postgres://user:pass@host:5432/db' \
                TYPESAFE_API_KEY='tsk_...' \
                JEVQL_TOKEN="$(openssl rand -hex 32)"
fly deploy
```

Fly terminates TLS at its edge, so the token only ever travels over HTTPS.
The volume keeps the answer cache across deploys.

## Anywhere else

```bash
docker build -f deploy/Dockerfile -t jevql .
docker run -p 8080:8080 -v jevql_data:/data \
  -e DATABASE_URL=... -e TYPESAFE_API_KEY=... -e JEVQL_TOKEN=... jevql
```

Put it behind TLS. `jevql serve` refuses to listen on a non-loopback address
without a token unless you pass `--insecure`.

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
