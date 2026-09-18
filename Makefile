.PHONY: build test golden
build:
	CGO_ENABLED=1 go build -o bin/jevql ./cmd/jevql
test:
	go test ./...
golden:
	go test ./internal/rewrite/ -update
