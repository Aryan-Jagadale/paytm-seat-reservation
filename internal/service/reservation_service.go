package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/domain"
)

type ReserveRequest struct {
	ShowID         string
	UserID         string
	Seats          []string
	IdempotencyKey string
}

type ReservationRepository interface {
	Reserve(
		ctx context.Context,
		showID string,
		userID string,
		seats []string,
		idempotencyKey string,
	) (*domain.ReserveResult, error)

	Cancel(
		ctx context.Context,
		reservationID string,
		userID string,
	) error
}

type ReservationService struct {
	repo ReservationRepository
}

func NewReservationService(repo ReservationRepository) *ReservationService {
	return &ReservationService{
		repo: repo,
	}
}

func (s *ReservationService) Reserve(
	ctx context.Context,
	req ReserveRequest,
) (*domain.ReserveResult, error) {

	if strings.TrimSpace(req.ShowID) == "" {
		return nil, fmt.Errorf("%w: show_id is required", domain.ErrInvalidReservationRequest)
	}

	if strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("%w: user identity is required", domain.ErrInvalidReservationRequest)
	}

	if len(req.Seats) == 0 {
		return nil, fmt.Errorf("%w: at least one seat is required", domain.ErrInvalidReservationRequest)
	}

	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, fmt.Errorf("%w: idempotency_key is required", domain.ErrInvalidReservationRequest)
	}

	seen := make(map[string]struct{}, len(req.Seats))

	for _, seat := range req.Seats {
		seat = strings.TrimSpace(seat)

		if seat == "" {
			return nil, fmt.Errorf("%w: seat cannot be empty", domain.ErrInvalidReservationRequest)
		}

		if _, exists := seen[seat]; exists {
			return nil, fmt.Errorf("%w: duplicate seat: %s", domain.ErrInvalidReservationRequest, seat)
		}

		seen[seat] = struct{}{}
	}

	return s.repo.Reserve(
		ctx,
		req.ShowID,
		req.UserID,
		req.Seats,
		req.IdempotencyKey,
	)
}

func (s *ReservationService) Cancel(
	ctx context.Context,
	reservationID string,
	userID string,
) error {
	if strings.TrimSpace(reservationID) == "" {
		return fmt.Errorf("reservation_id is required")
	}

	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("user identity is required")
	}

	return s.repo.Cancel(ctx, reservationID, userID)
}
