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

        status := c.Writer.Status()
        latency := time.Since(start)

        attrs := []any{
            slog.String("request_id", RequestIDFromContext(c.Request.Context())),
            slog.String("method", c.Request.Method),
            slog.String("path", c.Request.URL.Path),
            slog.Int("status", status),
            slog.Duration("latency", latency),
            slog.Int("bytes_written", c.Writer.Size()),
        }

        switch {
        case status >= 500:
            logger.Error("http request", attrs...)
        case status >= 400:
            logger.Warn("http request", attrs...)
        default:
            logger.Info("http request", attrs...)
        }
    }
}