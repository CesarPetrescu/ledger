.PHONY: build test test-integration test-stack lint up down reindex

build:
	mkdir -p bin
	go build -o bin/ledger-auth ./cmd/ledger-auth
	go build -o bin/ledger-mcp ./cmd/ledger-mcp
	go build -o bin/ledger-index ./cmd/ledger-index

test:
	go test ./...

test-integration:
	go test -tags=integration -count=1 ./...

test-stack:
	go test -tags=stack -count=1 ./...

lint:
	go vet ./...

up:
	docker compose up -d --build

down:
	docker compose down

reindex:
	docker compose run --rm ledger-index reindex
