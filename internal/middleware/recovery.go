package middleware

import (
    "log/slog"
    "net/http"
    "runtime/debug"

    "github.com/gin-gonic/gin"
)

func Recovery(logger *slog.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        defer func() {
            if recovered := recover(); recovered != nil {
                logger.Error(
                    "panic recovered",
                    slog.Any("panic", recovered),
                    slog.String("request_id", RequestIDFromContext(
                        c.Request.Context(),
                    )),
                    slog.String("stack", string(debug.Stack())),
                )

                c.AbortWithStatusJSON(
                    http.StatusInternalServerError,
                    gin.H{
                        "error":      "internal server error",
                        "request_id": RequestIDFromContext(
                            c.Request.Context(),
                        ),
                    },
                )
            }
        }()

        c.Next()
    }
}