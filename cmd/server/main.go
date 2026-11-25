package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"bigtree-products/internal/catalog"
	"bigtree-products/internal/config"
	"bigtree-products/internal/database"
	"bigtree-products/internal/handlers"
	"bigtree-products/internal/services"

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

	quotationSvc, err := services.NewQuotationService(context.Background(),
		cfg.GoogleCredentialsFile, cfg.GoogleTemplateSpreadsheetID, cfg.GoogleQuotesFolderID)
	if err != nil {
		log.Fatalf("quotation service: %v", err)
	}
	quotationHandler := handlers.NewQuotationHandler(db, quotationSvc)

	// In-memory catalog index behind the category pages: built in the
	// background at startup, rebuilt when a sync changes the data.
	index := catalog.NewHolder(db)
	index.Start(context.Background(), 5*time.Minute)
	categories := handlers.NewCategoryHandler(h, index, "templates")

	r := gin.Default()
	r.SetFuncMap(handlers.TemplateFuncs())
	r.LoadHTMLGlob("templates/*.html")
	r.Static("/static", "./static")

	// 404 for any unmatched route.
	r.NoRoute(h.NotFound)

	// --- public routes ------------------------------------------------------
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusSeeOther, "/categories") })
	r.GET("/login", h.ShowLogin)
	r.POST("/login", h.Login)
	r.POST("/logout", h.Logout)

	// --- authenticated storefront ------------------------------------------
	auth := r.Group("/")
	auth.Use(h.RequireAuth(false))
	{
		auth.GET("/categories", categories.Categories)
		auth.GET("/category/:slug", categories.Category)
		auth.GET("/selections", categories.AllSelections)
		auth.GET("/collection/:slug", categories.Collection) // Selection grid or leaf collection
		auth.GET("/products", h.Catalog)                     // search across every product
		auth.GET("/product/:slug", h.ProductDetail)
		auth.POST("/product/:slug/quote", quotationHandler.GenerateProductQuotation)
	}

	// --- authenticated JSON API --------------------------------------------
	api := r.Group("/api/v1")
	api.Use(h.RequireAuth(true))
	{
		api.GET("/products", h.APIProducts)
		api.GET("/category/:slug", categories.CategoryAPI)
	}

	log.Printf("bigtree storefront listening on %s", cfg.ListenAddr)
	if err := r.Run(cfg.ListenAddr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
