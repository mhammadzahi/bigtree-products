# CLAUDE.md

Staff-only B2B product catalog for Bigtree (staff.bigtree-group.com). Mirrors the
WooCommerce store (bigtree-group.com) into MariaDB and serves a filterable catalog
plus a Google Sheets → PDF quotation generator.

Stack: Go 1.25 (go.mod), Gin, `go-sql-driver/mysql`, MariaDB 10.11+, server-rendered
`html/template` + Vanilla JS. Google Sheets/Drive APIs + pdfcpu for quotes.

## Commands

```bash
make run                 # go run ./cmd/server  (LISTEN_ADDR, default :8080)
make build               # CGO_ENABLED=0 -> bin/server
make db                  # go run ./cmd/migrate schema.sql  — DROPS AND RECREATES ALL TABLES
make import-test         # go run fill_prodcut.go test  (first 1000 WC products)
make import              # go run fill_prodcut.go full
make sync                # ./sync.sh incremental  (changes since last sync)
make sync-reconcile      # ./sync.sh reconcile    (+ delete products gone from WC)
go run add_user.go <email> '<password>'      # create user / reset password
go run dump_sample.go [n | product <id>]     # inspect raw WooCommerce JSON
go run import_csv.go [--dry-run|--purge] a.csv b.csv   # import WC-format CSVs
./deploy.sh [setup]      # on the Ubuntu server: pull, build, restart systemd unit bt-products
go vet ./...             # no test suite exists
```

All tools read DB/API settings from `.env` via `internal/config` (literal values, no
`$` expansion; real env vars win). `DB_DSN` overrides `DB_*` pieces.

## Layout

- `cmd/server/main.go` — route wiring, template funcs (`contains`, `add`, `sub`,
  `safeHTML`, `label`).
- `cmd/migrate` — applies `.sql` files with `multiStatements=true`.
- `cmd/hashpw` — bcrypt helper.
- `internal/config` — `.env` loader + `Config` (DSN, ListenAddr, SecureCookie, Google ids).
- `internal/database` — pooled connection (25 open / 10 idle).
- `internal/models` — users, sessions, taxonomies, and the catalog query engine
  (`product.go`: `buildWhere`, `LoadFacets`, `QueryProducts`, `GetProductFull`).
- `internal/handlers` — auth, catalog (SSR + JSON API), quotation, `RequireAuth`.
- `internal/services/quotation.go` — copies the master Sheets template, fills fixed
  cells (constants at top of file), exports PDF, stamps product image with pdfcpu.
- `templates/*.html`, `static/js/app.js`, `static/css/style.css` — frontend.
- Root `*.go` scripts (`fill_prodcut.go`, `import_csv.go`, `add_user.go`,
  `dump_sample.go`) carry `//go:build ignore`; run each with `go run <file>`.

## Routes

Public: `GET /` (→ /products), `GET|POST /login`, `POST /logout`.
Auth (cookie `bt_session`): `GET /products`, `GET /product/:slug`,
`POST /product/:slug/quote` (PDF, or `?format=json` → sheet URL),
`GET /api/v1/products` (JSON page + facets). No registration, no roles.

## Data model (schema.sql)

- `products` — `id` = WooCommerce post ID; `parent_id` set for variations.
  Catalog lists only `parent_id IS NULL`. `slug` is UNIQUE.
- `taxonomies` — free-form `type`: `category` (hierarchical via `parent_id`),
  `collection` (B2B "X Selection", from WC tags), `brand`, `pa_*` attributes.
  UNIQUE(`type`, `slug`). `count` is a cached product count.
- `product_taxonomy` — junction. `product_meta` — ACF/custom fields, shown raw on
  the product page.

Catalog semantics (models/product.go):
- Products link to their **leaf** category. Sidebar "Collections" = leaf categories;
  "Categories" = parents of those leaves.
- Facets match taxonomy **names**, not slugs, so duplicate-named terms merge.
- Facet types in the UI: `brand`, `pa_application`, `pa_color`, `pa_composition`,
  `pa_features`. Other `pa_*` types are stored but not filterable.
- Every selected value adds an `EXISTS` sub-query (AND); categories are OR'd.
- N/A-like values are hidden from facets (`isNA`).
- Sort keys are whitelisted in `orderClauses`.

## Importers and ID ranges

- `fill_prodcut.go` (WooCommerce REST) — ids = real WP ids. It fetches products with
  status `publish` and `private` (`productStatuses`). Private products are included
  because the catalog is staff-only. Modes:
  - `test`: first 1000 products.
  - `full`: everything.
  - `incremental`: only products with `modified_after` = last sync − 15 min. It also
    deletes products moved to trash, draft or pending.
  - `reconcile`: incremental, plus deleting local products whose id is gone from
    WC. It refuses to delete more than half of the catalog.
- The last sync time is stored in table `sync_state` (key `woocommerce_last_sync`,
  RFC 3339 UTC). `full` writes it too. `incremental` fails until a sync time exists.
- Writes are batched (100 products per statement set) and run in 6 workers.
- Attribute terms with no WP id get synthetic ids in
  `[10_000_000_000, 20_000_000_000)`. Existing terms keep their id, and new ones
  start above the current maximum.
- `sync.sh` is the cron wrapper for the VPS. It takes a `flock` lock, rebuilds
  `bin/sync` when the source changes, and logs to `logs/sync-*.log` (kept 30 days).
  Planned schedule: `incremental` nightly Mon–Sat, `reconcile` on Sunday.
- `import_csv.go` (WooCommerce product-CSV format, e.g. `REVIEW_*_Master Product
  List*.csv`) — for products not on WooCommerce yet. Product ids start at
  `9_000_000_000` and new term ids start at `20_000_000_000`. Products are matched
  by SKU. A SKU that already has a real WC id is skipped. The whole run is one
  transaction, so `--dry-run` rolls everything back.
- Antiga (SKUs `BGTQ.ANT.*`) was CSV-imported on 2026-11-20. On 2026-11-24 the
  product tables were emptied and fully re-synced from WooCommerce, where Antiga
  now exists as private products. No CSV-imported rows remain.
- If CSV-imported products later appear on WooCommerce, run `import_csv.go --purge`
  with the same CSVs **before** the next `fill_prodcut.go` run. Otherwise the WC
  copy (new id, same slug) collides on UNIQUE(slug).

## Gotchas

- **The DB in `.env` is the remote shared/production MariaDB.** No local DB exists.
  Every script writes to it directly. `make db` wipes all data.
- WAN latency is high (~100+ ms/query). Avoid per-row round trips. Preload into
  memory and batch inserts. Never recount `taxonomies.count` over the whole table
  inside a long transaction, because it holds locks and blocks other writers.
- A killed `go run` can leave an open server-side transaction holding locks.
  Error `1205 Lock wait timeout` points to this. Find it in
  `information_schema.INNODB_TRX` before you retry.
- Scripts that import `internal/...` must live inside the module, for example in the
  repo root with `//go:build ignore`. They cannot run from /tmp.
- The DB server clock is UTC+4. `trx_started` and other server timestamps are
  shifted relative to local UTC.
- Descriptions are rendered with `safeHTML`. Only store first-party content there.
- README is partly stale. It lists a `pa_size` filter, slug-based filter params and
  Go 1.22. The code uses name-based params, has no `pa_size` filter and needs Go 1.25.
  Trust the code.
- Secrets: `.env` and `service-account.json` are gitignored. Never commit them, and
  never echo their values.
- Commit messages in history are just `.`. No convention is enforced.
