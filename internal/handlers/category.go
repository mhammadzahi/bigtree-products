package handlers

import (
	"bytes"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"bigtree-products/internal/catalog"

	"github.com/gin-gonic/gin"
)

// CategoryHandler serves the category tile pages, leaf grids and their
// filter API from the in-memory catalog index.
type CategoryHandler struct {
	h     *Handler
	index *catalog.Holder
	parts *template.Template // bar / grid fragments for the JSON API
}

func NewCategoryHandler(h *Handler, index *catalog.Holder, templatesDir string) *CategoryHandler {
	parts := template.Must(template.New("").Funcs(TemplateFuncs()).
		ParseFiles(filepath.Join(templatesDir, "catalog_parts.html")))
	return &CategoryHandler{h: h, index: index, parts: parts}
}

// Tile is one category tile.
type Tile struct {
	Name  string
	Slug  string
	Image string
	Count int
}

// CardView is everything a product card renders.
type CardView struct {
	ID      int64
	Title   string
	Slug    string
	Image   string
	Hover   string
	Coll    string
	Outdoor bool
	Private bool
}

// leafData is shared by the page and the API.
type leafData struct {
	Cat     *catalog.Term
	Top     *catalog.Top
	Filter  catalog.Filter
	Query   string
	Bar     catalog.Bar
	Cards   []CardView
	Total   int
	Paged   bool
	Page    int
	HasMore bool
}

func (ch *CategoryHandler) indexOr503(c *gin.Context, api bool) *catalog.Index {
	ix := ch.index.Get()
	if ix == nil {
		if api {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "catalog index is building"})
		} else {
			user, _ := currentUser(c)
			c.HTML(http.StatusServiceUnavailable, "notfound.html", gin.H{
				"Title": "Catalog loading", "User": user, "Code": "…", "Heading": "Catalog loading",
				"Message": "The catalog index is being built. Please refresh in a minute.",
			})
		}
	}
	return ix
}

// Categories renders the top-level tiles.
func (ch *CategoryHandler) Categories(c *gin.Context) {
	ix := ch.indexOr503(c, false)
	if ix == nil {
		return
	}
	user, _ := currentUser(c)
	var tiles []Tile
	for _, top := range ix.TopOrder {
		tiles = append(tiles, Tile{Name: top.Term.Name, Slug: top.Slug,
			Image: ix.TileImage(top, top.Term), Count: len(top.Recs)})
	}
	c.HTML(http.StatusOK, "category.html", gin.H{
		"Title": "Categories", "User": user, "Tiles": tiles, "Heading": "Categories",
	})
}

func (ch *CategoryHandler) resolve(c *gin.Context, ix *catalog.Index) (*catalog.Term, *catalog.Top) {
	cat := ix.CatBySlug[c.Param("slug")]
	if cat == nil {
		return nil, nil
	}
	top := ix.TopOf(cat)
	if top == nil || top.Count[cat.ID] == 0 {
		return nil, nil
	}
	return cat, top
}

// Category renders a tile page or a leaf grid.
func (ch *CategoryHandler) Category(c *gin.Context) {
	ix := ch.indexOr503(c, false)
	if ix == nil {
		return
	}
	user, _ := currentUser(c)
	cat, top := ch.resolve(c, ix)
	if cat == nil {
		c.HTML(http.StatusNotFound, "notfound.html", gin.H{
			"Title": "Not found", "User": user, "Message": "That category could not be found.",
		})
		return
	}
	data := gin.H{
		"Title": cat.Name, "User": user, "Heading": cat.Name,
		"Crumbs": ix.Breadcrumbs(cat),
	}
	if !ix.IsLeaf(top, cat) {
		var tiles []Tile
		for _, t := range ix.Tiles(top, cat) {
			tiles = append(tiles, Tile{Name: t.Name, Slug: t.Slug, Image: ix.TileImage(top, t), Count: top.Count[t.ID]})
		}
		data["Tiles"] = tiles
		c.HTML(http.StatusOK, "category.html", data)
		return
	}
	ld := ch.leaf(ix, top, cat, catalog.ParseFilter(c.Request.URL.Query()), 1, 1)
	data["Leaf"] = ld
	c.HTML(http.StatusOK, "category.html", data)
}

// CategoryAPI returns the filtered bar + grid as HTML fragments:
//
//	GET /api/v1/category/:slug?f_...        -> {count, bar, grid, more}
//	GET /api/v1/category/:slug?f_...&page=N -> next page of cards only
//	GET /api/v1/category/:slug?f_...&upto=N -> pages 1..N (Back restore)
func (ch *CategoryHandler) CategoryAPI(c *gin.Context) {
	ix := ch.indexOr503(c, true)
	if ix == nil {
		return
	}
	cat, top := ch.resolve(c, ix)
	if cat == nil || !ix.IsLeaf(top, cat) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not a product category"})
		return
	}
	f := catalog.ParseFilter(c.Request.URL.Query())
	page, _ := strconv.Atoi(c.Query("page"))
	upto, _ := strconv.Atoi(c.Query("upto"))
	from, to := 1, 1
	switch {
	case page > 1:
		from, to = page, page
	case upto > 1:
		to = min(upto, 30)
	}
	ld := ch.leaf(ix, top, cat, f, from, to)

	var grid bytes.Buffer
	if err := ch.parts.ExecuteTemplate(&grid, "cat-cards", ld); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "render failed"})
		return
	}
	resp := gin.H{"count": ld.Total, "grid": grid.String(), "more": ld.HasMore, "page": ld.Page, "query": ld.Query}
	if page <= 1 {
		var bar bytes.Buffer
		if err := ch.parts.ExecuteTemplate(&bar, "cat-bar", ld); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "render failed"})
			return
		}
		resp["bar"] = bar.String()
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// leaf computes the cards (pages from..to for paged leaves) and the bar.
func (ch *CategoryHandler) leaf(ix *catalog.Index, top *catalog.Top, cat *catalog.Term, f catalog.Filter, from, to int) *leafData {
	cards := top.Cards(cat.ID, f)
	ld := &leafData{Cat: cat, Top: top, Filter: f, Query: f.Query(), Total: len(cards), Paged: catalog.Paged(cat), Page: to}
	lo, hi := 0, len(cards)
	if ld.Paged {
		lo = min((from-1)*catalog.PageSize, len(cards))
		hi = min(to*catalog.PageSize, len(cards))
		ld.HasMore = hi < len(cards)
	}
	if from <= 1 {
		ld.Bar = ix.Bar(top, cat, f)
	}
	round := ix.IsRound(cat)
	for _, cd := range cards[lo:hi] {
		ld.Cards = append(ld.Cards, cardView(ix, cd, round))
	}
	return ld
}

func cardView(ix *catalog.Index, cd catalog.Card, round bool) CardView {
	p := cd.Rec.P
	lead := ix.Products[cd.Of]
	v := CardView{ID: p.ID, Title: p.Title, Slug: p.Slug, Image: p.Image, Private: p.Private(),
		Outdoor: p.Outdoor || (lead != nil && lead.Outdoor)}
	if round && p.RoundImage != "" {
		v.Image = p.RoundImage
	}
	if v.Image == "" && lead != nil {
		v.Image = lead.Image // colour version without its own photo
	}
	if lead != nil {
		v.Coll = lead.CollName
	}
	for _, g := range p.Gallery {
		if g != "" && g != v.Image {
			v.Hover = g
			break
		}
	}
	return v
}

// DownloadName is BigTree_<name>_<id> with unsafe characters removed.
func DownloadName(title string, id int64) string {
	var b strings.Builder
	for _, r := range strings.Join(strings.Fields(title), "_") {
		if r == '_' || r == '-' || (r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))) {
			b.WriteRune(r)
		}
	}
	return "BigTree_" + b.String() + "_" + strconv.FormatInt(id, 10)
}
