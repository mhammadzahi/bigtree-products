package handlers

import (
	"errors"
	"net/http"

	"bigtree-products/internal/models"

	"github.com/gin-gonic/gin"
)

// ShowLogin renders the login page.
func (h *Handler) ShowLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", gin.H{"Title": "Sign in"})
}

// ShowRegister renders the registration page.
func (h *Handler) ShowRegister(c *gin.Context) {
	c.HTML(http.StatusOK, "register.html", gin.H{"Title": "Create account"})
}

// Register creates a buyer account then logs the user straight in.
func (h *Handler) Register(c *gin.Context) {
	email := c.PostForm("email")
	password := c.PostForm("password")

	if len(password) < 8 {
		c.HTML(http.StatusBadRequest, "register.html", gin.H{
			"Title": "Create account",
			"Error": "Password must be at least 8 characters.",
			"Email": email,
		})
		return
	}

	user, err := models.CreateUser(c.Request.Context(), h.DB, email, password, "buyer")
	if err != nil {
		msg := "Could not create account."
		if errors.Is(err, models.ErrEmailTaken) {
			msg = "That email is already registered."
		}
		c.HTML(http.StatusBadRequest, "register.html", gin.H{
			"Title": "Create account", "Error": msg, "Email": email,
		})
		return
	}

	h.startSession(c, user, "/products")
}

// Login authenticates and starts a session.
func (h *Handler) Login(c *gin.Context) {
	email := c.PostForm("email")
	password := c.PostForm("password")

	user, err := models.Authenticate(c.Request.Context(), h.DB, email, password)
	if err != nil {
		c.HTML(http.StatusUnauthorized, "login.html", gin.H{
			"Title": "Sign in", "Error": "Invalid email or password.", "Email": email,
		})
		return
	}

	h.startSession(c, user, "/products")
}

// startSession creates the DB session, sets the cookie and redirects.
func (h *Handler) startSession(c *gin.Context, user *models.User, redirect string) {
	token, err := models.CreateSession(c.Request.Context(), h.DB, user.ID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"Title": "Sign in", "Error": "Could not start a session. Try again.",
		})
		return
	}
	h.setSessionCookie(c, token)
	c.Redirect(http.StatusSeeOther, redirect)
}

// Logout revokes the session and clears the cookie.
func (h *Handler) Logout(c *gin.Context) {
	if token, err := c.Cookie(sessionCookie); err == nil {
		_ = models.DeleteSession(c.Request.Context(), h.DB, token)
	}
	h.clearSessionCookie(c)
	c.Redirect(http.StatusSeeOther, "/login")
}
