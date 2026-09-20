.PHONY: dev build test lint migrate seed ingest

dev:
	docker compose -f compose.yaml -f compose.dev.yaml up --build

build:
	docker build --target production -t calbot:local .

test:
	go test ./...

lint:
	golangci-lint run

migrate:
	docker compose up -d api

seed:
	docker compose run --rm api seed

ingest:
	docker compose run --rm api ingest $(SOURCE)
