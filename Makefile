.PHONY: build test test-integration test-stack lint up down reindex test-race test-integration-race frontend-verify images

build:
	mkdir -p bin
	go build -o bin/ledger-auth ./cmd/ledger-auth
	go build -o bin/ledger-mcp ./cmd/ledger-mcp
	go build -o bin/ledger-index ./cmd/ledger-index
	go build -o bin/ledger-admin ./cmd/ledger-admin

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

test-integration:
	go test -tags=integration -count=1 ./...

test-integration-race:
	go test -tags=integration -race -count=1 ./...

test-stack:
	go test -tags=stack -count=1 ./...

lint:
	go vet ./...

frontend-verify:
	cd frontend && npm ci && npm run verify

images:
	docker build --build-arg CMD=ledger-admin -t ledger-admin-local .
	docker build -t ledger-frontend-local ./frontend

up:
	docker compose up -d --build

down:
	docker compose down

reindex:
	docker compose run --rm ledger-index reindex
