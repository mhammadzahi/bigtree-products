package models

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Product is a single catalog card / detail record.
type Product struct {
	ID               uint64     `json:"id"`
	SKU              string     `json:"sku"`
	Title            string     `json:"title"`
	Slug             string     `json:"slug"`
	Description      string     `json:"description,omitempty"`
	ShortDescription string     `json:"short_description"`
	Price            float64    `json:"price"`
	ImageURL         string     `json:"image_url"`
	StockStatus      string     `json:"stock_status"`
	ProductType      string     `json:"product_type"`
	Collections      []Taxonomy `json:"collections"`
}

// PriceLabel renders the price for templates ("On request" when zero).
func (p Product) PriceLabel() string {
	if p.Price <= 0 {
		return "Price on request"
	}
	return "$" + strconv.FormatFloat(p.Price, 'f', 2, 64)
}

// InStock is a template helper.
func (p Product) InStock() bool { return p.StockStatus == "in_stock" }

const pageSize = 12

// ProductFilter captures every query parameter the WooCommerce-style archive
// accepts. Zero values mean "not filtered".
type ProductFilter struct {
	Search       string   // ?s=
	Category     string   // ?category= (slug)
	Collection   string   // ?collection= (slug — WooCommerce tag)
	Brands       []string // ?brand= (repeatable)
	Colors       []string // ?pa_color= (repeatable)
	Compositions []string // ?pa_composition=
	Applications []string // ?pa_application=
	Types        []string // ?pa_types=
	Features     []string // ?pa_features=
	OrderBy      string   // ?orderby=title_asc|title_desc|newest
	Page         int      // ?page= (1-based)
}

// ProductPage is the paginated result set.
type ProductPage struct {
	Products    []Product `json:"products"`
	Total       int       `json:"total"`
	Page        int       `json:"page"`
	PageSize    int       `json:"page_size"`
	TotalPages  int       `json:"total_pages"`
	HasPrev     bool      `json:"has_prev"`
	HasNext     bool      `json:"has_next"`
}

// orderClauses whitelists sort options — user input never reaches the ORDER BY
// clause directly, which keeps this injection-proof.
var orderClauses = map[string]string{
	"price_asc":  "p.price ASC, p.id ASC",
	"price_desc": "p.price DESC, p.id ASC",
	"title_asc":  "p.title ASC",
	"title_desc": "p.title DESC",
	"newest":     "p.created_at DESC, p.id DESC",
}

// buildWhere turns a ProductFilter into a parameterised WHERE clause fragment
// plus its ordered args. Every taxonomy facet becomes an EXISTS sub-query so
// multiple facets combine with AND semantics (a product must match them all),
// exactly like WooCommerce's layered nav.
func buildWhere(f ProductFilter) (string, []any) {
	// Only ever list top-level products (parent_id IS NULL); variations are
	// children and shouldn't appear as their own cards, mirroring the storefront.
	clauses := []string{"p.parent_id IS NULL"}
	args := []any{}

	if s := strings.TrimSpace(f.Search); s != "" {
		// Match on title OR SKU. LIKE keeps partial-token matching predictable
		// for short B2B SKUs where FULLTEXT min-word-length would drop terms.
		like := "%" + s + "%"
		clauses = append(clauses, "(p.title LIKE ? OR p.sku LIKE ?)")
		args = append(args, like, like)
	}

	addTax := func(typ, slug string) {
		clauses = append(clauses, `EXISTS (
			SELECT 1 FROM product_taxonomy pt
			JOIN taxonomies t ON t.id = pt.taxonomy_id
			WHERE pt.product_id = p.id AND t.type = ? AND t.slug = ?)`)
		args = append(args, typ, slug)
	}
	addTaxAny := func(typ string, slugs []string) {
		// Repeatable facet: product must match AT LEAST ONE of the chosen terms.
		clean := nonEmpty(slugs)
		if len(clean) == 0 {
			return
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(clean)), ",")
		clauses = append(clauses, `EXISTS (
			SELECT 1 FROM product_taxonomy pt
			JOIN taxonomies t ON t.id = pt.taxonomy_id
			WHERE pt.product_id = p.id AND t.type = ? AND t.slug IN (`+ph+`))`)
		args = append(args, typ)
		for _, s := range clean {
			args = append(args, s)
		}
	}

	if f.Category != "" {
		addTax("category", f.Category)
	}
	if f.Collection != "" {
		addTax("collection", f.Collection)
	}
	addTaxAny("brand", f.Brands)
	addTaxAny("pa_color", f.Colors)
	addTaxAny("pa_composition", f.Compositions)
	addTaxAny("pa_application", f.Applications)
	addTaxAny("pa_types", f.Types)
	addTaxAny("pa_features", f.Features)

	return strings.Join(clauses, " AND "), args
}

// QueryProducts runs the filtered, sorted, paginated catalog query plus a
// matching COUNT over the same predicate, then hydrates each card's collection
// tags in a single follow-up query (avoids the N+1 pattern).
func QueryProducts(ctx context.Context, db *sql.DB, f ProductFilter) (*ProductPage, error) {
	where, args := buildWhere(f)

	order, ok := orderClauses[f.OrderBy]
	if !ok {
		order = orderClauses["newest"]
	}

	page := f.Page
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	// --- total count over the identical predicate ---------------------------
	var total int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM products p WHERE `+where, args...).Scan(&total); err != nil {
		return nil, err
	}

	// --- page of rows -------------------------------------------------------
	rowArgs := append(append([]any{}, args...), pageSize, offset)
	rows, err := db.QueryContext(ctx, `
		SELECT p.id, COALESCE(p.sku,''), p.title, p.slug,
		       COALESCE(p.short_description,''), p.price,
		       COALESCE(p.image_url,''), p.stock_status, p.product_type
		FROM products p
		WHERE `+where+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`, rowArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []Product
	ids := []any{}
	index := map[uint64]int{}
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SKU, &p.Title, &p.Slug,
			&p.ShortDescription, &p.Price, &p.ImageURL,
			&p.StockStatus, &p.ProductType); err != nil {
			return nil, err
		}
		index[p.ID] = len(products)
		ids = append(ids, p.ID)
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := attachCollections(ctx, db, products, ids, index); err != nil {
		return nil, err
	}

	totalPages := (total + pageSize - 1) / pageSize
	return &ProductPage{
		Products:   products,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}, nil
}

// attachCollections hydrates the B2B collection tags shown on each card in one
// batched query keyed by the product ids on the current page.
func attachCollections(ctx context.Context, db *sql.DB, products []Product, ids []any, index map[uint64]int) error {
	if len(ids) == 0 {
		return nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := db.QueryContext(ctx, `
		SELECT pt.product_id, t.id, t.name, t.slug, t.type
		FROM product_taxonomy pt
		JOIN taxonomies t ON t.id = pt.taxonomy_id
		WHERE t.type = 'collection' AND pt.product_id IN (`+ph+`)
		ORDER BY t.name`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var pid uint64
		var t Taxonomy
		if err := rows.Scan(&pid, &t.ID, &t.Name, &t.Slug, &t.Type); err != nil {
			return err
		}
		if i, ok := index[pid]; ok {
			products[i].Collections = append(products[i].Collections, t)
		}
	}
	return rows.Err()
}

// GetProductBySlug loads a single product with its full description and all
// taxonomy tags — backs the product detail permalink.
func GetProductBySlug(ctx context.Context, db *sql.DB, slug string) (*Product, error) {
	p := &Product{}
	err := db.QueryRowContext(ctx, `
		SELECT id, COALESCE(sku,''), title, slug,
		       COALESCE(description,''), COALESCE(short_description,''),
		       price, COALESCE(image_url,''), stock_status, product_type
		FROM products WHERE slug = ? AND parent_id IS NULL`, slug).
		Scan(&p.ID, &p.SKU, &p.Title, &p.Slug, &p.Description,
			&p.ShortDescription, &p.Price, &p.ImageURL, &p.StockStatus, &p.ProductType)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, t.type
		FROM product_taxonomy pt JOIN taxonomies t ON t.id = pt.taxonomy_id
		WHERE pt.product_id = ? AND t.type = 'collection' ORDER BY t.name`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Taxonomy
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Type); err != nil {
			return nil, err
		}
		p.Collections = append(p.Collections, t)
	}
	return p, rows.Err()
}

// MetaKV is one ACF / postmeta key-value pair.
type MetaKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ProductFull is everything the staff dashboard shows for one product: the core
// row, every taxonomy membership grouped by type, all ACF/meta, and (for
// variable products) the child variations.
type ProductFull struct {
	Product
	Taxonomies map[string][]Taxonomy `json:"taxonomies"` // type -> terms
	Meta       []MetaKV              `json:"meta"`
	Variations []Product             `json:"variations"`
}

// GetProductFull loads the complete dashboard record for a product slug.
func GetProductFull(ctx context.Context, db *sql.DB, slug string) (*ProductFull, error) {
	pf := &ProductFull{Taxonomies: map[string][]Taxonomy{}}
	err := db.QueryRowContext(ctx, `
		SELECT id, COALESCE(sku,''), title, slug,
		       COALESCE(description,''), COALESCE(short_description,''),
		       price, COALESCE(image_url,''), stock_status, product_type
		FROM products WHERE slug = ? AND parent_id IS NULL`, slug).
		Scan(&pf.ID, &pf.SKU, &pf.Title, &pf.Slug, &pf.Description,
			&pf.ShortDescription, &pf.Price, &pf.ImageURL, &pf.StockStatus, &pf.ProductType)
	if err != nil {
		return nil, err
	}

	// All taxonomy memberships, grouped by type.
	txRows, err := db.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, t.type, t.count
		FROM product_taxonomy pt JOIN taxonomies t ON t.id = pt.taxonomy_id
		WHERE pt.product_id = ? ORDER BY t.type, t.name`, pf.ID)
	if err != nil {
		return nil, err
	}
	for txRows.Next() {
		var t Taxonomy
		if err := txRows.Scan(&t.ID, &t.Name, &t.Slug, &t.Type, &t.Count); err != nil {
			txRows.Close()
			return nil, err
		}
		pf.Taxonomies[t.Type] = append(pf.Taxonomies[t.Type], t)
		if t.Type == "collection" {
			pf.Collections = append(pf.Collections, t)
		}
	}
	txRows.Close()
	if err := txRows.Err(); err != nil {
		return nil, err
	}

	// All ACF / postmeta.
	mRows, err := db.QueryContext(ctx, `
		SELECT meta_key, COALESCE(meta_value,'')
		FROM product_meta WHERE product_id = ? ORDER BY meta_key`, pf.ID)
	if err != nil {
		return nil, err
	}
	for mRows.Next() {
		var kv MetaKV
		if err := mRows.Scan(&kv.Key, &kv.Value); err != nil {
			mRows.Close()
			return nil, err
		}
		pf.Meta = append(pf.Meta, kv)
	}
	mRows.Close()
	if err := mRows.Err(); err != nil {
		return nil, err
	}

	// Variations (child products).
	vRows, err := db.QueryContext(ctx, `
		SELECT id, COALESCE(sku,''), title, slug, price, COALESCE(image_url,''), stock_status
		FROM products WHERE parent_id = ? ORDER BY title`, pf.ID)
	if err != nil {
		return nil, err
	}
	for vRows.Next() {
		var v Product
		if err := vRows.Scan(&v.ID, &v.SKU, &v.Title, &v.Slug, &v.Price, &v.ImageURL, &v.StockStatus); err != nil {
			vRows.Close()
			return nil, err
		}
		pf.Variations = append(pf.Variations, v)
	}
	vRows.Close()
	if err := vRows.Err(); err != nil {
		return nil, err
	}

	return pf, nil
}

func nonEmpty(in []string) []string {
	out := in[:0:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
