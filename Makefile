# All targets read DB connection settings from .env via the Go config loader —
# no local mysql client, no password prompt.

.PHONY: deps db schema import-test import run build clean

## deps: download Go module dependencies
deps:
	go mod tidy

## db: load schema.sql into the database (connection from .env)
db schema:
	go run ./cmd/migrate schema.sql

## import-test: import the first 1000 products from WooCommerce
import-test:
	go run fill_prodcut.go test

## import: import ALL products from WooCommerce
import:
	go run fill_prodcut.go full

## run: start the server
run:
	go run ./cmd/server

## build: compile a static binary into ./bin/
build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server

clean:
	rm -rf bin
