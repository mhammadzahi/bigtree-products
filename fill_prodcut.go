//go:build ignore

// fill_prodcut.go — one-shot importer that mirrors a WooCommerce catalog into
// the local `products` / `taxonomies` / `product_taxonomy` tables.
//
// Run manually from the project root, in one of two modes:
//
//	go run fill_prodcut.go            # test mode: first 1000 products (default)
//	go run fill_prodcut.go test       # same as above, explicit
//	go run fill_prodcut.go full       # normal mode: ALL products
//
// Reads everything from .env (same loader the server uses):
//
//	DB_*                       — database connection
//	WC_STORE_URL               — e.g. https://bigtree-group.com
//	WC_CONSUMER_KEY  (ck_...)  — WooCommerce REST API key
//	WC_CONSUMER_SECRET (cs_...) — WooCommerce REST API secret
//
// Idempotent: products/taxonomies are upserted, so re-running syncs changes
// without creating duplicates. Variable products also pull their variations.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bigtree-products/internal/config"
	"bigtree-products/internal/database"
)

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
// column is now a free VARCHAR, every attribute is kept — global attributes stay
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
	taxByKey    map[string]*taxo // "type|slug" -> taxo
	attrSeq     int64            // synthetic ids for attribute terms (no WP id in payload)
	maxProducts int              // 0 = unlimited (full mode); >0 = cap (test mode)
}

func main() {
	// Mode: "test" (default) imports up to testModeLimit; "full"/"all" imports
	// everything.
	mode := "test"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}
	maxProducts := testModeLimit
	switch mode {
	case "test", "":
		maxProducts = testModeLimit
	case "full", "all", "normal":
		maxProducts = 0
	default:
		log.Fatalf("unknown mode %q — use \"test\" or \"full\"", mode)
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
		client:      &http.Client{Timeout: 30 * time.Second},
		base:        store + "/wp-json/wc/v3",
		key:         key,
		secret:      secret,
		taxByKey:    map[string]*taxo{},
		attrSeq:     10_000_000_000, // well above typical WP term ids to avoid collisions
		maxProducts: maxProducts,
	}

	if maxProducts > 0 {
		log.Printf("running in TEST mode (max %d products)", maxProducts)
	} else {
		log.Printf("running in FULL mode (all products)")
	}

	if err := imp.run(); err != nil {
		log.Fatalf("import failed: %v", err)
	}
}

func (imp *importer) run() error {
	ctx := context.Background()

	// Import the full category tree first so hierarchy (sub-categories) and
	// parent links exist regardless of which categories the products reference.
	if err := imp.importCategoryTree(ctx); err != nil {
		return fmt.Errorf("categories: %w", err)
	}

	products, err := imp.fetchAllProducts(ctx)
	if err != nil {
		return err
	}
	log.Printf("fetched %d products from WooCommerce", len(products))

	// productTaxos[productID] = taxonomy keys ("type|slug") the product belongs to.
	productTaxos := map[int64][]string{}

	for _, p := range products {
		keys := imp.collectTaxonomies(p)
		productTaxos[p.ID] = keys
	}

	// 1) upsert taxonomies, then read back authoritative ids.
	if err := imp.upsertTaxonomies(ctx); err != nil {
		return fmt.Errorf("taxonomies: %w", err)
	}
	authTaxID, err := imp.loadTaxonomyIDs(ctx)
	if err != nil {
		return fmt.Errorf("load taxonomy ids: %w", err)
	}

	// 2) upsert products + rebuild their taxonomy links.
	var variableIDs []wooProduct
	for _, p := range products {
		if err := imp.upsertProduct(ctx, p); err != nil {
			return fmt.Errorf("product %d: %w", p.ID, err)
		}
		if err := imp.linkTaxonomies(ctx, p.ID, productTaxos[p.ID], authTaxID); err != nil {
			return fmt.Errorf("link product %d: %w", p.ID, err)
		}
		if err := imp.importMeta(ctx, p.ID, p.MetaData); err != nil {
			return fmt.Errorf("meta product %d: %w", p.ID, err)
		}
		if p.Type == "variable" {
			variableIDs = append(variableIDs, p)
		}
	}

	// 3) pull variations for variable parents (best-effort per parent).
	for _, parent := range variableIDs {
		if err := imp.importVariations(ctx, parent); err != nil {
			log.Printf("warning: variations for %d (%s): %v", parent.ID, parent.Slug, err)
		}
	}

	// 4) refresh cached taxonomy counts.
	if _, err := imp.db.ExecContext(ctx, `
		UPDATE taxonomies t SET count = (
			SELECT COUNT(*) FROM product_taxonomy pt
			JOIN products p ON p.id = pt.product_id
			WHERE pt.taxonomy_id = t.id AND p.parent_id IS NULL)`); err != nil {
		return fmt.Errorf("recount: %w", err)
	}

	log.Println("import complete")
	return nil
}

// getOrAddTax registers a taxonomy term (deduped by type|slug) and returns its key.
func (imp *importer) getOrAddTax(typ, slug, name string, realID int64) string {
	key := typ + "|" + slug
	if _, ok := imp.taxByKey[key]; ok {
		return key
	}
	id := realID
	if id == 0 {
		imp.attrSeq++
		id = imp.attrSeq
	}
	imp.taxByKey[key] = &taxo{id: id, typ: typ, slug: slug, name: name}
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

// upsertTaxonomies writes every collected term. ON DUPLICATE keeps any existing
// row's id (matched by the UNIQUE(type,slug)) and just refreshes the name.
func (imp *importer) upsertTaxonomies(ctx context.Context) error {
	const q = `INSERT INTO taxonomies (id, name, slug, type)
	           VALUES (?, ?, ?, ?)
	           ON DUPLICATE KEY UPDATE name = VALUES(name)`
	for _, t := range imp.taxByKey {
		if _, err := imp.db.ExecContext(ctx, q, t.id, t.name, t.slug, t.typ); err != nil {
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

const upsertProductSQL = `
INSERT INTO products
  (id, sku, title, slug, description, short_description, price, image_url, stock_status, product_type, parent_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  sku=VALUES(sku), title=VALUES(title), slug=VALUES(slug),
  description=VALUES(description), short_description=VALUES(short_description),
  price=VALUES(price), image_url=VALUES(image_url),
  stock_status=VALUES(stock_status), product_type=VALUES(product_type),
  parent_id=VALUES(parent_id)`

func (imp *importer) upsertProduct(ctx context.Context, p wooProduct) error {
	ptype := "simple"
	if p.Type == "variable" {
		ptype = "variable"
	}
	_, err := imp.db.ExecContext(ctx, upsertProductSQL,
		p.ID, p.SKU, p.Name, p.Slug, p.Description, p.ShortDescription,
		parsePrice(p.Price), firstImage(p.Images), mapStock(p.StockStatus),
		ptype, nullParent(p.Parent))
	return err
}

// linkTaxonomies rebuilds a product's junction rows from scratch (idempotent).
func (imp *importer) linkTaxonomies(ctx context.Context, productID int64, keys []string, authID map[string]int64) error {
	if _, err := imp.db.ExecContext(ctx,
		`DELETE FROM product_taxonomy WHERE product_id = ?`, productID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, k := range keys {
		id, ok := authID[k]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := imp.db.ExecContext(ctx,
			`INSERT IGNORE INTO product_taxonomy (product_id, taxonomy_id) VALUES (?, ?)`,
			productID, id); err != nil {
			return err
		}
	}
	return nil
}

// importVariations pulls a variable product's variations and stores them as
// child products (parent_id = the variable parent).
func (imp *importer) importVariations(ctx context.Context, parent wooProduct) error {
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
			child := wooProduct{
				ID: v.ID, Name: title, Slug: slug, SKU: v.SKU, Type: "simple",
				Price: v.Price, StockStatus: v.StockStatus, Parent: parent.ID, Images: img,
			}
			if err := imp.upsertProduct(ctx, child); err != nil {
				return err
			}
		}
		if !more || len(vs) == 0 {
			return nil
		}
		page++
	}
}

// importCategoryTree pulls every product category (not just those on sampled
// products) and stores them with their parent links, so sub-categories nest
// correctly. Done in two passes to satisfy the self-referencing foreign key.
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

	// pass 1: upsert every category with a NULL parent
	for _, c := range all {
		if _, err := imp.db.ExecContext(ctx, `
			INSERT INTO taxonomies (id, name, slug, type, parent_id)
			VALUES (?, ?, ?, 'category', NULL)
			ON DUPLICATE KEY UPDATE name = VALUES(name), slug = VALUES(slug)`,
			c.ID, c.Name, c.Slug); err != nil {
			return err
		}
	}
	// pass 2: set parent links now that all rows exist
	for _, c := range all {
		if c.Parent == 0 {
			continue
		}
		if _, err := imp.db.ExecContext(ctx,
			`UPDATE taxonomies SET parent_id = ? WHERE id = ?`, c.Parent, c.ID); err != nil {
			return err
		}
	}
	log.Printf("imported %d categories (with hierarchy)", len(all))
	return nil
}

// metaDenylist drops non-product plugin bookkeeping that surfaces as meta.
var metaDenylist = map[string]bool{
	"rank_math_internal_links_processed": true,
}

// importMeta rebuilds a product's ACF / custom-field rows. WordPress-internal
// keys (leading underscore) and known plugin noise are skipped.
func (imp *importer) importMeta(ctx context.Context, productID int64, metas []wooMeta) error {
	if _, err := imp.db.ExecContext(ctx,
		`DELETE FROM product_meta WHERE product_id = ?`, productID); err != nil {
		return err
	}
	for _, m := range metas {
		if m.Key == "" || strings.HasPrefix(m.Key, "_") || metaDenylist[m.Key] {
			continue
		}
		val := metaValueString(m.Value)
		if val == "" {
			continue
		}
		if _, err := imp.db.ExecContext(ctx,
			`INSERT INTO product_meta (product_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			productID, m.Key, val); err != nil {
			return err
		}
	}
	return nil
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

// fetchAllProducts pages through /products until exhausted.
func (imp *importer) fetchAllProducts(ctx context.Context) ([]wooProduct, error) {
	var all []wooProduct
	page := 1
	for {
		var batch []wooProduct
		more, err := imp.getJSON(ctx, "/products",
			url.Values{
				"per_page": {"100"},
				"page":     {strconv.Itoa(page)},
				"status":   {"publish"},
			}, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		log.Printf("  page %d: %d products (total %d)", page, len(batch), len(all))

		// Test mode: stop once the cap is reached, trimming any overshoot.
		if imp.maxProducts > 0 && len(all) >= imp.maxProducts {
			return all[:imp.maxProducts], nil
		}
		if !more || len(batch) == 0 {
			return all, nil
		}
		page++
	}
}

// getJSON performs an authenticated GET and decodes into dst. It returns
// hasMore=true when the WooCommerce X-WP-TotalPages header indicates more pages.
func (imp *importer) getJSON(ctx context.Context, path string, q url.Values, dst any) (bool, error) {
	u := imp.base + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(imp.key, imp.secret)
	req.Header.Set("Accept", "application/json")

	resp, err := imp.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return false, err
	}

	curPage, _ := strconv.Atoi(q.Get("page"))
	totalPages, _ := strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
	return totalPages > curPage, nil
}
