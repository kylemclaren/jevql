# jevql node: `jevql serve` for any Postgres, behind a bearer token.
# Build from the repo root:  docker build -f deploy/Dockerfile -t jevql .
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=1 go build -trimpath \
    -ldflags "-s -w -X github.com/kylemclaren/jevql/internal/app.version=${VERSION}" \
    -o /out/jevql ./cmd/jevql

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* \
    && useradd --system --create-home --home-dir /data --uid 10001 jevql
COPY --from=build /out/jevql /usr/local/bin/jevql
USER jevql
WORKDIR /data
ENV JEVQL_LISTEN=0.0.0.0:8080 \
    JEVQL_CACHE=/data/cache.db \
    XDG_CONFIG_HOME=/data/.config
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD ["jevql", "--version"]
# DATABASE_URL, TYPESAFE_API_KEY and JEVQL_TOKEN come from the environment / secrets.
ENTRYPOINT ["jevql", "serve"]
