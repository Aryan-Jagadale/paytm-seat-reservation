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

        if id == "" {
            var bytes [16]byte
            if _, err := rand.Read(bytes[:]); err != nil {
                // A random ID failure should not crash request handling.
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

func RequestIDFromContext(ctx context.Context) string {
    id, _ := ctx.Value(requestIDKey{}).(string)
    return id;
}