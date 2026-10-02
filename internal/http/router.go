package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/auth"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/metrics"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/middleware"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/service"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type ReadyChecker interface {
	Ready(ctx context.Context) error
}

type Handler struct {
	ready  ReadyChecker
	logger *slog.Logger
}

func NewRouter(ready ReadyChecker, logger *slog.Logger, showHandler *ShowHandler, reservationHandler *ReservationHandler, showService *service.ShowService, authenticator *auth.JWTAuthenticator, appMetrics *metrics.Metrics) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()

	router.Use(middleware.RequestID())
	router.Use(middleware.Recovery(logger))
	router.Use(middleware.AccessLog(logger))

	protected := router.Group("")
	protected.Use(middleware.Authentication(authenticator))

	handler := &Handler{
		ready:  ready,
		logger: logger,
	}

	router.GET("/healthz", handler.Liveness)
	router.GET("/readyz", handler.Readiness)

	router.POST("/shows", showHandler.CreateShow)
	router.GET("/shows/:id", showHandler.GetShow)

	router.GET("/metrics", func(c *gin.Context) {
		if err := appMetrics.RefreshSeatsAvailable(
			c.Request.Context(),
			showService,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to refresh metrics",
			})
			return
		}

		promhttp.HandlerFor(
			appMetrics.Registry,
			promhttp.HandlerOpts{},
		).ServeHTTP(c.Writer, c.Request)
	})

	protected.POST("/shows/:id/reserve", reservationHandler.Reserve)
	protected.POST("/reservations/:id/cancel", reservationHandler.Cancel)

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
