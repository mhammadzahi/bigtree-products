# CLAUDE.md

Staff-only B2B product catalog for Bigtree (staff.bigtree-group.com). Mirrors the
WooCommerce store (bigtree-group.com) into MySQL. It serves category pages with
filters (a port of the live site's filter engine) and a search page, plus a
Google Sheets → PDF quotation generator.

Stack: Go 1.25 (go.mod), Gin, `go-sql-driver/mysql`, **MySQL 8.0** (not MariaDB,
whatever README/schema comments say), server-rendered `html/template` + Vanilla JS.
Google Sheets/Drive APIs + pdfcpu for quotes.

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
go run ./cmd/migrate migrations/002_filters.sql   # schema change for the filters (re-runnable)
./deploy.sh [setup]      # on the Ubuntu server: pull, build, restart systemd unit bt-products
go vet ./...
go test ./internal/catalog/   # filter rule tests (fixtures = the live snippets' self-tests)
```

All tools read DB/API settings from `.env` via `internal/config` (literal values, no
`$` expansion; real env vars win). `DB_DSN` overrides `DB_*` pieces.

## Layout

- `cmd/server/main.go` — route wiring; starts the catalog index holder.
- `internal/catalog` — in-memory product index behind the category pages:
  - `text.go`: `Norm`, keyword scanner, natural sort.
  - `rules.go`: material, colour and Fabrics families; feature roles; Martindale,
    lightfastness and fire-standard parsers.
  - `index.go`: build from the DB, per-top config, tiles, colour versions and swatches.
  - `query.go`: `f_*` URL filters, cards, the filter bar with honest counts.
  - `holder.go`: background rebuild when `sync_state`/products change.
  - `collections.go`: Selection > Collection tree, All Selections, Selection grid,
    leaf collection products.
- `internal/handlers/category.go` — category pages + `/api/v1/category/:slug`
  fragments. `selection.go` — Selection / Collection pages. `funcs.go` holds the
  shared template funcs.
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
  Category pages: `category.html` + `catalog_parts.html` (fragments shared with
  the API), `static/js/category.js`, `static/css/category.css`. Selection pages:
  `selection.html` + `static/js/selection.js`. `static/js/cards.js` holds the card
  extras (hover image, download) and is loaded on every page with cards.
- `migrations/` — incremental schema changes applied with `cmd/migrate`.
- Root `*.go` scripts (`fill_prodcut.go`, `import_csv.go`, `add_user.go`,
  `dump_sample.go`) carry `//go:build ignore`; run each with `go run <file>`.

## Routes

Public: `GET /` (→ /categories), `GET|POST /login`, `POST /logout`.

Auth (cookie `bt_session`). No registration, no roles.
- `GET /categories`: the 6 top-level tiles.
- `GET /category/:slug`: a tile page or a leaf grid with filters.
- `GET /api/v1/category/:slug?f_...[&page=N|&upto=N]`: `{count, bar, grid, more, page}`.
- `GET /selections[?filters=a,b&f_out=1]`: All Selections.
- `GET /collection/:slug[?f_out=1]`: a Selection grid (the term has children) or a
  leaf collection's products.
- `GET /products` + `GET /api/v1/products`: the older search page across every product.
- `GET /product/:slug`, `POST /product/:slug/quote` (PDF, or `?format=json` → sheet URL).

## Category and Selection pages (port of the live-site filters)

Spec: `bigtree_filters_and_product_cards_platform_neutral_brief.md`. Source rules:
the snippets in `BigTree_Filters_Architecture_And_Snippets_*.md` (Data Layer v5,
Tile Index v12, Leaf Grid v14, Fabrics v12, Add-on Categories). Annasta filters
are excluded on purpose.

- **Index:** built in memory at startup (about 1–2 min over the WAN; pages answer 503
  until then). It is rebuilt in the background when the sync signature changes; the
  old index serves meanwhile. Counts and options never use live SQL.
- **Tops:** `furniture`, `floor-coverings`, `fabrics`, `wallcoverings`, `leathers`,
  `lighting`. Per-top rules live at the top of `index.go`:
  - `leathers` and `lighting` are flat (every product at once, Type as filter).
  - In `floor-coverings`, a single child skips its level.
  - Fabrics has 5 rule-based tiles; `drapery-fabrics` and `upholstery-fabrics` load 60 cards at a time.
- **URL state:** `f_out`, `f_flame`, `f_style`, `f_type`, `f_mat`, `f_col`, `f_proj`,
  `f_app`, `f_sel`, `f_mart`, `f_light`, `f_fire`. Filters are AND across facets and
  OR within one. Each option count is the number of cards picking it would show.
- **Colour versions** (`related_series_products`, hidden finishes that share category
  and collection) become cards only under a Color filter. **Swatches** roll their
  colours up into the lead.
- **Selection pages** (#181/#212 rules, in `collections.go`). They filter in the
  browser over the rendered cards. No card shows a count.
  - **All Selections:** root collections with products. Department pills are AND (`?filters=`).
  - **Selection grid:** a term with ≤3 children, one of which has its own children,
    unpacks those folders. Collections whose products are all under a "Finishes"
    category are hidden. Pill depth is adaptive (level 1 if ≥3 departments, else
    level 2 or 3). Only pills carried by a shown card appear. Pills are single-select.
  - **Leaf collection:** products directly on the term. Child-category pills appear
    only when there are more than one.
  - The Outdoor switch is a product flag (the `pa_features` Outdoor term). When on, it
    adds `f_out=1` to every card link, so it carries into the next page.
- **Not built:** the card's "+" enquiry-list button (left out on purpose), and
  per-country hiding (also set aside on the live site).

## Data model (schema.sql)

- `products` — `id` = WooCommerce post ID; `parent_id` set for variations.
  Catalog lists only `parent_id IS NULL`. `slug` is UNIQUE.
  `status` (publish|private), `catalog_visibility` (search/hidden = hidden from the
  catalog: colour versions and swatches), and `gallery` (JSON array of the images after
  the first; the first is the card's hover image).
- `taxonomies` — free-form `type`:
  - `category`: hierarchical via `parent_id`.
  - `collection`: the WordPress Selection > Collection tree, ids = WP term ids.
  - `tag`: WC product tags.
  - `brand` and `pa_*` attributes.

  UNIQUE(`type`, `slug`). `count` is a cached product count.
- `product_taxonomy` — junction. `product_meta` — ACF/custom fields, shown raw on
  the product page.

Search page semantics (`/products`, models/product.go; the category pages use `internal/catalog`):
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
- **Collections:** the WooCommerce API does not return the `collection` taxonomy.
  The sync reads the term tree from `wp/v2/collection`, which is public. It reads each
  product's links from `wp/v2/product?include=...&_fields=id,collection`. Private
  products need a WordPress Application Password in `.env`: `WP_USERNAME` (or
  `WP_USER`) and `WP_APP_PASSWORD`, plus optional `WP_SITE_URL` (defaults to
  `WC_STORE_URL`). Without them, only published products get collections.
- It also stores `status`, `catalog_visibility` and `gallery` (migration 002).
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

- **The DB in `.env` is the remote shared/production MySQL 8.0.** No local DB exists.
  Every script writes to it directly. `make db` wipes all data.
- MySQL 8 has no `ADD COLUMN IF NOT EXISTS`. Make migrations re-runnable with an
  `information_schema` check and `PREPARE`/`EXECUTE` (see `migrations/002_filters.sql`).
  The isolation variable is `transaction_isolation`, not `tx_isolation`.
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
