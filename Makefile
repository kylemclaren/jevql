.PHONY: build test golden
build:
	CGO_ENABLED=1 go build -o bin/jevpsql ./cmd/jevpsql
test:
	go test ./...
golden:
	go test ./internal/rewrite/ -update
