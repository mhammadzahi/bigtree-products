package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"bigtree-products/internal/models"

	"github.com/gin-gonic/gin"
)

// Catalog renders the full two-column storefront (server-side rendered so the
// first paint needs no JS). Subsequent filtering is handled by the JSON API.
func (h *Handler) Catalog(c *gin.Context) {
	user, _ := currentUser(c)
	filter := parseFilter(c)

	// Facets are computed over the current filter so options always yield results.
	filters, err := models.LoadFacets(c.Request.Context(), h.DB, filter)
	if err != nil {
		c.String(http.StatusInternalServerError, "failed to load filters")
		return
	}
	page, err := models.QueryProducts(c.Request.Context(), h.DB, filter)
	if err != nil {
		c.String(http.StatusInternalServerError, "failed to load products")
		return
	}

	c.HTML(http.StatusOK, "catalog.html", gin.H{
		"Title":    "Product catalog",
		"User":     user,
		"Filters":  filters,
		"Result":   page,
		"Selected": filter, // pre-tick the sidebar from the URL query
	})
}

// APIProducts is the async endpoint the sidebar/search hit via fetch(). It
// returns the paginated result set plus the recomputed facets so the client can
// re-render both the grid and the filter options.
func (h *Handler) APIProducts(c *gin.Context) {
	filter := parseFilter(c)
	page, err := models.QueryProducts(c.Request.Context(), h.DB, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	if facets, err := models.LoadFacets(c.Request.Context(), h.DB, filter); err == nil {
		page.Facets = facets
	}
	c.JSON(http.StatusOK, page)
}

// ProductDetail renders the full product-info dashboard for one product.
func (h *Handler) ProductDetail(c *gin.Context) {
	user, _ := currentUser(c)
	product, err := models.GetProductFull(c.Request.Context(), h.DB, c.Param("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		c.HTML(http.StatusNotFound, "notfound.html", gin.H{"Title": "Not found", "User": user})
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, "failed to load product")
		return
	}
	c.HTML(http.StatusOK, "product.html", gin.H{
		"Title": product.Title, "User": user, "Product": product,
	})
}
