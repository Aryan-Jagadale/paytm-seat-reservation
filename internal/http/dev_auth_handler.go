package http

import (
	"net/http"
	"strings"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/auth"
	"github.com/gin-gonic/gin"
)

type DevAuthHandler struct {
	authenticator *auth.JWTAuthenticator
}

func NewDevAuthHandler(authenticator *auth.JWTAuthenticator) *DevAuthHandler {
	return &DevAuthHandler{authenticator: authenticator}
}

type devTokenRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

func (h *DevAuthHandler) GenerateToken(c *gin.Context) {
	var req devTokenRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	userID := strings.TrimSpace(req.UserID)
	role := strings.TrimSpace(req.Role)

	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "user_id is required",
		})
		return
	}

	if role != "user" && role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "role must be user or admin",
		})
		return
	}

	token, err := h.authenticator.GenerateToken(userID, role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to generate token",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
	})
}