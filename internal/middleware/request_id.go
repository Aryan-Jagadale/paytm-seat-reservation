package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

type requestIDKey struct{}

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")

		if !isValidRequestID(id) {
			var bytes [16]byte

			if _, err := rand.Read(bytes[:]); err != nil {
				id = "request-id-unavailable"
			} else {
				id = hex.EncodeToString(bytes[:])
			}
		}

		c.Set("request_id", id)
		c.Request = c.Request.WithContext(
			context.WithValue(c.Request.Context(), requestIDKey{}, id),
		)
		c.Writer.Header().Set("X-Request-ID", id)

		c.Next()
	}
}

func isValidRequestID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}

	for _, char := range id {
		isLetter := char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z'
		isDigit := char >= '0' && char <= '9'

		if !isLetter && !isDigit && char != '-' && char != '_' {
			return false
		}
	}

	return true
}

func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
