package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"bigtree-products/internal/config"
	"bigtree-products/internal/models"

	"github.com/gin-gonic/gin"
)

const sessionCookie = "bt_session"

// Handler bundles the shared dependencies every HTTP handler needs.
type Handler struct {
	DB  *sql.DB
	Cfg config.Config
}

func New(db *sql.DB, cfg config.Config) *Handler {
	return &Handler{DB: db, Cfg: cfg}
}

// setSessionCookie writes the hardened session cookie.
//   - HttpOnly: not reachable from JS (XSS-resistant)
//   - SameSite=Strict: not sent on cross-site requests (CSRF-resistant)
//   - Secure: only over HTTPS (enabled via config in production)
func (h *Handler) setSessionCookie(c *gin.Context, token string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(models.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.Cfg.SecureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.Cfg.SecureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

// parseFilter maps the request query string onto a models.ProductFilter.
func parseFilter(c *gin.Context) models.ProductFilter {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	return models.ProductFilter{
		Search:       strings.TrimSpace(c.Query("s")),
		Collection:   c.Query("collection"),
		Categories:   c.QueryArray("category"),
		Brands:       c.QueryArray("brand"),
		Applications: c.QueryArray("pa_application"),
		Colors:       c.QueryArray("pa_color"),
		Compositions: c.QueryArray("pa_composition"),
		Features:     c.QueryArray("pa_features"),
		OrderBy:      c.Query("orderby"),
		Page:         page,
	}
}
