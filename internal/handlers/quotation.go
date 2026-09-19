package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"bigtree-products/internal/models"
	"bigtree-products/internal/services"

	"github.com/gin-gonic/gin"
)

// QuotationHandler generates PDF quotations for a single product via the
// Google Sheets template.
type QuotationHandler struct {
	DB        *sql.DB
	Quotation *services.QuotationService
}

func NewQuotationHandler(db *sql.DB, qs *services.QuotationService) *QuotationHandler {
	return &QuotationHandler{DB: db, Quotation: qs}
}

// GenerateProductQuotation fills a copy of the master quotation template for
// one product and streams the resulting PDF back, or returns its Sheets URL
// when called with ?format=json.
func (h *QuotationHandler) GenerateProductQuotation(c *gin.Context) {
	product, err := models.GetProductBySlug(c.Request.Context(), h.DB, c.Param("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "product not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load product"})
		return
	}

	buyerCompany := c.PostForm("buyer_company")
	buyerContact := c.PostForm("buyer_contact")
	if buyerCompany == "" || buyerContact == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "buyer_company and buyer_contact are required"})
		return
	}

	quantity, err := strconv.ParseFloat(c.DefaultPostForm("quantity", "1"), 64)
	if err != nil || quantity <= 0 {
		quantity = 1
	}
	unit := c.DefaultPostForm("unit", "PCS")
	unitPrice, err := strconv.ParseFloat(c.PostForm("unit_price"), 64)
	if err != nil {
		unitPrice = product.Price
	}

	now := time.Now()
	quoteNumber := fmt.Sprintf("BT-%s-%04d", now.Format("20060102"), now.Unix()%10000)

	result, err := h.Quotation.GenerateQuote(c.Request.Context(), services.QuoteRequest{
		QuoteNumber:  quoteNumber,
		Date:         now.Format("2006-01-02"),
		BuyerCompany: buyerCompany,
		BuyerContact: buyerContact,
		BuyerProject: c.PostForm("buyer_project"),
		BuyerPhone:   c.PostForm("buyer_phone"),
		BuyerEmail:   c.PostForm("buyer_email"),
		ProductName:  product.Title,
		SKU:          product.SKU,
		ImageURL:     product.ImageURL,
		Quantity:     quantity,
		Unit:         unit,
		UnitPrice:    unitPrice,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate quotation"})
		return
	}

	if c.Query("format") == "json" {
		c.JSON(http.StatusOK, gin.H{
			"quote_number":    quoteNumber,
			"spreadsheet_url": result.EditURL,
		})
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="Quotation-%s.pdf"`, quoteNumber))
	c.Data(http.StatusOK, "application/pdf", result.PDF)
}
