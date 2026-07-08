DB_NAME ?= bigtree
MYSQL   ?= mysql

.PHONY: deps db seed run build clean

## deps: download Go module dependencies
deps:
	go mod tidy

## db: create the database and load the schema
db:
	$(MYSQL) -e "CREATE DATABASE IF NOT EXISTS $(DB_NAME) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
	$(MYSQL) $(DB_NAME) < schema.sql

## seed: load sample data (run after `make db`)
seed:
	$(MYSQL) $(DB_NAME) < seed.sql

## run: start the server (reads .env-style vars from the environment)
run:
	go run ./cmd/server

## build: compile a static binary into ./bin/
build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server

clean:
	rm -rf bin
