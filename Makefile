# All targets read DB connection settings from .env via the Go config loader —
# no local mysql client, no password prompt.

.PHONY: deps db schema seed run build clean

## deps: download Go module dependencies
deps:
	go mod tidy

## db: load schema.sql into the database (connection from .env)
db schema:
	go run ./cmd/migrate schema.sql

## seed: load sample data — run after `make db`
seed:
	go run ./cmd/migrate seed.sql

## run: start the server
run:
	go run ./cmd/server

## build: compile a static binary into ./bin/
build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server

clean:
	rm -rf bin
