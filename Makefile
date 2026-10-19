.PHONY: dev build test lint migrate seed ingest token user

dev:
	docker compose up --build

build:
	docker build --target production -t calbot:local .

test:
	go test ./...

lint:
	golangci-lint run

migrate:
	docker compose run --rm cli migrate

seed:
	docker compose run --rm cli seed

ingest:
	docker compose run --rm cli ingest $(SOURCE)

# Issue an API token, printed once. Usage: make token USER=1
token:
	docker compose run --rm cli token issue $(USER)

user:
	docker compose run --rm cli user create
