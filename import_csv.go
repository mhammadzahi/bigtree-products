//go:build ignore

// import_csv.go — imports products from a WooCommerce-format product CSV (the
// "Master Product List" exports, e.g. REVIEW_Antiga_...csv) straight into the
// local `products` / `taxonomies` / `product_taxonomy` / `product_meta` tables.
//
// Use it for products that are NOT (yet) on the WooCommerce store, so
// fill_prodcut.go cannot pull them.
//
//	go run import_csv.go --dry-run a.csv b.csv   # do everything, then ROLLBACK
//	go run import_csv.go a.csv b.csv             # import (one transaction)
//	go run import_csv.go --purge a.csv b.csv     # delete the rows these CSVs created
//
// Mapping (mirrors what fill_prodcut.go produces for WooCommerce products):
//
//	SKU / Name / Description / Images[0]  -> products row (simple, top-level)
//	Categories "A > B > C, X > Y > C"     -> linked to each path's LEAF category;
//	                                         path resolved by name from the root,
//	                                         missing nodes are created
//	Selections (or Collections root)      -> `collection` term ("ANT Selection")
//	Brands                                -> `brand` term
//	Attribute N name/value(s)             -> `pa_<name>` terms (Color is split
//	                                         into individual colour names)
//	Meta: <key> (non "_" keys)            -> product_meta
//	Collections / Selections / supplier   -> product_meta collections / selections
//	                                         / supplier_sku
//	Meta: _temp_related_skus              -> product_meta related_series_products
//	                                         (JSON array of product ids, like WC)
//
// IDs: the CSVs carry no WooCommerce ids, so products get synthetic ids from
// csvProductIDBase and new terms from csvTaxonomyIDBase. Both ranges are far
// above real WP ids and above fill_prodcut.go's synthetic attribute range
// (10_000_000_000+), so the two importers never collide.
//
// Idempotent: products are matched by SKU and updated in place. A SKU that
// already exists with a real WooCommerce id (< csvProductIDBase) is skipped —
// the WooCommerce sync owns it.
//
// Performance: the DB is remote, so taxonomies and product slugs/SKUs are
// preloaded into memory and each product is written with a handful of batched
// statements instead of per-term round trips.
//
// Caveat: once these products are created on WooCommerce, run --purge BEFORE
// the next fill_prodcut.go sync, otherwise the WC copy (new id, same slug)
// collides with the CSV copy on UNIQUE(slug).
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"bigtree-products/internal/config"
	"bigtree-products/internal/database"
)

const (
	csvProductIDBase  int64 = 9_000_000_000
	csvTaxonomyIDBase int64 = 20_000_000_000
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func clean(s string) string { return html.UnescapeString(strings.TrimSpace(s)) }

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// isNA mirrors models.isNA plus spreadsheet "#N/A" artefacts.
func isNA(s string) bool {
	switch lower(s) {
	case "", "n/a", "na", "n-a", "n\\a", "#n/a", "-":
		return true
	}
	return false
}

// row is one CSV record keyed by header name.
type row map[string]string

func (r row) get(k string) string { return strings.TrimSpace(r[k]) }

func readCSV(path string) ([]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, err
	}
	for i := range header {
		header[i] = strings.TrimSpace(strings.TrimPrefix(header[i], "\uFEFF"))
	}
	var out []row
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		r := row{}
		for i, v := range rec {
			if i < len(header) && header[i] != "" {
				r[header[i]] = v
			}
		}
		if r.get("SKU") == "" || r.get("Name") == "" {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// importer
// ---------------------------------------------------------------------------

type term struct {
	id     int64
	name   string
	slug   string
	typ    string
	parent int64 // 0 = none
}

type importer struct {
	ctx context.Context
	tx  *sql.Tx

	nextProd, nextTax int64

	// preloaded state
	termByName map[string]*term  // "type|lower(name)" (non-category)
	termBySlug map[string]*term  // "type|slug"
	catByName  map[string]*term  // "parentID|lower(name)"
	prodBySKU  map[string]int64  // sku -> id
	prodSlugs  map[string]int64  // slug -> id
	links      map[int64][]int64 // existing CSV products' taxonomy links

	touched map[int64]bool // taxonomy ids whose cached count needs refreshing
	created struct{ products, updated, skipped, terms, categories int }
	newCats []string
}

func main() {
	var dryRun, purge bool
	var files []string
	for _, a := range os.Args[1:] {
		switch a {
		case "--dry-run", "-n":
			dryRun = true
		case "--purge":
			purge = true
		default:
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		log.Fatal("usage: go run import_csv.go [--dry-run] [--purge] file.csv [more.csv ...]")
	}

	var rows []row
	for _, f := range files {
		rs, err := readCSV(f)
		if err != nil {
			log.Fatalf("read %s: %v", f, err)
		}
		log.Printf("%s: %d products", f, len(rs))
		rows = append(rows, rs...)
	}

	db, err := database.Connect(config.Load().DSN)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Rollback()

	imp := &importer{ctx: ctx, tx: tx, touched: map[int64]bool{}}
	if err := imp.preload(); err != nil {
		log.Fatalf("preload: %v", err)
	}

	if purge {
		err = imp.purge(rows)
	} else {
		err = imp.run(rows)
	}
	if err != nil {
		log.Fatalf("import failed (rolled back): %v", err)
	}

	if dryRun {
		log.Println("DRY RUN — rolling back, nothing written")
		return
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}
	log.Println("committed")
}

func (imp *importer) exec(q string, args ...any) error {
	_, err := imp.tx.ExecContext(imp.ctx, q, args...)
	return err
}

// preload pulls every taxonomy term, every product SKU/slug and the links of
// previously CSV-imported products in three queries.
func (imp *importer) preload() error {
	imp.termByName = map[string]*term{}
	imp.termBySlug = map[string]*term{}
	imp.catByName = map[string]*term{}
	imp.prodBySKU = map[string]int64{}
	imp.prodSlugs = map[string]int64{}
	imp.links = map[int64][]int64{}
	imp.nextProd, imp.nextTax = csvProductIDBase, csvTaxonomyIDBase

	rows, err := imp.tx.QueryContext(imp.ctx,
		`SELECT id, name, slug, type, COALESCE(parent_id, 0) FROM taxonomies ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		t := &term{}
		if err := rows.Scan(&t.id, &t.name, &t.slug, &t.typ, &t.parent); err != nil {
			rows.Close()
			return err
		}
		imp.indexTerm(t)
		if t.id > imp.nextTax {
			imp.nextTax = t.id
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = imp.tx.QueryContext(imp.ctx, `SELECT id, COALESCE(sku, ''), slug FROM products ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var sku, slug string
		if err := rows.Scan(&id, &sku, &slug); err != nil {
			rows.Close()
			return err
		}
		if _, dup := imp.prodBySKU[sku]; sku != "" && !dup {
			imp.prodBySKU[sku] = id
		}
		imp.prodSlugs[slug] = id
		if id > imp.nextProd {
			imp.nextProd = id
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if imp.nextProd < csvProductIDBase {
		imp.nextProd = csvProductIDBase
	}

	rows, err = imp.tx.QueryContext(imp.ctx,
		`SELECT product_id, taxonomy_id FROM product_taxonomy WHERE product_id >= ?`, csvProductIDBase)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p, t int64
		if err := rows.Scan(&p, &t); err != nil {
			return err
		}
		imp.links[p] = append(imp.links[p], t)
	}
	log.Printf("preloaded %d taxonomy terms, %d products", len(imp.termBySlug), len(imp.prodSlugs))
	return rows.Err()
}

func (imp *importer) indexTerm(t *term) {
	// First (lowest id) wins, matching ORDER BY id lookups.
	if t.typ == "category" {
		k := fmt.Sprintf("%d|%s", t.parent, lower(t.name))
		if _, ok := imp.catByName[k]; !ok {
			imp.catByName[k] = t
		}
	} else {
		k := t.typ + "|" + lower(t.name)
		if _, ok := imp.termByName[k]; !ok {
			imp.termByName[k] = t
		}
	}
	if k := t.typ + "|" + t.slug; imp.termBySlug[k] == nil {
		imp.termBySlug[k] = t
	}
}

func (imp *importer) touchProduct(id int64) {
	for _, t := range imp.links[id] {
		imp.touched[t] = true
	}
}

func (imp *importer) purge(rows []row) error {
	var ids []any
	for _, r := range rows {
		if id, ok := imp.prodBySKU[r.get("SKU")]; ok && id >= csvProductIDBase {
			imp.touchProduct(id)
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		// FKs cascade to product_taxonomy and product_meta.
		if err := imp.exec(`DELETE FROM products WHERE id IN (`+placeholders(len(ids))+`)`, ids...); err != nil {
			return err
		}
	}
	log.Printf("purged %d products", len(ids))
	return imp.recount()
}

func (imp *importer) run(rows []row) error {
	skuToID := map[string]int64{}
	var imported []row
	for _, r := range rows {
		id, ok, err := imp.upsertProduct(r)
		if err != nil {
			return fmt.Errorf("%s: %w", r.get("SKU"), err)
		}
		if !ok {
			continue
		}
		skuToID[r.get("SKU")] = id
		imported = append(imported, r)
		if err := imp.linkTaxonomies(id, r); err != nil {
			return fmt.Errorf("%s taxonomies: %w", r.get("SKU"), err)
		}
		if len(imported)%20 == 0 {
			log.Printf("  %d/%d products written", len(imported), len(rows))
		}
	}
	// Meta runs after every product exists so related SKUs resolve to ids.
	log.Printf("writing meta")
	for _, r := range imported {
		if err := imp.importMeta(skuToID[r.get("SKU")], r, skuToID); err != nil {
			return fmt.Errorf("%s meta: %w", r.get("SKU"), err)
		}
	}
	if err := imp.recount(); err != nil {
		return err
	}

	c := imp.created
	log.Printf("products: %d new, %d updated, %d skipped (owned by WooCommerce sync)",
		c.products, c.updated, c.skipped)
	log.Printf("new terms: %d, new categories: %d", c.terms, c.categories)
	for _, p := range imp.newCats {
		log.Printf("  + category %s", p)
	}
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// recount refreshes the cached count of every touched taxonomy term only — a
// full-table recount is far too slow (and lock-heavy) over the remote DB.
func (imp *importer) recount() error {
	if len(imp.touched) == 0 {
		return nil
	}
	args := make([]any, 0, len(imp.touched))
	for id := range imp.touched {
		args = append(args, id)
	}
	log.Printf("recounting %d taxonomy terms", len(args))
	return imp.exec(`
		UPDATE taxonomies t SET count = (
			SELECT COUNT(*) FROM product_taxonomy pt
			JOIN products p ON p.id = pt.product_id
			WHERE pt.taxonomy_id = t.id AND p.parent_id IS NULL)
		WHERE t.id IN (`+placeholders(len(args))+`)`, args...)
}

// upsertProduct writes the products row. ok=false means the SKU is skipped.
func (imp *importer) upsertProduct(r row) (id int64, ok bool, err error) {
	sku := r.get("SKU")
	id, exists := imp.prodBySKU[sku]
	switch {
	case !exists:
		imp.nextProd++
		id = imp.nextProd
		imp.created.products++
	case id < csvProductIDBase:
		log.Printf("skip %s: already synced from WooCommerce (id %d)", sku, id)
		imp.created.skipped++
		return 0, false, nil
	default:
		imp.created.updated++
		imp.touchProduct(id)
	}

	title := clean(r.get("Name"))
	slug, err := imp.uniqueSlug(id, title, r)
	if err != nil {
		return 0, false, err
	}
	stock := "in_stock"
	if r.get("In stock?") == "0" {
		stock = "out_of_stock"
	}
	price, _ := strconv.ParseFloat(r.get("Regular price"), 64)
	image := strings.TrimSpace(strings.Split(r.get("Images"), ",")[0])

	err = imp.exec(`
		INSERT INTO products
		  (id, sku, title, slug, description, short_description, price, image_url, stock_status, product_type, parent_id)
		VALUES (?, ?, ?, ?, ?, '', ?, ?, ?, 'simple', NULL)
		ON DUPLICATE KEY UPDATE
		  sku=VALUES(sku), title=VALUES(title), slug=VALUES(slug),
		  description=VALUES(description), price=VALUES(price), image_url=VALUES(image_url),
		  stock_status=VALUES(stock_status)`,
		id, sku, title, slug, toHTML(r.get("Description")), price, image, stock)
	if err != nil {
		return 0, false, err
	}
	imp.prodBySKU[sku] = id
	imp.prodSlugs[slug] = id
	return id, true, nil
}

// uniqueSlug returns slugify(name), falling back to name+brand, then name+SKU
// with a numeric suffix, when another product already holds the slug.
func (imp *importer) uniqueSlug(id int64, name string, r row) (string, error) {
	cands := []string{slugify(name), slugify(name + " " + r.get("Brands")), slugify(name + " " + r.get("SKU"))}
	for i := 2; i < 50; i++ {
		cands = append(cands, cands[2]+"-"+strconv.Itoa(i))
	}
	for _, s := range cands {
		if s == "" {
			continue
		}
		if other, taken := imp.prodSlugs[s]; !taken || other == id {
			return s, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q", name)
}

// toHTML turns plain-text descriptions into the <p>/<br /> markup WooCommerce
// would store, since the product page renders description as HTML.
func toHTML(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n")
	if s == "" || strings.Contains(s, "<") {
		return s
	}
	var out []string
	for _, para := range strings.Split(s, "\n\n") {
		if p := strings.TrimSpace(para); p != "" {
			out = append(out, "<p>"+strings.ReplaceAll(html.EscapeString(p), "\n", "<br />\n")+"</p>")
		}
	}
	return strings.Join(out, "\n")
}

// ---------------------------------------------------------------------------
// taxonomies
// ---------------------------------------------------------------------------

func (imp *importer) linkTaxonomies(productID int64, r row) error {
	ids := map[int64]bool{}

	// Categories: one leaf per comma-separated path.
	for _, path := range strings.Split(r.get("Categories"), ",") {
		if strings.TrimSpace(path) == "" {
			continue
		}
		id, err := imp.categoryPath(path)
		if err != nil {
			return err
		}
		ids[id] = true
	}

	add := func(typ, name string) error {
		if isNA(name) {
			return nil
		}
		id, err := imp.term(typ, name)
		if err == nil {
			ids[id] = true
		}
		return err
	}

	// Collection: the B2B "Selection" (Selections column, else Collections root).
	sel := r.get("Selections")
	if sel == "" {
		sel = strings.TrimSpace(strings.Split(r.get("Collections"), ">")[0])
	}
	if err := add("collection", sel); err != nil {
		return err
	}
	for _, b := range strings.Split(r.get("Brands"), ",") {
		if err := add("brand", b); err != nil {
			return err
		}
	}

	// Attributes 1..N.
	for i := 1; ; i++ {
		name, ok := r[fmt.Sprintf("Attribute %d name", i)]
		if !ok {
			break
		}
		if strings.TrimSpace(name) == "" {
			continue
		}
		typ := "pa_" + slugify(name)
		for _, v := range attrValues(typ, r.get(fmt.Sprintf("Attribute %d value(s)", i))) {
			if err := add(typ, v); err != nil {
				return err
			}
		}
	}

	if err := imp.exec(`DELETE FROM product_taxonomy WHERE product_id = ?`, productID); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	var args []any
	for id := range ids {
		imp.touched[id] = true
		args = append(args, productID, id)
	}
	return imp.exec(`INSERT IGNORE INTO product_taxonomy (product_id, taxonomy_id) VALUES `+
		strings.TrimSuffix(strings.Repeat("(?, ?),", len(ids)), ","), args...)
}

var labelRe = regexp.MustCompile(`(?m)^[^|,\n]*:\s*$`)

// attrValues splits a WooCommerce attribute cell into terms. Colour cells in
// the furniture sheets look like "Upholstery:\nBalsa | Noce\nLegs:\nIvory" —
// the "Label:" lines are dropped and every colour becomes its own term.
func attrValues(typ, cell string) []string {
	if typ == "pa_size" { // "L: 120 W: 60 H: 87 cm" is one value
		if isNA(cell) {
			return nil
		}
		return []string{strings.TrimSpace(cell)}
	}
	if typ == "pa_color" {
		cell = labelRe.ReplaceAllString(cell, "")
		cell = strings.NewReplacer("|", ",", "\n", ",").Replace(cell)
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range strings.Split(cell, ",") {
		v = strings.TrimSpace(v)
		if isNA(v) || seen[lower(v)] {
			continue
		}
		seen[lower(v)] = true
		out = append(out, v)
	}
	return out
}

// term finds a non-category term by (type, name) — or (type, slug) — creating
// it when missing.
func (imp *importer) term(typ, name string) (int64, error) {
	name = clean(name)
	if t := imp.termByName[typ+"|"+lower(name)]; t != nil {
		return t.id, nil
	}
	slug := slugify(name)
	if t := imp.termBySlug[typ+"|"+slug]; t != nil {
		return t.id, nil
	}
	imp.nextTax++
	t := &term{id: imp.nextTax, name: name, slug: slug, typ: typ}
	if err := imp.exec(`INSERT INTO taxonomies (id, name, slug, type) VALUES (?, ?, ?, ?)`,
		t.id, t.name, t.slug, t.typ); err != nil {
		return 0, err
	}
	imp.indexTerm(t)
	imp.created.terms++
	return t.id, nil
}

// categoryPath resolves "Furniture > Seating > Armchairs" by name, walking
// from the root, creating any missing node. Returns the leaf id.
func (imp *importer) categoryPath(path string) (int64, error) {
	var parent *term
	var trail []string
	for _, seg := range strings.Split(path, ">") {
		name := clean(seg)
		if name == "" {
			continue
		}
		trail = append(trail, name)
		var pid int64
		if parent != nil {
			pid = parent.id
		}
		t := imp.catByName[fmt.Sprintf("%d|%s", pid, lower(name))]
		if t == nil {
			var err error
			if t, err = imp.createCategory(name, parent); err != nil {
				return 0, err
			}
			imp.newCats = append(imp.newCats, strings.Join(trail, " > "))
		}
		parent = t
	}
	if parent == nil {
		return 0, fmt.Errorf("empty category path %q", path)
	}
	return parent.id, nil
}

// createCategory inserts a category, picking a slug the WooCommerce way:
// "name", then "name-<parent slug>", then numbered.
func (imp *importer) createCategory(name string, parent *term) (*term, error) {
	base := slugify(name)
	cands := []string{base}
	var pid any
	if parent != nil {
		pid = parent.id
		cands = append(cands, base+"-"+parent.slug)
	}
	for i := 2; i < 50; i++ {
		cands = append(cands, cands[len(cands)-1]+"-"+strconv.Itoa(i))
	}
	for _, s := range cands {
		if imp.termBySlug["category|"+s] != nil {
			continue
		}
		imp.nextTax++
		t := &term{id: imp.nextTax, name: name, slug: s, typ: "category"}
		if parent != nil {
			t.parent = parent.id
		}
		if err := imp.exec(`INSERT INTO taxonomies (id, name, slug, type, parent_id) VALUES (?, ?, ?, 'category', ?)`,
			t.id, name, s, pid); err != nil {
			return nil, err
		}
		imp.indexTerm(t)
		imp.created.categories++
		return t, nil
	}
	return nil, fmt.Errorf("no free slug for category %q", name)
}

// ---------------------------------------------------------------------------
// meta
// ---------------------------------------------------------------------------

func (imp *importer) importMeta(productID int64, r row, skuToID map[string]int64) error {
	if err := imp.exec(`DELETE FROM product_meta WHERE product_id = ?`, productID); err != nil {
		return err
	}
	var args []any
	put := func(k, v string) {
		if v = clean(v); v != "" {
			args = append(args, productID, k, v)
		}
	}

	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if mk, ok := strings.CutPrefix(k, "Meta: "); ok && !strings.HasPrefix(mk, "_") {
			put(mk, r[k])
		}
	}
	put("selections", r.get("Selections"))
	put("collections", r.get("Collections"))
	put("supplier_sku", r.get("(Supplier)-SKU"))

	// Related SKUs -> related_series_products as a JSON array of id strings,
	// the same shape WooCommerce's ACF relationship field produces.
	var related []string
	for _, s := range strings.Split(r.get("Meta: _temp_related_skus"), "|") {
		if id, ok := skuToID[strings.TrimSpace(s)]; ok {
			related = append(related, strconv.FormatInt(id, 10))
		}
	}
	if len(related) > 0 {
		b, _ := json.Marshal(related)
		put("related_series_products", string(b))
	}

	if len(args) == 0 {
		return nil
	}
	return imp.exec(`INSERT INTO product_meta (product_id, meta_key, meta_value) VALUES `+
		strings.TrimSuffix(strings.Repeat("(?, ?, ?),", len(args)/3), ","), args...)
}
