package middleware

import (
	"net/http"
	"strings"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/auth"
	"github.com/gin-gonic/gin"
)

const (
	UserIDKey = "user_id"
	RoleKey   = "role"
)

func Authentication(authenticator *auth.JWTAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")

		if header == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "missing_authentication",
			})
			c.Abort()
			return
		}

		parts := strings.Fields(header)

		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "invalid_authorization_header",
			})
			c.Abort()
			return
		}

		userID, role, err := authenticator.Authenticate(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "invalid_authentication",
			})
			c.Abort()
			return
		}

		c.Set(UserIDKey, userID)
		c.Set(RoleKey, role)

		c.Next()
	}
}

func GetUserID(c *gin.Context) (string, bool) {
	value, exists := c.Get(UserIDKey)
	if !exists {
		return "", false
	}

	userID, ok := value.(string)
	return userID, ok
}

func GetRole(c *gin.Context) (string, bool) {
	value, exists := c.Get(RoleKey)
	if !exists {
		return "", false
	}

	role, ok := value.(string)
	return role, ok
}

func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := GetRole(c)

		if !ok || role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "admin_required",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
