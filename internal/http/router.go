package http

import (
    "context"
    "log/slog"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"

    "github.com/Aryan-Jagadale/paytm-seat-reservation/internal/middleware"

)

type ReadyChecker interface {
    Ready(ctx context.Context) error
}

type Handler struct {
    ready  ReadyChecker
    logger *slog.Logger
}

func NewRouter(ready ReadyChecker, logger *slog.Logger) *gin.Engine {
    gin.SetMode(gin.ReleaseMode)

    router := gin.New()

    router.Use(middleware.RequestID())
    router.Use(middleware.Recovery(logger))
    router.Use(middleware.AccessLog(logger))

    handler := &Handler{
        ready:  ready,
        logger: logger,
    }

    router.GET("/healthz", handler.Liveness)
    router.GET("/readyz", handler.Readiness)

    return router
}

func (h *Handler) Liveness(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

func (h *Handler) Readiness(c *gin.Context) {
    ctx, cancel := context.WithTimeout(
        c.Request.Context(),
        time.Second,
    )
    defer cancel()

    if err := h.ready.Ready(ctx); err != nil {
        h.logger.Warn(
            "readiness check failed",
            slog.String("request_id", middleware.RequestIDFromContext(
                c.Request.Context(),
            )),
            slog.String("error", err.Error()),
        )

        c.JSON(http.StatusServiceUnavailable, gin.H{
            "status": "not_ready",
            "error":  "dependency unavailable",
        })
        return
    }

    c.JSON(http.StatusOK, gin.H{"status": "ready"})
}