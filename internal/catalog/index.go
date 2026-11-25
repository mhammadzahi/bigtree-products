package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ---------------------------------------------------------------------------
// Per-category configuration (Data Layer v5, Fabrics v12, Add-on Categories)
// ---------------------------------------------------------------------------

// Tops are the top-level categories with category pages, in display order.
var Tops = []string{"furniture", "floor-coverings", "fabrics", "wallcoverings", "leathers", "lighting"}

var (
	flatTops        = set("leathers", "lighting") // list every product directly, Type as filter
	passthroughTops = set("floor-coverings")      // a single child skips its level
	swatchTops      = set("floor-coverings", "furniture", "lighting")
	swatchForceTops = set("lighting")             // finishes under "Finishes" are swatches
	perfTops        = set("fabrics")              // Martindale / lightfastness / fire standard
	projectTops     = set("leathers")             // Project filter shown
	hiddenFacets    = map[string]map[string]bool{ // per top
		"leathers": set(FMaterial, FFlame, FOutdoor),
		"lighting": set(FFlame),
	}
	typeIgnore = map[string]map[string]bool{"fabrics": set("n-a")}

	// Fabrics: the five tiles; every other category below Fabrics is hidden.
	fabricTiles = []string{"drapery-fabrics", "upholstery-fabrics", "linens-fabrics", "trimmings", "decorative-accessories-2"}

	// Leaf pages loaded 60 cards at a time.
	pagedLeaves = set("drapery-fabrics", "upholstery-fabrics")

	// Children of `sofas` (under furniture) that behave as leaves.
	sofaCollapse = set("linear", "linear sofas", "modular & sectionals", "modular and sectionals",
		"modular sectionals", "modular & sectional")
)

// PageSize for paged leaves.
const PageSize = 60

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// Term is a category or collection term.
type Term struct {
	ID     int64
	Name   string // decoded display name
	Slug   string
	Type   string
	Parent int64
}

// Product is one top-level product with its raw facet inputs.
type Product struct {
	ID         int64
	SKU        string
	Title      string
	Slug       string
	Image      string
	Gallery    []string
	Status     string // publish | private
	Visibility string // visible | catalog | search | hidden
	InStock    bool

	Cats    []int64 // direct categories
	Colls   []int64 // collection terms
	Related []int64 // related_series_products (colour versions / swatches)

	Outdoor, Flame bool
	Projects       []string // project keys (Norm of label)
	Colors         []string // colour families of its own terms
	Types, Styles  []string // term slugs
	Apps           []string // pa_application slugs
	Compositions   []string // raw pa_composition names (families depend on the top)

	LeafColl  int64  // deepest collection term
	CollName  string // its name (card line + sort key)
	Selection int64  // top ancestor of LeafColl

	Mt   int     // Martindale rubs
	Lf   float64 // lightfastness
	Fire []string

	RoundImage string // round-variation photo (round rugs)
}

// Hidden reports catalog visibility "hidden from catalog".
func (p *Product) Hidden() bool { return p.Visibility == "search" || p.Visibility == "hidden" }

// Private reports a private product (PRIVATE tag on the card).
func (p *Product) Private() bool { return p.Status == "private" }

// Rec is a product's facet record within one top (materials and colours can
// differ per top: per-top material rules, swatch colours).
type Rec struct {
	P      *Product
	Mat    []string
	Colors []string
}

// Top is the index of one top-level category.
type Top struct {
	Term     *Term
	Slug     string
	Recs     map[int64]*Rec    // visible products in this top
	InCat    map[int64][]int64 // category id -> product ids (sorted by card order)
	Count    map[int64]int     // category id -> product count
	Versions map[int64][]int64 // lead id -> colour-version ids
	Fin      map[int64]*Rec    // colour-version records
	virtual  map[int64]bool    // fabrics: tile ids filled by rule
}

// Index is the whole catalog index; immutable once built.
type Index struct {
	Built     time.Time
	Duration  time.Duration
	Terms     map[int64]*Term
	CatBySlug map[string]*Term
	Children  map[int64][]int64 // category tree
	Products  map[int64]*Product
	Tops      map[string]*Top
	TopOrder  []*Top

	TermName map[string]map[string]string // type -> slug -> name (pa_types, pa_style, pa_application)
	Projects map[string]string            // project key -> label

	// Selection > Collection tree (collections.go).
	CollBySlug   map[string]*Term
	CollChildren map[int64][]int64 // by name
	CollDirect   map[int64][]int64 // term -> visible products directly on it
	collTree     map[int64][]int64 // memo: term -> products in its subtree
	collMu       sync.Mutex
}

// ---------------------------------------------------------------------------
// Build
// ---------------------------------------------------------------------------

// Build loads everything from the database and computes the index.
func Build(ctx context.Context, db *sql.DB) (*Index, error) {
	start := time.Now()
	ix := &Index{
		Terms:     map[int64]*Term{},
		CatBySlug: map[string]*Term{},
		Children:  map[int64][]int64{},
		Products:  map[int64]*Product{},
		Tops:      map[string]*Top{},
		TermName:  map[string]map[string]string{},
		Projects:  map[string]string{},
	}
	attr := map[int64]*Term{} // attribute terms (pa_*) by id

	// 1. terms
	rows, err := db.QueryContext(ctx, `SELECT id, name, slug, type, COALESCE(parent_id, 0) FROM taxonomies`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		t := &Term{}
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Type, &t.Parent); err != nil {
			rows.Close()
			return nil, err
		}
		t.Name = Decode(t.Name)
		switch {
		case t.Type == "category":
			ix.Terms[t.ID] = t
			ix.CatBySlug[t.Slug] = t
		case t.Type == "collection":
			ix.Terms[t.ID] = t
		case strings.HasPrefix(t.Type, "pa_"):
			attr[t.ID] = t
			if ix.TermName[t.Type] == nil {
				ix.TermName[t.Type] = map[string]string{}
			}
			ix.TermName[t.Type][t.Slug] = t.Name
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, t := range ix.Terms {
		if t.Type == "category" && t.Parent != 0 {
			ix.Children[t.Parent] = append(ix.Children[t.Parent], t.ID)
		}
	}

	// 2. top-level products
	rows, err = db.QueryContext(ctx, `
		SELECT id, COALESCE(sku,''), title, slug, COALESCE(image_url,''), stock_status,
		       status, catalog_visibility, COALESCE(gallery,'')
		FROM products WHERE parent_id IS NULL`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		p := &Product{}
		var stock, gallery string
		if err := rows.Scan(&p.ID, &p.SKU, &p.Title, &p.Slug, &p.Image, &stock,
			&p.Status, &p.Visibility, &gallery); err != nil {
			rows.Close()
			return nil, err
		}
		p.Title = Decode(p.Title)
		p.InStock = stock == "in_stock"
		if gallery != "" {
			_ = json.Unmarshal([]byte(gallery), &p.Gallery)
		}
		ix.Products[p.ID] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 3. taxonomy links
	rows, err = db.QueryContext(ctx, `SELECT product_id, taxonomy_id FROM product_taxonomy`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid, tid int64
		if err := rows.Scan(&pid, &tid); err != nil {
			rows.Close()
			return nil, err
		}
		p := ix.Products[pid]
		if p == nil {
			continue
		}
		if t := ix.Terms[tid]; t != nil {
			if t.Type == "category" {
				p.Cats = append(p.Cats, tid)
			} else {
				p.Colls = append(p.Colls, tid)
			}
			continue
		}
		t := attr[tid]
		if t == nil {
			continue
		}
		switch t.Type {
		case "pa_features":
			switch FeatureRole(t.Name) {
			case "outdoor":
				p.Outdoor = true
			case "flame":
				p.Flame = true
			case "project":
				label := ProjectLabel(t.Name)
				key := Norm(label)
				p.Projects = appendUniq(p.Projects, key)
				if _, ok := ix.Projects[key]; !ok {
					ix.Projects[key] = label
				}
			}
		case "pa_color":
			if f := ColorFamily(t.Name); f != "" {
				p.Colors = appendUniq(p.Colors, f)
			}
		case "pa_composition":
			p.Compositions = append(p.Compositions, t.Name)
		case "pa_types":
			p.Types = appendUniq(p.Types, t.Slug)
		case "pa_style":
			p.Styles = appendUniq(p.Styles, t.Slug)
		case "pa_application":
			if !strings.EqualFold(strings.TrimSpace(t.Name), "n/a") {
				p.Apps = appendUniq(p.Apps, t.Slug)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 4. meta: performance values and colour-version links
	rows, err = db.QueryContext(ctx, `
		SELECT product_id, meta_key, meta_value FROM product_meta
		WHERE meta_key IN ('durability','color_resistance','flame_retardant','related_series_products')`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid int64
		var key string
		var val sql.NullString
		if err := rows.Scan(&pid, &key, &val); err != nil {
			rows.Close()
			return nil, err
		}
		p := ix.Products[pid]
		if p == nil || !val.Valid {
			continue
		}
		switch key {
		case "durability":
			p.Mt = Martindale(val.String)
		case "color_resistance":
			p.Lf = Lightfastness(val.String)
		case "flame_retardant":
			p.Fire = FireStandardKeys(val.String)
		case "related_series_products":
			p.Related = parseIDList(val.String, pid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 5. round-variation photos (round rugs card / tile image)
	if err := ix.loadRoundImages(ctx, db); err != nil {
		return nil, err
	}

	// 6. collections: leaf collection, its name, the selection
	for _, p := range ix.Products {
		ix.resolveCollection(p)
	}

	// 7. Selection > Collection tree
	ix.buildCollections()

	// 8. per-top indexes
	for _, slug := range Tops {
		t := ix.CatBySlug[slug]
		if t == nil {
			continue
		}
		top := ix.buildTop(t)
		ix.Tops[slug] = top
		ix.TopOrder = append(ix.TopOrder, top)
	}

	ix.Built = time.Now()
	ix.Duration = time.Since(start)
	return ix, nil
}

func appendUniq(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// parseIDList reads an ACF relationship value: a JSON array of ids (strings
// or numbers). The product's own id is dropped and duplicates removed.
func parseIDList(v string, self int64) []int64 {
	var raw []any
	if json.Unmarshal([]byte(v), &raw) != nil {
		return nil
	}
	var out []int64
	seen := map[int64]bool{self: true}
	for _, r := range raw {
		var id int64
		switch x := r.(type) {
		case string:
			id, _ = strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		case float64:
			id = int64(x)
		}
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

var reRound = regexp.MustCompile(`(?i)^\s*(dia\b|ø|⌀|round)`)

// loadRoundImages finds, for products under Round Rugs, the first variation
// whose size option looks round. Variation titles are "Parent — opt1, opt2".
func (ix *Index) loadRoundImages(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT parent_id, title, COALESCE(image_url,'') FROM products
		WHERE parent_id IS NOT NULL AND image_url <> '' ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		var title, img string
		if err := rows.Scan(&pid, &title, &img); err != nil {
			return err
		}
		p := ix.Products[pid]
		if p == nil || p.RoundImage != "" {
			continue
		}
		_, opts, ok := strings.Cut(title, " — ")
		if !ok {
			continue
		}
		for _, o := range strings.Split(opts, ",") {
			if reRound.MatchString(o) {
				p.RoundImage = img
				break
			}
		}
	}
	return rows.Err()
}

func (ix *Index) depth(id int64) int {
	d := 0
	for t := ix.Terms[id]; t != nil && t.Parent != 0 && d < 20; t = ix.Terms[t.Parent] {
		d++
	}
	return d
}

func (ix *Index) root(id int64) *Term {
	t := ix.Terms[id]
	for i := 0; t != nil && t.Parent != 0 && i < 20; i++ {
		p := ix.Terms[t.Parent]
		if p == nil {
			break
		}
		t = p
	}
	return t
}

// resolveCollection: the deepest collection term is the leaf (ties: lower
// id); the selection is its top ancestor.
func (ix *Index) resolveCollection(p *Product) {
	best, bestDepth := int64(0), -1
	for _, id := range p.Colls {
		d := ix.depth(id)
		if d > bestDepth || (d == bestDepth && id < best) {
			best, bestDepth = id, d
		}
	}
	if best == 0 {
		return
	}
	p.LeafColl = best
	p.CollName = ix.Terms[best].Name
	if r := ix.root(best); r != nil {
		p.Selection = r.ID
	}
}

// ancestors returns the category and every ancestor (up to 15 levels).
func (ix *Index) ancestors(id int64) []int64 {
	var out []int64
	for t, i := ix.Terms[id], 0; t != nil && i < 16; i++ {
		out = append(out, t.ID)
		if t.Parent == 0 {
			break
		}
		t = ix.Terms[t.Parent]
	}
	return out
}

func (ix *Index) underTop(catID int64, top *Term) bool {
	r := ix.root(catID)
	return r != nil && r.ID == top.ID
}

// chainHasName reports whether the category or an ancestor is named name.
func (ix *Index) chainHasName(catID int64, name string) bool {
	for _, a := range ix.ancestors(catID) {
		if strings.EqualFold(ix.Terms[a].Name, name) {
			return true
		}
	}
	return false
}

// rec computes a product's facet record under a top.
func (ix *Index) rec(p *Product, top string) *Rec {
	r := &Rec{P: p, Colors: append([]string(nil), p.Colors...)}
	for _, c := range p.Compositions {
		for _, f := range MaterialFamilies(top, c) {
			r.Mat = appendUniq(r.Mat, f)
		}
	}
	return r
}

func (ix *Index) buildTop(t *Term) *Top {
	top := &Top{
		Term: t, Slug: t.Slug,
		Recs:     map[int64]*Rec{},
		InCat:    map[int64][]int64{},
		Count:    map[int64]int{},
		Versions: map[int64][]int64{},
		Fin:      map[int64]*Rec{},
		virtual:  map[int64]bool{},
	}

	// Fabrics tiles filled by rule.
	var drapery, upholstery, linens, trimmings, decorative, tableLinens, bedLinens int64
	if t.Slug == "fabrics" {
		id := func(slug string) int64 {
			if c := ix.CatBySlug[slug]; c != nil {
				return c.ID
			}
			return 0
		}
		drapery, upholstery, linens = id("drapery-fabrics"), id("upholstery-fabrics"), id("linens-fabrics")
		trimmings, decorative = id("trimmings"), id("decorative-accessories-2")
		tableLinens, bedLinens = id("table-linens"), id("bed-linens")
		for _, v := range []int64{drapery, upholstery, linens} {
			if v != 0 {
				top.virtual[v] = true
			}
		}
	}

	// Membership and per-category product sets.
	ids := make([]int64, 0, 1024)
	for _, p := range ix.Products {
		if p.Hidden() {
			continue
		}
		in := false
		for _, c := range p.Cats {
			if ix.underTop(c, t) {
				in = true
				break
			}
		}
		if in {
			ids = append(ids, p.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, pid := range ids {
		p := ix.Products[pid]
		top.Recs[pid] = ix.rec(p, t.Slug)

		a := map[int64]bool{}
		for _, c := range p.Cats {
			if !ix.underTop(c, t) {
				continue
			}
			for _, x := range ix.ancestors(c) {
				a[x] = true
			}
		}
		if t.Slug == "fabrics" {
			excluded := (trimmings != 0 && a[trimmings]) || (decorative != 0 && a[decorative])
			if excluded {
				delete(a, drapery)
				delete(a, upholstery)
				delete(a, linens)
			} else {
				apps := set(p.Apps...)
				if drapery != 0 && (apps["curtain"] || apps["transparent"] || apps["dimout"] || apps["blackout"] || apps["drapery"]) {
					a[drapery] = true
				}
				if upholstery != 0 && apps["upholstery"] {
					a[upholstery] = true
				}
				if linens != 0 && (apps["linens"] || apps["tableware"] || (tableLinens != 0 && a[tableLinens]) || (bedLinens != 0 && a[bedLinens])) {
					a[linens] = true
				}
			}
		}
		for c := range a {
			top.InCat[c] = append(top.InCat[c], pid)
		}
	}
	for c, list := range top.InCat {
		top.Count[c] = len(list)
	}

	ix.buildVersions(top)

	// Card order within every category: collection name, then id (stable).
	for c := range top.InCat {
		ix.sortCards(top.InCat[c])
	}
	return top
}

// buildVersions links colour versions to their lead and rolls swatch colours
// up into the lead (#200 L60358-505).
func (ix *Index) buildVersions(top *Top) {
	ids := make([]int64, 0, len(top.Recs))
	for id := range top.Recs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, lid := range ids {
		lead := top.Recs[lid]
		lp := lead.P
		if len(lp.Related) == 0 {
			continue
		}
		leadNames := map[string]bool{}
		for _, c := range lp.Cats {
			leadNames[Norm(ix.Terms[c].Name)] = true
		}
		leadKey := Norm(lp.CollName) + "|" + strconv.FormatInt(lp.Selection, 10)
		swatched := false

		for _, fid := range lp.Related {
			fp := ix.Products[fid]
			// Staff index: published and private finishes both count.
			if fp == nil || (fp.Status != "publish" && fp.Status != "private") {
				continue
			}
			if _, visible := top.Recs[fid]; visible {
				continue // its own card
			}
			fin := ix.rec(fp, top.Slug)

			shared := false
			underFinishes := false
			otherTop := false
			for _, c := range fp.Cats {
				if leadNames[Norm(ix.Terms[c].Name)] {
					shared = true
				}
				if ix.chainHasName(c, "finishes") {
					underFinishes = true
				}
				if !ix.underTop(c, top.Term) {
					otherTop = true
				}
			}

			swatch := false
			switch {
			case swatchForceTops[top.Slug] && underFinishes:
				swatch = true
			case !shared:
				swatch = true
			default:
				key := Norm(fp.CollName) + "|" + strconv.FormatInt(fp.Selection, 10)
				if key != leadKey {
					continue // dropped
				}
				// colour version
				top.Fin[fid] = fin
				top.Versions[lid] = append(top.Versions[lid], fid)
				continue
			}

			if swatch && swatchTops[top.Slug] && (otherTop || underFinishes) && len(fin.Colors) > 0 {
				if !swatched {
					lead.Colors = remove(lead.Colors, "other")
					swatched = true
				}
				for _, c := range fin.Colors {
					if c != "other" {
						lead.Colors = appendUniq(lead.Colors, c)
					}
				}
			}
		}
	}
}

func remove(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// collGroup: 0 = starts with a letter, 1 = digit/symbol, 2 = no collection.
func collGroup(name string) int {
	if name == "" {
		return 2
	}
	for _, r := range name {
		if unicode.IsLetter(r) {
			return 0
		}
		return 1
	}
	return 2
}

// sortCards orders product ids by leaf collection name (letters first, then
// digits/symbols, then none; natural case-insensitive), then title, then id.
func (ix *Index) sortCards(ids []int64) {
	sort.SliceStable(ids, func(i, j int) bool {
		a, b := ix.Products[ids[i]], ix.Products[ids[j]]
		ga, gb := collGroup(a.CollName), collGroup(b.CollName)
		if ga != gb {
			return ga < gb
		}
		if c := natCmp(a.CollName, b.CollName); c != 0 {
			return c < 0
		}
		if c := natCmp(a.Title, b.Title); c != 0 {
			return c < 0
		}
		return a.ID < b.ID
	})
}

// ---------------------------------------------------------------------------
// Category tree helpers used by the pages
// ---------------------------------------------------------------------------

// TopOf returns the top index a category belongs to, or nil.
func (ix *Index) TopOf(cat *Term) *Top {
	r := ix.root(cat.ID)
	if r == nil {
		return nil
	}
	return ix.Tops[r.Slug]
}

// hiddenBranch: in Fabrics only the five tiles are visible children.
func (top *Top) hiddenBranch(ix *Index, c *Term) bool {
	if top.Slug != "fabrics" || c.Parent != top.Term.ID {
		return false
	}
	for _, s := range fabricTiles {
		if c.Slug == s {
			return false
		}
	}
	return true
}

// IsLeaf reports whether the category renders a product grid.
func (ix *Index) IsLeaf(top *Top, cat *Term) bool {
	if cat.ID == top.Term.ID && flatTops[top.Slug] {
		return true
	}
	if top.Slug == "fabrics" && cat.Parent == top.Term.ID {
		return true // the five Fabrics tiles are product pages
	}
	if p := ix.Terms[cat.Parent]; p != nil && p.Slug == "sofas" && ix.underTop(cat.ID, ix.CatBySlug["furniture"]) {
		if sofaCollapse[Norm(cat.Name)] {
			return true
		}
	}
	return len(ix.Tiles(top, cat)) == 0
}

// Tiles returns the child categories shown as tiles (count > 0, natural
// order), applying the single-child pass-through where enabled.
func (ix *Index) Tiles(top *Top, cat *Term) []*Term {
	var out []*Term
	for _, cid := range ix.Children[cat.ID] {
		c := ix.Terms[cid]
		if top.Count[cid] == 0 || top.hiddenBranch(ix, c) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return natLess(out[i].Name, out[j].Name) })
	if passthroughTops[top.Slug] && len(out) == 1 && len(ix.Children[out[0].ID]) > 0 {
		if sub := ix.Tiles(top, out[0]); len(sub) > 0 {
			return sub
		}
	}
	return out
}

// TileImage: the first product inside the category (by id) with an image;
// the round-variation photo on round-rug categories.
func (ix *Index) TileImage(top *Top, cat *Term) string {
	ids := append([]int64(nil), top.InCat[cat.ID]...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	round := ix.IsRound(cat)
	for _, id := range ids {
		p := ix.Products[id]
		if round && p.RoundImage != "" {
			return p.RoundImage
		}
		if p.Image != "" {
			return p.Image
		}
	}
	return ""
}

// IsRound: a Round Rugs category (or below) on Floor Coverings.
func (ix *Index) IsRound(cat *Term) bool {
	for _, a := range ix.ancestors(cat.ID) {
		if ix.Terms[a].Slug == "round-rugs" {
			return true
		}
	}
	return false
}

// Paged reports a leaf loaded PageSize cards at a time.
func Paged(cat *Term) bool { return pagedLeaves[cat.Slug] }

// Breadcrumbs from the top down to cat.
func (ix *Index) Breadcrumbs(cat *Term) []*Term {
	ids := ix.ancestors(cat.ID)
	out := make([]*Term, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		out = append(out, ix.Terms[ids[i]])
	}
	return out
}

// Summary is a one-line description for logs.
func (ix *Index) Summary() string {
	var parts []string
	for _, t := range ix.TopOrder {
		parts = append(parts, fmt.Sprintf("%s=%d", t.Slug, len(t.Recs)))
	}
	return fmt.Sprintf("%d products indexed in %s (%s)", len(ix.Products), ix.Duration.Round(time.Millisecond), strings.Join(parts, " "))
}
