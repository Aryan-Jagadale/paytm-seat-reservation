package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/auth"
	appconfig "github.com/Aryan-Jagadale/paytm-seat-reservation/internal/config"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/db"
	apphealth "github.com/Aryan-Jagadale/paytm-seat-reservation/internal/health"
	apphttp "github.com/Aryan-Jagadale/paytm-seat-reservation/internal/http"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/metrics"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/repository"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := appconfig.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	startupCtx, startupCancel := context.WithTimeout(
		context.Background(),
		cfg.DBConnectTimeout,
	)
	defer startupCancel()

	pool, err := db.NewPool(startupCtx, db.PoolConfig{
		URL:            cfg.DatabaseURL,
		MaxConns:       cfg.MaxConns,
		MinConns:       cfg.MinConns,
		ConnectTimeout: cfg.DBConnectTimeout,
	})
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer pool.Close()

	showRepo := repository.NewShowRepository(pool)
	appMetrics := metrics.New()

	showService := service.NewShowService(showRepo)
	showHandler := apphttp.NewShowHandler(showService)

	reservationRepo := repository.NewReservationRepository(pool)
	reservationService := service.NewReservationService(reservationRepo)
	reservationHandler := apphttp.NewReservationHandler(reservationService, appMetrics, logger)

	authenticator := auth.NewJWTAuthenticator(cfg.AuthJWTSecret)

	ready := apphealth.NewChecker(pool, time.Second)
	router := apphttp.NewRouter(ready, logger, showHandler, reservationHandler, showService, authenticator, appMetrics)

	server := &http.Server{
		Addr:              net.JoinHostPort("0.0.0.0", cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownCtx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	serverErr := make(chan error, 1)

	go func() {
		logger.Info(
			"server starting",
			slog.String("address", server.Addr),
		)

		if err := server.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("HTTP server: %w", err)
		} else {
			serverErr <- nil
		}
	}()

	select {
	case <-shutdownCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			return err
		}
		return fmt.Errorf("HTTP server stopped unexpectedly")
	}

	drainCtx, cancel := context.WithTimeout(
		context.Background(),
		cfg.ShutdownTimeout,
	)
	defer cancel()

	if err := server.Shutdown(drainCtx); err != nil {
		logger.Error("graceful shutdown timed out", slog.String(
			"error", err.Error(),
		))
		if closeErr := server.Close(); closeErr != nil {
			logger.Error("force close failed", slog.String(
				"error", closeErr.Error(),
			))
		}
	}

	logger.Info("server shutdown complete")
	return nil
}
