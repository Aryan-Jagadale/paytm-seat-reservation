package middleware

import (
    "log/slog"
    "time"

    "github.com/gin-gonic/gin"
)

func AccessLog(logger *slog.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()

        c.Next()

        logger.Info(
            "http request",
            slog.String("request_id", RequestIDFromContext(c.Request.Context())),
            slog.String("method", c.Request.Method),
            slog.String("path", c.Request.URL.Path),
            slog.Int("status", c.Writer.Status()),
            slog.Duration("latency", time.Since(start)),
        )
    }
}