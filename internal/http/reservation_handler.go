package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/domain"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/metrics"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/middleware"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/service"
	"github.com/gin-gonic/gin"
)

type ReservationHandler struct {
	service *service.ReservationService
	metrics *metrics.Metrics
	logger  *slog.Logger
}

func NewReservationHandler(
	service *service.ReservationService,
	appMetrics *metrics.Metrics,
	logger *slog.Logger,
) *ReservationHandler {
	return &ReservationHandler{
		service: service,
		metrics: appMetrics,
		logger:  logger,
	}
}

type reserveRequest struct {
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type reservationResponse struct {
	ID             string   `json:"id"`
	ShowID         string   `json:"show_id"`
	UserID         string   `json:"user_id"`
	Status         string   `json:"status"`
	AmountPaise    int64    `json:"amount_paise"`
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`
}

func (h *ReservationHandler) Reserve(c *gin.Context) {
	showID := c.Param("id")

	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "authentication_required",
		})
		return
	}

	var req reserveRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	result, err := h.service.Reserve(
		c.Request.Context(),
		service.ReserveRequest{
			ShowID:         showID,
			UserID:         userID,
			Seats:          req.Seats,
			IdempotencyKey: req.IdempotencyKey,
		},
	)

	if err != nil {
		switch {
		case errors.Is(err, domain.ErrSeatUnavailable):
			h.metrics.ReservationsDeclined.
				WithLabelValues("seat_taken").
				Inc()

			h.logger.Warn(
				"reservation declined",
				slog.String(
					"request_id",
					middleware.RequestIDFromContext(c.Request.Context()),
				),
				slog.String("show_id", showID),
				slog.String("user_id", userID),
				slog.String("reason", "seat_taken"),
			)

			c.JSON(http.StatusConflict, gin.H{
				"error": "seat_taken",
			})

		case errors.Is(err, domain.ErrPerUserLimitExceeded):
			h.metrics.ReservationsDeclined.
				WithLabelValues("per_user_limit").
				Inc()

			h.logger.Warn(
				"reservation declined",
				slog.String(
					"request_id",
					middleware.RequestIDFromContext(c.Request.Context()),
				),
				slog.String("show_id", showID),
				slog.String("user_id", userID),
				slog.String("reason", "per_user_limit"),
			)

			c.JSON(http.StatusConflict, gin.H{
				"error": "per_user_limit",
			})

		case errors.Is(err, domain.ErrIdempotencyConflict):
			h.logger.Warn(
				"reservation declined",
				slog.String(
					"request_id",
					middleware.RequestIDFromContext(c.Request.Context()),
				),
				slog.String("show_id", showID),
				slog.String("user_id", userID),
				slog.String("reason", "idempotency_conflict"),
			)

			c.JSON(http.StatusConflict, gin.H{
				"error": "idempotency_key_reused_with_different_seats",
			})

		default:
			h.logger.Error(
				"reservation request failed",
				slog.String(
					"request_id",
					middleware.RequestIDFromContext(c.Request.Context()),
				),
				slog.String("show_id", showID),
				slog.String("user_id", userID),
				slog.String("error", err.Error()),
			)

			c.JSON(http.StatusBadRequest, gin.H{
				"error": err.Error(),
			})
		}

		return
	}

	reservation := result.Reservation

	if result.Replayed {
		h.metrics.ReservationsDeclined.
			WithLabelValues("idempotent_replay").
			Inc()

		h.logger.Info(
			"reservation replayed",
			slog.String(
				"request_id",
				middleware.RequestIDFromContext(c.Request.Context()),
			),
			slog.String("reservation_id", reservation.ID),
			slog.String("show_id", reservation.ShowID),
			slog.String("user_id", reservation.UserID),
		)
	} else {
		h.metrics.ReservationsConfirmed.Inc()

		h.logger.Info(
			"reservation confirmed",
			slog.String(
				"request_id",
				middleware.RequestIDFromContext(c.Request.Context()),
			),
			slog.String("reservation_id", reservation.ID),
			slog.String("show_id", reservation.ShowID),
			slog.String("user_id", reservation.UserID),
		)
	}

	seats := make([]string, 0, len(reservation.Seats))

	for _, seat := range reservation.Seats {
		seats = append(seats, seat.SeatNumber)
	}

	// Reservation.Seats isn't populated by repository yet.
	// Return requested seats for now.
	if len(seats) == 0 {
		seats = req.Seats
	}

	c.JSON(http.StatusCreated, reservationResponse{
		ID:             reservation.ID,
		ShowID:         reservation.ShowID,
		UserID:         reservation.UserID,
		Status:         reservation.Status,
		AmountPaise:    reservation.AmountPaise,
		Seats:          seats,
		IdempotencyKey: reservation.IdempotencyKey,
	})
}

func (h *ReservationHandler) Cancel(c *gin.Context) {
	reservationID := c.Param("id")

	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "authentication_required",
		})
		return
	}

	err := h.service.Cancel(
		c.Request.Context(),
		reservationID,
		userID,
	)

	if err != nil {
		switch {
		case errors.Is(err, domain.ErrReservationNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "reservation_not_found",
			})

		case errors.Is(err, domain.ErrNotReservationOwner):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "not_reservation_owner",
			})

		default:
			h.logger.Error(
				"reservation cancellation failed",
				slog.String(
					"request_id",
					middleware.RequestIDFromContext(c.Request.Context()),
				),
				slog.String("reservation_id", reservationID),
				slog.String("user_id", userID),
				slog.String("error", err.Error()),
			)

			c.JSON(http.StatusConflict, gin.H{
				"error": err.Error(),
			})
		}

		return
	}

	h.logger.Info(
		"reservation cancelled",
		slog.String(
			"request_id",
			middleware.RequestIDFromContext(c.Request.Context()),
		),
		slog.String("reservation_id", reservationID),
		slog.String("user_id", userID),
	)

	c.JSON(http.StatusOK, gin.H{
		"message": "reservation_cancelled",
	})
}