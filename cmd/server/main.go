package main

import (
	"html/template"
	"log"
	"net/http"
	"strings"
	"unicode"

	"bigtree-products/internal/config"
	"bigtree-products/internal/database"
	"bigtree-products/internal/handlers"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()

	db, err := database.Connect(cfg.DSN)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	h := handlers.New(db, cfg)

	r := gin.Default()
	r.SetFuncMap(templateFuncs())
	r.LoadHTMLGlob("templates/*.html")
	r.Static("/static", "./static")

	// --- public routes ------------------------------------------------------
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusSeeOther, "/products") })
	r.GET("/login", h.ShowLogin)
	r.POST("/login", h.Login)
	r.POST("/logout", h.Logout)

	// --- authenticated storefront ------------------------------------------
	auth := r.Group("/")
	auth.Use(h.RequireAuth(false))
	{
		auth.GET("/products", h.Catalog)
		auth.GET("/product/:slug", h.ProductDetail)
	}

	// --- authenticated JSON API --------------------------------------------
	api := r.Group("/api/v1")
	api.Use(h.RequireAuth(true))
	{
		api.GET("/products", h.APIProducts)
	}

	log.Printf("bigtree storefront listening on %s", cfg.ListenAddr)
	if err := r.Run(cfg.ListenAddr); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// templateFuncs exposes small helpers used by the HTML templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// contains reports whether slug is in the selected slice (sidebar ticks).
		"contains": func(list []string, slug string) bool {
			for _, s := range list {
				if s == slug {
					return true
				}
			}
			return false
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		// safeHTML renders first-party WooCommerce rich text (product
		// descriptions) as HTML on the staff-only dashboard. Only ever feed it
		// content that originates from the store, never user input.
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		// label prettifies a taxonomy type or ACF key for display, e.g.
		// "pa_color" -> "Color", "minimum_order_quantity" -> "Minimum Order Quantity".
		"label": prettyLabel,
	}
}

// prettyLabel turns a machine key into a human heading.
func prettyLabel(s string) string {
	s = strings.TrimPrefix(s, "pa_")
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	fields := strings.Fields(s)
	for i, w := range fields {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		fields[i] = string(r)
	}
	return strings.Join(fields, " ")
}
