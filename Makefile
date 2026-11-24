# All targets read DB connection settings from .env via the Go config loader —
# no local mysql client, no password prompt.

.PHONY: deps db schema import-test import sync sync-reconcile run build clean

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

## sync: incremental WooCommerce sync (changes since last sync)
sync:
	./sync.sh incremental

## sync-reconcile: incremental sync + delete products gone from WooCommerce
sync-reconcile:
	./sync.sh reconcile

## add my user
add-user-basil:
	go run add_user.go basil@bigtree-group.com 'basil@BT!59f'

add-user-shafiq:
	go run add_user.go shafiq@bigtree-group.com 'w3b@BT'

## run: start the server
run:
	go run ./cmd/server

## build: compile a static binary into ./bin/
build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server

clean:
	rm -rf bin
