package middleware

import (
	"net/http"
	"strings"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/auth"
	"github.com/gin-gonic/gin"
)

const UserIDKey = "user_id"

func Authentication(authenticator *auth.JWTAuthenticator,) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")

		if header == "" {
			c.AbortWithStatusJSON(
				http.StatusUnauthorized,
				gin.H{
					"error": "missing_authentication",
				},
			)
			return
		}

		parts := strings.Fields(header)

		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(
				http.StatusUnauthorized,
				gin.H{
					"error": "invalid_authorization_header",
				},
			)
			return
		}

		userID, err := authenticator.Authenticate(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(
				http.StatusUnauthorized,
				gin.H{
					"error": "invalid_authentication",
				},
			)
			return
		}

		c.Set(UserIDKey, userID)

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