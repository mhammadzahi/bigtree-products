//go:build ignore

// fill_prodcut.go — mirrors the WooCommerce catalog into the local `products` /
// `taxonomies` / `product_taxonomy` / `product_meta` tables.
//
// Modes:
//
//	go run fill_prodcut.go               # test: first 1000 products (default)
//	go run fill_prodcut.go test          # same as above, explicit
//	go run fill_prodcut.go full          # ALL products, then records the sync time
//	go run fill_prodcut.go incremental   # only products changed since the last sync
//	go run fill_prodcut.go reconcile     # incremental + delete products that no
//	                                     # longer exist on WooCommerce
//
// For scheduled runs use sync.sh (lock + logs), e.g. nightly `incremental` and
// weekly `reconcile`.
//
// Reads everything from .env (same loader the server uses):
//
//	DB_*                       — database connection
//	WC_STORE_URL               — e.g. https://bigtree-group.com
//	WC_CONSUMER_KEY  (ck_...)  — WooCommerce REST API key
//	WC_CONSUMER_SECRET (cs_...) — WooCommerce REST API secret
//
// Statuses: `publish` and `private` products are imported (staff-only catalog).
// In incremental/reconcile mode, products moved to trash/draft/pending since the
// last sync are deleted locally.
//
// The last successful sync time lives in the `sync_state` table (created on
// demand). Incremental runs fetch `modified_after = last sync - syncOverlap`.
// `full` records the time too, so it can seed incremental runs.
//
// Idempotent: everything is upserted. Writes are batched (chunkSize products
// per statement set) because the DB is remote and round trips dominate runtime.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bigtree-products/internal/config"
	"bigtree-products/internal/database"
)

const (
	// Concurrency for the write phase. The DB pool allows 25 connections.
	productWorkers   = 6
	variationWorkers = 8

	// Products per batched write. Meta rows are split further (metaBatch) to
	// keep each statement well under max_allowed_packet.
	chunkSize = 100
	metaBatch = 1000

	// Synthetic ids for attribute terms (no WP id in the payload) live in
	// [attrIDBase, attrIDLimit). import_csv.go allocates from attrIDLimit up.
	attrIDBase  int64 = 10_000_000_000
	attrIDLimit int64 = 20_000_000_000

	// Products created by import_csv.go; never deleted by reconcile.
	csvProductIDBase int64 = 9_000_000_000

	// Overlap subtracted from the last sync time, covering clock skew and
	// products saved while the previous run was in progress.
	syncOverlap = 15 * time.Minute

	syncStateKey = "woocommerce_last_sync"
)

// productStatuses are the WooCommerce post statuses imported. The catalog is
// staff-only, so private products (e.g. not-yet-public brands) are included.
var productStatuses = []string{"publish", "private"}

// removedStatuses: a product modified into one of these since the last sync is
// deleted locally (incremental/reconcile modes).
var removedStatuses = []string{"trash", "draft", "pending"}

// runWorkers fans `items` out across `workers` goroutines, stopping on the first
// error. Order is irrelevant here — every item's DB writes are independent.
func runWorkers[T any](items []T, workers int, fn func(T) error) error {
	if workers < 1 {
		workers = 1
	}
	ch := make(chan T)
	stop := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var firstErr error

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range ch {
				if err := fn(it); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					once.Do(func() { close(stop) })
					return
				}
			}
		}()
	}

feed:
	for _, it := range items {
		select {
		case ch <- it:
		case <-stop:
			break feed
		}
	}
	close(ch)
	wg.Wait()
	return firstErr
}

// chunks splits s into slices of at most n items.
func chunks[T any](s []T, n int) [][]T {
	var out [][]T
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}

func placeholders(n int, group string) string {
	return strings.TrimSuffix(strings.Repeat(group+",", n), ",")
}

func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// clean trims and decodes HTML entities so names/titles store as plain text
// ("Nails &amp; Buttons" -> "Nails & Buttons"), rendered correctly by templates.
func clean(s string) string {
	return html.UnescapeString(strings.TrimSpace(s))
}

// ---------------------------------------------------------------------------
// WooCommerce REST payloads (only the fields we consume)
// ---------------------------------------------------------------------------

type wooTerm struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type wooImage struct {
	Src string `json:"src"`
}

type wooAttr struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Slug    string   `json:"slug"`
	Options []string `json:"options"`
}

type wooProduct struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
	SKU              string     `json:"sku"`
	Type             string     `json:"type"` // simple | variable | ...
	Price            string     `json:"price"`
	Description      string     `json:"description"`
	ShortDescription string     `json:"short_description"`
	StockStatus      string     `json:"stock_status"` // instock | outofstock | onbackorder
	Parent           int64      `json:"parent_id"`
	Categories       []wooTerm  `json:"categories"`
	Tags             []wooTerm  `json:"tags"`   // used as B2B "collections"
	Brands           []wooTerm  `json:"brands"` // WooCommerce Brands taxonomy
	Images           []wooImage `json:"images"`
	Attributes       []wooAttr  `json:"attributes"`
	// ACF fields (and every other custom field) surface here.
	MetaData []wooMeta `json:"meta_data"`
}

type wooMeta struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// wooVariation is the shape returned by /products/{id}/variations.
type wooVariation struct {
	ID          int64    `json:"id"`
	SKU         string   `json:"sku"`
	Price       string   `json:"price"`
	StockStatus string   `json:"stock_status"`
	Image       wooImage `json:"image"`
	Attributes  []struct {
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		Option string `json:"option"`
	} `json:"attributes"`
}

// ---------------------------------------------------------------------------
// Taxonomy accumulation
// ---------------------------------------------------------------------------

type taxo struct {
	id   int64
	typ  string
	slug string
	name string
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// attrType maps a WooCommerce attribute to a taxonomy `type` string. Since the
// column is a free VARCHAR, every attribute is kept — global attributes stay
// `pa_*`, custom (product-level) attributes get a `pa_<name>` slug, and any
// "collection" variant is normalised to `collection`.
func attrType(a wooAttr) string {
	s := strings.ToLower(a.Slug)
	if s == "" {
		s = "pa_" + slugify(a.Name)
	}
	switch s {
	case "pa_collection", "pa_collections", "collection", "collections":
		return "collection"
	}
	return s
}

// ---------------------------------------------------------------------------
// Importer
// ---------------------------------------------------------------------------

// testModeLimit caps how many products the "test" mode imports.
const testModeLimit = 1000

type importer struct {
	db          *sql.DB
	client      *http.Client
	base        string // https://store/wp-json/wc/v3
	key         string
	secret      string
	mode        string
	maxProducts int    // 0 = unlimited; >0 = cap (test mode)
	since       string // modified_after (GMT, ISO 8601); "" = everything

	taxByKey    map[string]*taxo // "type|slug" -> taxo collected this run
	existingTax map[string]int64 // "type|slug" -> id already in the DB
	attrSeq     int64            // last synthetic attribute id handed out

	touchMu sync.Mutex
	touched map[int64]bool // taxonomy ids whose cached count must be refreshed
}

func main() {
	mode := "test"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}
	maxProducts := 0
	switch mode {
	case "test", "":
		mode, maxProducts = "test", testModeLimit
	case "full", "all", "normal":
		mode = "full"
	case "incremental", "reconcile":
	default:
		log.Fatalf("unknown mode %q — use test | full | incremental | reconcile", mode)
	}

	cfg := config.Load() // loads .env, builds DSN

	store := strings.TrimRight(os.Getenv("WC_STORE_URL"), "/")
	key := os.Getenv("WC_CONSUMER_KEY")
	secret := os.Getenv("WC_CONSUMER_SECRET")
	if store == "" || key == "" || secret == "" {
		log.Fatal("missing WC_STORE_URL / WC_CONSUMER_KEY / WC_CONSUMER_SECRET in .env")
	}

	db, err := database.Connect(cfg.DSN)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()

	imp := &importer{
		db:          db,
		client:      &http.Client{Timeout: 120 * time.Second},
		base:        store + "/wp-json/wc/v3",
		key:         key,
		secret:      secret,
		mode:        mode,
		maxProducts: maxProducts,
		taxByKey:    map[string]*taxo{},
		existingTax: map[string]int64{},
		touched:     map[int64]bool{},
	}
	ctx := context.Background()
	startedAt := time.Now().UTC()

	if err := imp.ensureSyncState(ctx); err != nil {
		log.Fatalf("sync_state: %v", err)
	}
	if mode == "incremental" || mode == "reconcile" {
		last, err := imp.lastSync(ctx)
		if err != nil {
			log.Fatalf("read last sync: %v", err)
		}
		if last.IsZero() {
			log.Fatal("no previous sync recorded — run `full` once first")
		}
		imp.since = last.Add(-syncOverlap).Format("2006-01-02T15:04:05")
		log.Printf("running in %s mode (changes since %s UTC)", strings.ToUpper(mode), imp.since)
	} else {
		log.Printf("running in %s mode", strings.ToUpper(mode))
	}

	if err := imp.run(ctx); err != nil {
		log.Fatalf("import failed: %v", err)
	}

	if mode != "test" {
		if err := imp.setLastSync(ctx, startedAt); err != nil {
			log.Fatalf("record sync time: %v", err)
		}
		log.Printf("recorded sync time %s", startedAt.Format(time.RFC3339))
	}
	log.Printf("import complete in %s", time.Since(startedAt).Round(time.Second))
}

func (imp *importer) incremental() bool { return imp.since != "" }

func (imp *importer) run(ctx context.Context) error {
	// The category tree is small and fetched whole every run, so new or moved
	// categories are always present before products reference them.
	if err := imp.importCategoryTree(ctx); err != nil {
		return fmt.Errorf("categories: %w", err)
	}
	if err := imp.loadExistingTaxonomies(ctx); err != nil {
		return fmt.Errorf("load taxonomies: %w", err)
	}

	products, err := imp.fetchProducts(ctx)
	if err != nil {
		return err
	}
	log.Printf("fetched %d products from WooCommerce", len(products))

	if len(products) > 0 {
		if err := imp.writeProducts(ctx, products); err != nil {
			return err
		}
	}

	if imp.incremental() {
		if err := imp.deleteRemoved(ctx); err != nil {
			return fmt.Errorf("delete removed: %w", err)
		}
	}
	if imp.mode == "reconcile" {
		if err := imp.reconcile(ctx); err != nil {
			return fmt.Errorf("reconcile: %w", err)
		}
	}

	return imp.recount(ctx)
}

// writeProducts upserts taxonomies, then products + links + meta in batched
// chunks, then variations of variable products.
func (imp *importer) writeProducts(ctx context.Context, products []wooProduct) error {
	productTaxos := make(map[int64][]string, len(products))
	for _, p := range products {
		productTaxos[p.ID] = imp.collectTaxonomies(p)
	}
	if err := imp.upsertTaxonomies(ctx); err != nil {
		return fmt.Errorf("taxonomies: %w", err)
	}
	authTaxID, err := imp.loadTaxonomyIDs(ctx)
	if err != nil {
		return fmt.Errorf("load taxonomy ids: %w", err)
	}

	var done int64
	if err := runWorkers(chunks(products, chunkSize), productWorkers, func(ps []wooProduct) error {
		if err := imp.writeChunk(ctx, ps, productTaxos, authTaxID); err != nil {
			return err
		}
		if n := atomic.AddInt64(&done, int64(len(ps))); n%1000 < chunkSize {
			log.Printf("  wrote %d/%d products", n, len(products))
		}
		return nil
	}); err != nil {
		return err
	}
	log.Printf("wrote %d products", len(products))

	var variable []wooProduct
	for _, p := range products {
		if p.Type == "variable" {
			variable = append(variable, p)
		}
	}
	if len(variable) > 0 {
		log.Printf("importing variations for %d variable products", len(variable))
		_ = runWorkers(variable, variationWorkers, func(parent wooProduct) error {
			if err := imp.importVariations(ctx, parent); err != nil {
				log.Printf("warning: variations for %d (%s): %v", parent.ID, parent.Slug, err)
			}
			return nil // don't abort the whole import over one parent's variations
		})
	}
	return nil
}

// ---------------------------------------------------------------------------
// sync_state
// ---------------------------------------------------------------------------

func (imp *importer) ensureSyncState(ctx context.Context) error {
	_, err := imp.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS sync_state (
		  name  VARCHAR(64)  NOT NULL,
		  value VARCHAR(255) NOT NULL,
		  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		  PRIMARY KEY (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`)
	return err
}

// lastSync returns the recorded sync time (UTC), or zero when none exists.
// Stored as an RFC 3339 string so the DB server's timezone never matters.
func (imp *importer) lastSync(ctx context.Context) (time.Time, error) {
	var v string
	err := imp.db.QueryRowContext(ctx,
		`SELECT value FROM sync_state WHERE name = ?`, syncStateKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, v)
}

func (imp *importer) setLastSync(ctx context.Context, t time.Time) error {
	_, err := imp.db.ExecContext(ctx, `
		INSERT INTO sync_state (name, value) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value)`,
		syncStateKey, t.UTC().Format(time.RFC3339))
	return err
}

// ---------------------------------------------------------------------------
// taxonomies
// ---------------------------------------------------------------------------

// loadExistingTaxonomies indexes every term already in the DB so synthetic
// attribute ids are reused, and new ones start above the current maximum —
// re-runs on a non-empty DB can never hand an existing term's id to another.
func (imp *importer) loadExistingTaxonomies(ctx context.Context) error {
	rows, err := imp.db.QueryContext(ctx, `SELECT id, type, slug FROM taxonomies`)
	if err != nil {
		return err
	}
	defer rows.Close()
	imp.attrSeq = attrIDBase
	for rows.Next() {
		var id int64
		var typ, slug string
		if err := rows.Scan(&id, &typ, &slug); err != nil {
			return err
		}
		imp.existingTax[typ+"|"+slug] = id
		if id >= attrIDBase && id < attrIDLimit && id > imp.attrSeq {
			imp.attrSeq = id
		}
	}
	return rows.Err()
}

// getOrAddTax registers a taxonomy term (deduped by type|slug) and returns its key.
func (imp *importer) getOrAddTax(typ, slug, name string, realID int64) string {
	key := typ + "|" + slug
	if _, ok := imp.taxByKey[key]; ok {
		return key
	}
	id := realID
	if id == 0 {
		if existing, ok := imp.existingTax[key]; ok {
			id = existing
		} else {
			imp.attrSeq++
			id = imp.attrSeq
		}
	}
	imp.taxByKey[key] = &taxo{id: id, typ: typ, slug: slug, name: clean(name)}
	return key
}

// collectTaxonomies extracts every modelled facet from a product.
func (imp *importer) collectTaxonomies(p wooProduct) []string {
	var keys []string

	for _, c := range p.Categories {
		if c.Slug == "" {
			continue
		}
		keys = append(keys, imp.getOrAddTax("category", c.Slug, c.Name, c.ID))
	}
	// Product tags model the B2B "collections" (e.g. "AZ Collection").
	for _, tg := range p.Tags {
		if tg.Slug == "" {
			continue
		}
		keys = append(keys, imp.getOrAddTax("collection", tg.Slug, tg.Name, tg.ID))
	}
	for _, b := range p.Brands {
		if b.Slug == "" {
			continue
		}
		keys = append(keys, imp.getOrAddTax("brand", b.Slug, b.Name, b.ID))
	}
	for _, a := range p.Attributes {
		typ := attrType(a)
		for _, opt := range a.Options {
			slug := slugify(opt)
			if slug == "" {
				continue
			}
			keys = append(keys, imp.getOrAddTax(typ, slug, opt, 0))
		}
	}
	return keys
}

// upsertTaxonomies writes every collected term in chunked multi-row inserts.
// ON DUPLICATE keeps any existing row's id (matched by UNIQUE(type,slug)).
func (imp *importer) upsertTaxonomies(ctx context.Context) error {
	terms := make([]*taxo, 0, len(imp.taxByKey))
	for _, t := range imp.taxByKey {
		terms = append(terms, t)
	}
	for _, c := range chunks(terms, 400) {
		var args []any
		for _, t := range c {
			args = append(args, t.id, t.name, t.slug, t.typ)
		}
		q := `INSERT INTO taxonomies (id, name, slug, type) VALUES ` +
			placeholders(len(c), "(?, ?, ?, ?)") + ` ON DUPLICATE KEY UPDATE name = VALUES(name)`
		if _, err := imp.db.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// loadTaxonomyIDs reads back the authoritative id for every (type,slug).
func (imp *importer) loadTaxonomyIDs(ctx context.Context) (map[string]int64, error) {
	rows, err := imp.db.QueryContext(ctx, `SELECT id, type, slug FROM taxonomies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var typ, slug string
		if err := rows.Scan(&id, &typ, &slug); err != nil {
			return nil, err
		}
		out[typ+"|"+slug] = id
	}
	return out, rows.Err()
}

// importCategoryTree pulls every product category and stores them with their
// parent links, so sub-categories nest correctly. Two batched passes satisfy
// the self-referencing foreign key: rows first, then parent links.
func (imp *importer) importCategoryTree(ctx context.Context) error {
	type wc struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		Parent int64  `json:"parent"`
	}
	var all []wc
	page := 1
	for {
		var batch []wc
		more, err := imp.getJSON(ctx, "/products/categories",
			url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}, &batch)
		if err != nil {
			return err
		}
		all = append(all, batch...)
		if !more || len(batch) == 0 {
			break
		}
		page++
	}

	exists := make(map[int64]bool, len(all))
	for _, c := range all {
		exists[c.ID] = true
	}

	// pass 1: upsert every category (parent untouched)
	for _, ch := range chunks(all, 500) {
		var args []any
		for _, c := range ch {
			args = append(args, c.ID, clean(c.Name), c.Slug)
		}
		if _, err := imp.db.ExecContext(ctx, `
			INSERT INTO taxonomies (id, name, slug, type, parent_id) VALUES `+
			placeholders(len(ch), "(?, ?, ?, 'category', NULL)")+`
			ON DUPLICATE KEY UPDATE name = VALUES(name), slug = VALUES(slug)`, args...); err != nil {
			return err
		}
	}
	// pass 2: set parent links (NULL for roots and for orphan parents that
	// would break the self-referencing foreign key).
	for _, ch := range chunks(all, 500) {
		var args []any
		for _, c := range ch {
			var parent any
			if c.Parent != 0 && exists[c.Parent] {
				parent = c.Parent
			}
			args = append(args, c.ID, clean(c.Name), c.Slug, parent)
		}
		if _, err := imp.db.ExecContext(ctx, `
			INSERT INTO taxonomies (id, name, slug, type, parent_id) VALUES `+
			placeholders(len(ch), "(?, ?, ?, 'category', ?)")+`
			ON DUPLICATE KEY UPDATE parent_id = VALUES(parent_id)`, args...); err != nil {
			return err
		}
	}
	log.Printf("imported %d categories (with hierarchy)", len(all))
	return nil
}

// touch marks taxonomy ids for recount (incremental modes only).
func (imp *importer) touch(ids ...int64) {
	imp.touchMu.Lock()
	for _, id := range ids {
		imp.touched[id] = true
	}
	imp.touchMu.Unlock()
}

// touchLinked marks every taxonomy currently linked to the given products.
func (imp *importer) touchLinked(ctx context.Context, productIDs []int64) error {
	if !imp.incremental() || len(productIDs) == 0 {
		return nil
	}
	rows, err := imp.db.QueryContext(ctx,
		`SELECT DISTINCT taxonomy_id FROM product_taxonomy WHERE product_id IN (`+
			placeholders(len(productIDs), "?")+`)`, int64Args(productIDs)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	imp.touch(ids...)
	return rows.Err()
}

// recount refreshes cached taxonomy counts: all of them after a full/test
// run, only the touched ones after an incremental run.
func (imp *importer) recount(ctx context.Context) error {
	q := `UPDATE taxonomies t SET count = (
			SELECT COUNT(*) FROM product_taxonomy pt
			JOIN products p ON p.id = pt.product_id
			WHERE pt.taxonomy_id = t.id AND p.parent_id IS NULL)`
	if !imp.incremental() {
		_, err := imp.db.ExecContext(ctx, q)
		return err
	}
	ids := make([]int64, 0, len(imp.touched))
	for id := range imp.touched {
		ids = append(ids, id)
	}
	log.Printf("recounting %d taxonomy terms", len(ids))
	for _, c := range chunks(ids, 1000) {
		if _, err := imp.db.ExecContext(ctx, q+` WHERE t.id IN (`+placeholders(len(c), "?")+`)`,
			int64Args(c)...); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// products
// ---------------------------------------------------------------------------

func mapStock(s string) string {
	if s == "instock" {
		return "in_stock"
	}
	return "out_of_stock"
}

func parsePrice(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func nullParent(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func firstImage(imgs []wooImage) string {
	if len(imgs) > 0 {
		return imgs[0].Src
	}
	return ""
}

// upsertProducts writes many products in one multi-row statement.
func (imp *importer) upsertProducts(ctx context.Context, ps []wooProduct) error {
	if len(ps) == 0 {
		return nil
	}
	var args []any
	for _, p := range ps {
		ptype := "simple"
		if p.Type == "variable" {
			ptype = "variable"
		}
		args = append(args, p.ID, p.SKU, clean(p.Name), p.Slug, p.Description, p.ShortDescription,
			parsePrice(p.Price), firstImage(p.Images), mapStock(p.StockStatus), ptype, nullParent(p.Parent))
	}
	_, err := imp.db.ExecContext(ctx, `
		INSERT INTO products
		  (id, sku, title, slug, description, short_description, price, image_url, stock_status, product_type, parent_id)
		VALUES `+placeholders(len(ps), "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")+`
		ON DUPLICATE KEY UPDATE
		  sku=VALUES(sku), title=VALUES(title), slug=VALUES(slug),
		  description=VALUES(description), short_description=VALUES(short_description),
		  price=VALUES(price), image_url=VALUES(image_url),
		  stock_status=VALUES(stock_status), product_type=VALUES(product_type),
		  parent_id=VALUES(parent_id)`, args...)
	return err
}

// writeChunk upserts a batch of products and rebuilds their taxonomy links and
// meta with a handful of statements in total.
func (imp *importer) writeChunk(ctx context.Context, ps []wooProduct, productTaxos map[int64][]string, authID map[string]int64) error {
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	idPH := placeholders(len(ids), "?")

	if err := imp.touchLinked(ctx, ids); err != nil { // old links: counts may drop
		return err
	}
	if err := imp.upsertProducts(ctx, ps); err != nil {
		return fmt.Errorf("products %d..: %w", ids[0], err)
	}

	// links
	if _, err := imp.db.ExecContext(ctx,
		`DELETE FROM product_taxonomy WHERE product_id IN (`+idPH+`)`, int64Args(ids)...); err != nil {
		return err
	}
	var linkArgs []any
	var linked []int64
	for _, p := range ps {
		seen := map[int64]bool{}
		for _, k := range productTaxos[p.ID] {
			id, ok := authID[k]
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			linkArgs = append(linkArgs, p.ID, id)
			linked = append(linked, id)
		}
	}
	if len(linkArgs) > 0 {
		if _, err := imp.db.ExecContext(ctx,
			`INSERT IGNORE INTO product_taxonomy (product_id, taxonomy_id) VALUES `+
				placeholders(len(linkArgs)/2, "(?, ?)"), linkArgs...); err != nil {
			return err
		}
	}
	if imp.incremental() {
		imp.touch(linked...)
	}

	// meta
	if _, err := imp.db.ExecContext(ctx,
		`DELETE FROM product_meta WHERE product_id IN (`+idPH+`)`, int64Args(ids)...); err != nil {
		return err
	}
	var metaArgs []any
	for _, p := range ps {
		metaArgs = append(metaArgs, metaRows(p.ID, p.MetaData)...)
	}
	for _, c := range chunks(metaArgs, metaBatch*3) {
		if _, err := imp.db.ExecContext(ctx,
			`INSERT INTO product_meta (product_id, meta_key, meta_value) VALUES `+
				placeholders(len(c)/3, "(?, ?, ?)"), c...); err != nil {
			return err
		}
	}
	return nil
}

// importVariations pulls a variable product's variations, stores them as child
// products (parent_id = the variable parent) and deletes children that no
// longer exist on WooCommerce.
func (imp *importer) importVariations(ctx context.Context, parent wooProduct) error {
	var children []wooProduct
	page := 1
	for {
		var vs []wooVariation
		more, err := imp.getJSON(ctx,
			fmt.Sprintf("/products/%d/variations", parent.ID),
			url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}, &vs)
		if err != nil {
			return err
		}
		for _, v := range vs {
			opts := make([]string, 0, len(v.Attributes))
			for _, a := range v.Attributes {
				if a.Option != "" {
					opts = append(opts, a.Option)
				}
			}
			suffix := slugify(strings.Join(opts, "-"))
			title := parent.Name
			if len(opts) > 0 {
				title += " — " + strings.Join(opts, ", ")
			}
			slug := parent.Slug
			if suffix != "" {
				slug += "-" + suffix
			}
			img := parent.Images
			if v.Image.Src != "" {
				img = []wooImage{{Src: v.Image.Src}}
			}
			children = append(children, wooProduct{
				ID: v.ID, Name: title, Slug: slug, SKU: v.SKU, Type: "simple",
				Price: v.Price, StockStatus: v.StockStatus, Parent: parent.ID, Images: img,
			})
		}
		if !more || len(vs) == 0 {
			break
		}
		page++
	}

	for _, c := range chunks(children, chunkSize) {
		if err := imp.upsertProducts(ctx, c); err != nil {
			return err
		}
	}
	// Drop variations removed on WooCommerce.
	q := `DELETE FROM products WHERE parent_id = ?`
	args := []any{parent.ID}
	if len(children) > 0 {
		q += ` AND id NOT IN (` + placeholders(len(children), "?") + `)`
		for _, c := range children {
			args = append(args, c.ID)
		}
	}
	_, err := imp.db.ExecContext(ctx, q, args...)
	return err
}

// deleteProducts removes products (variations cascade) and marks their terms
// for recount. CSV-imported products are never touched.
func (imp *importer) deleteProducts(ctx context.Context, ids []int64) (int64, error) {
	var n int64
	for _, c := range chunks(ids, 500) {
		if err := imp.touchLinked(ctx, c); err != nil {
			return n, err
		}
		res, err := imp.db.ExecContext(ctx,
			`DELETE FROM products WHERE id < ? AND id IN (`+placeholders(len(c), "?")+`)`,
			append([]any{csvProductIDBase}, int64Args(c)...)...)
		if err != nil {
			return n, err
		}
		k, _ := res.RowsAffected()
		n += k
	}
	return n, nil
}

// deleteRemoved deletes products moved to trash/draft/pending since the last sync.
func (imp *importer) deleteRemoved(ctx context.Context) error {
	var ids []int64
	for _, st := range removedStatuses {
		got, err := imp.fetchIDs(ctx, st, imp.since)
		if err != nil {
			return err
		}
		ids = append(ids, got...)
	}
	if len(ids) == 0 {
		return nil
	}
	n, err := imp.deleteProducts(ctx, ids)
	log.Printf("deleted %d products removed from the store (%d candidates)", n, len(ids))
	return err
}

// reconcile deletes local top-level products whose id no longer exists on
// WooCommerce (permanent deletes never appear in modified_after queries).
func (imp *importer) reconcile(ctx context.Context) error {
	remote := map[int64]bool{}
	for _, st := range productStatuses {
		ids, err := imp.fetchIDs(ctx, st, "")
		if err != nil {
			return err
		}
		for _, id := range ids {
			remote[id] = true
		}
	}

	rows, err := imp.db.QueryContext(ctx,
		`SELECT id FROM products WHERE parent_id IS NULL AND id < ?`, csvProductIDBase)
	if err != nil {
		return err
	}
	var local, missing []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		local = append(local, id)
		if !remote[id] {
			missing = append(missing, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Safety net: an API hiccup returning few ids must not wipe the catalog.
	if len(local) > 0 && len(missing)*2 > len(local) {
		return fmt.Errorf("refusing to delete %d of %d local products (WooCommerce returned %d ids)",
			len(missing), len(local), len(remote))
	}
	n, err := imp.deleteProducts(ctx, missing)
	log.Printf("reconcile: %d remote, %d local, deleted %d", len(remote), len(local), n)
	return err
}

// metaDenylist drops non-product plugin bookkeeping that surfaces as meta.
var metaDenylist = map[string]bool{
	"rank_math_internal_links_processed": true,
}

// metaRows flattens a product's ACF / custom fields into (product_id, key,
// value) argument triples. WordPress-internal keys (leading underscore) and
// known plugin noise are skipped.
func metaRows(productID int64, metas []wooMeta) []any {
	var args []any
	for _, m := range metas {
		if m.Key == "" || strings.HasPrefix(m.Key, "_") || metaDenylist[m.Key] {
			continue
		}
		val := clean(metaValueString(m.Value))
		if val == "" {
			continue
		}
		args = append(args, productID, m.Key, val)
	}
	return args
}

// metaValueString flattens an ACF value (string, number, array or object) to a
// storable string: plain strings are unquoted, everything else kept as JSON.
func metaValueString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

// fetchProducts pages through /products for every imported status, limited to
// products modified since imp.since in incremental modes.
func (imp *importer) fetchProducts(ctx context.Context) ([]wooProduct, error) {
	var all []wooProduct
	for _, status := range productStatuses {
		page := 1
		for {
			q := url.Values{
				"per_page": {"100"},
				"page":     {strconv.Itoa(page)},
				"status":   {status},
			}
			if imp.since != "" {
				q.Set("modified_after", imp.since)
				q.Set("dates_are_gmt", "true")
			}
			var batch []wooProduct
			more, err := imp.getJSON(ctx, "/products", q, &batch)
			if err != nil {
				return nil, err
			}
			all = append(all, batch...)
			log.Printf("  %s page %d: %d products (total %d)", status, page, len(batch), len(all))

			// Test mode: stop once the cap is reached, trimming any overshoot.
			if imp.maxProducts > 0 && len(all) >= imp.maxProducts {
				return all[:imp.maxProducts], nil
			}
			if !more || len(batch) == 0 {
				break
			}
			page++
		}
	}
	return all, nil
}

// fetchIDs lists only the ids of products with the given status (cheap:
// `_fields=id`), optionally limited to those modified since `since`.
func (imp *importer) fetchIDs(ctx context.Context, status, since string) ([]int64, error) {
	var ids []int64
	page := 1
	for {
		q := url.Values{
			"per_page": {"100"},
			"page":     {strconv.Itoa(page)},
			"status":   {status},
			"_fields":  {"id"},
		}
		if since != "" {
			q.Set("modified_after", since)
			q.Set("dates_are_gmt", "true")
		}
		var batch []struct {
			ID int64 `json:"id"`
		}
		more, err := imp.getJSON(ctx, "/products", q, &batch)
		if err != nil {
			return nil, err
		}
		for _, b := range batch {
			ids = append(ids, b.ID)
		}
		if !more || len(batch) == 0 {
			return ids, nil
		}
		page++
	}
}

// getJSON performs an authenticated GET and decodes into dst, retrying transient
// failures (timeouts, network errors, 429/5xx) with exponential backoff. It
// returns hasMore=true when the X-WP-TotalPages header indicates more pages.
func (imp *importer) getJSON(ctx context.Context, path string, q url.Values, dst any) (bool, error) {
	u := imp.base + path + "?" + q.Encode()
	const maxAttempts = 4
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// backoff: 3s, 6s, 12s ...
			wait := time.Duration(3<<(attempt-2)) * time.Second
			log.Printf("  retry %s (attempt %d) after %s: %v", path, attempt, wait, lastErr)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false, err
		}
		req.SetBasicAuth(imp.key, imp.secret)
		req.Header.Set("Accept", "application/json")

		resp, err := imp.client.Do(req)
		if err != nil {
			lastErr = err // network error / timeout — retry
			continue
		}
		// Retry on rate-limit / server errors; give up on other 4xx.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return false, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
		}

		err = json.NewDecoder(resp.Body).Decode(dst)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		curPage, _ := strconv.Atoi(q.Get("page"))
		totalPages, _ := strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
		return totalPages > curPage, nil
	}
	return false, fmt.Errorf("GET %s failed after %d attempts: %w", path, maxAttempts, lastErr)
}
