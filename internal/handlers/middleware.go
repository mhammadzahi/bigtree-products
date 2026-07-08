package handlers

import (
	"errors"
	"net/http"

	"bigtree-products/internal/models"

	"github.com/gin-gonic/gin"
)

const ctxUserKey = "currentUser"

// RequireAuth protects the catalog routes. HTML routes redirect to /login;
// API/XHR routes get a 401 JSON body so the frontend fetch can react cleanly.
func (h *Handler) RequireAuth(apiRoute bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookie)
		if err == nil {
			userID, err := models.UserIDForSession(c.Request.Context(), h.DB, token)
			if err == nil {
				if user, err := models.GetUserByID(c.Request.Context(), h.DB, userID); err == nil {
					c.Set(ctxUserKey, user)
					c.Next()
					return
				}
			}
		}

		if apiRoute {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Redirect(http.StatusSeeOther, "/login")
		c.Abort()
	}
}

// currentUser pulls the authenticated user set by RequireAuth.
func currentUser(c *gin.Context) (*models.User, error) {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return nil, errors.New("no user in context")
	}
	u, ok := v.(*models.User)
	if !ok {
		return nil, errors.New("invalid user in context")
	}
	return u, nil
}
