# Bigtree Products

A production-ready B2B storefront that replicates a highly-customised WooCommerce
catalog's browsing, filtering and presentation architecture — rebuilt on **Go +
Gin + MariaDB** with a server-rendered, Vanilla-JS frontend. The schema is
designed to ingest/mirror an existing WooCommerce database without the runtime
cost of the WordPress EAV model.

## Stack

| Layer     | Choice                                                        |
|-----------|--------------------------------------------------------------|
| Backend   | Go 1.22+, Gin, `go-sql-driver/mysql`, `x/crypto/bcrypt`      |
| Database  | MariaDB 10.11+                                                |
| Frontend  | Gin `html/template` (SSR) + CSS grid/flexbox + Vanilla JS    |

## Project layout

```
cmd/server/          program entrypoint + route wiring
internal/config/     env-driven configuration
internal/database/   pooled MariaDB connection
internal/models/     users, sessions, taxonomies, products (query engine)
internal/handlers/   auth, catalog, JSON API, auth middleware
templates/           login, register, catalog, product, 404
static/css, static/js
schema.sql           DDL (WooCommerce-mirroring, de-normalised)
seed.sql             realistic sample data + test admin
```

## Quick start

```bash
# 1. Dependencies
go mod tidy                 # or: make deps

# 2. Configure the database connection in .env (DB_HOST, DB_USER, DB_PASS, ...)

# 3. Load the schema + sample data. These run a small Go migrator that reads
#    .env and connects itself — no mysql client, no password prompt.
make db
make seed

# 4. Run
make run                    # http://localhost:8080
```

All DB access — server and migrations alike — is driven entirely by `.env`
(`cmd/migrate` reuses the same config loader as the server).

**Test login:** `admin@bigtree-group.com` / `password` (change it).

## Users are provisioned manually

Self-service registration is disabled — accounts are inserted directly into the
`users` table. Generate a bcrypt hash + a ready-to-run INSERT with the helper:

```bash
go run ./cmd/hashpw 'someone@bigtree-group.com' 'their-password' buyer
# prints the bcrypt hash and an INSERT INTO users (...) VALUES (...); statement
```

Paste the printed statement into your SQL client. `role` is `buyer` or `admin`.

## How the WooCommerce mapping works

| WooCommerce                              | Here                                  |
|------------------------------------------|---------------------------------------|
| `wp_posts` (product / product_variation) | `products` (`parent_id` = post_parent)|
| `wp_terms` + `wp_term_taxonomy`          | `taxonomies` (`type` = the taxonomy)  |
| `wp_term_relationships`                  | `product_taxonomy` junction           |
| `wp_postmeta`                            | `product_meta` (narrow key/value)     |

Relational, filterable attributes (categories, collections, `pa_*`) are promoted
into real indexed rows so the catalog query is plain SQL with covering indexes
instead of chained meta joins.

## Catalog query engine

One filter → SQL builder (`internal/models/product.go`) backs both the SSR page
(`GET /products`) and the async API (`GET /api/v1/products`). Supported params:

```
?s=linen            full/partial match on title OR sku
?category=slug      single category (hierarchical-aware)
?collection=slug    single B2B collection
?pa_color=slug      repeatable attribute facet (AND across facets, OR within)
?pa_composition=…   repeatable
?pa_application=…    repeatable
?pa_size=…          repeatable
?orderby=price_asc  whitelisted: price_asc|price_desc|title_asc|title_desc|newest
?page=2             LIMIT/OFFSET pagination + matching COUNT
```

Each facet compiles to a parameterised `EXISTS` sub-query, so filters combine
safely and injection-free. Card collection tags are hydrated in one batched
query (no N+1). Sort keys are whitelisted server-side.

## Security

- Passwords hashed with **bcrypt**.
- Sessions are opaque random tokens stored server-side (`sessions` table); the
  cookie is **HttpOnly**, **SameSite=Strict**, and **Secure** in production.
- Auth middleware gates `/products`, `/product/:slug` and the JSON API.
- All SQL is parameterised; sort keys and facet types are whitelisted.
- Client-side rendering escapes every field before insertion into the DOM.

## Routes

| Method | Path                  | Auth | Purpose                    |
|--------|-----------------------|------|----------------------------|
| GET    | `/login`              | no   | login page                 |
| POST   | `/login`              | no   | authenticate               |
| POST   | `/logout`             | no   | revoke session             |
| GET    | `/products`           | yes  | SSR catalog                |
| GET    | `/product/:slug`      | yes  | product permalink          |
| GET    | `/api/v1/products`    | yes  | JSON for async filtering   |
