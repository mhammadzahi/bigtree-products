package handlers

import (
	"net/http"
	"strings"

	"bigtree-products/internal/catalog"

	"github.com/gin-gonic/gin"
)

// Selection / Collection pages ("Path A"). Filtering on these pages is done
// in the browser over the cards already rendered (static/js/selection.js),
// like the live site: pills and the Outdoor switch only show or hide cards.

// selectionCardView is one card on All Selections or a Selection grid.
type selectionCardView struct {
	Name    string
	Slug    string
	Image   string
	Labels  string // categories under the name
	Slugs   string // space-separated pill slugs (data attribute)
	Outdoor bool
}

func selectionCards(cards []catalog.SelectionCard) ([]selectionCardView, int) {
	out := make([]selectionCardView, 0, len(cards))
	outdoor := 0
	for _, c := range cards {
		if c.Outdoor {
			outdoor++
		}
		out = append(out, selectionCardView{
			Name: c.Term.Name, Slug: c.Term.Slug, Image: c.Image,
			Labels: strings.Join(c.Labels, ", "), Slugs: strings.Join(c.Slugs, " "), Outdoor: c.Outdoor,
		})
	}
	return out, outdoor
}

func pillViews(pills []catalog.Pill) []gin.H {
	out := make([]gin.H, 0, len(pills))
	for _, p := range pills {
		out = append(out, gin.H{"Label": p.Label, "Slugs": strings.Join(p.Slugs, " ")})
	}
	return out
}

// AllSelections renders every Selection with department pills (AND).
func (ch *CategoryHandler) AllSelections(c *gin.Context) {
	ix := ch.indexOr503(c, false)
	if ix == nil {
		return
	}
	user, _ := currentUser(c)
	cards, pills := ix.AllSelections()
	views, outdoor := selectionCards(cards)
	c.HTML(http.StatusOK, "selection.html", gin.H{
		"Title": "All Selections", "User": user, "Heading": "All Selections",
		"Mode": "all", "Cards": views, "Pills": pillViews(pills), "OutdoorCount": outdoor,
		"ShowBar": len(pills) > 1 || outdoor > 0,
	})
}

// Collection renders a Selection grid (term with children) or a leaf
// collection's products.
func (ch *CategoryHandler) Collection(c *gin.Context) {
	ix := ch.indexOr503(c, false)
	if ix == nil {
		return
	}
	user, _ := currentUser(c)
	term := ix.CollBySlug[c.Param("slug")]
	if term == nil || len(ix.CollProducts(term.ID)) == 0 {
		c.HTML(http.StatusNotFound, "notfound.html", gin.H{
			"Title": "Not found", "User": user, "Message": "That selection or collection could not be found.",
		})
		return
	}
	data := gin.H{
		"Title": term.Name, "User": user, "Heading": term.Name,
		"Crumbs": ix.CollCrumbs(term.ID),
	}

	if ix.HasChildren(term.ID) {
		cards, pills := ix.SelectionGrid(term.ID)
		views, outdoor := selectionCards(cards)
		data["Mode"] = "grid"
		data["Cards"] = views
		data["Pills"] = pillViews(pills)
		data["OutdoorCount"] = outdoor
		data["ShowBar"] = len(pills) > 1 || outdoor > 0
		c.HTML(http.StatusOK, "selection.html", data)
		return
	}

	products, pills := ix.CollectionProducts(term.ID)
	type productView struct {
		Card  CardView
		Slugs string
	}
	views := make([]productView, 0, len(products))
	for _, cp := range products {
		cd := catalog.Card{Rec: &catalog.Rec{P: cp.P}, Of: cp.P.ID}
		views = append(views, productView{Card: cardView(ix, cd, false), Slugs: strings.Join(cp.Slugs, " ")})
	}
	data["Mode"] = "products"
	data["Products"] = views
	data["Pills"] = pillViews(pills)
	data["ShowBar"] = len(pills) > 1
	data["Total"] = len(views)
	c.HTML(http.StatusOK, "selection.html", data)
}
