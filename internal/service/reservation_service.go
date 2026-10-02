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
	) (*domain.Reservation, error)
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
) (*domain.Reservation, error) {

	if strings.TrimSpace(req.ShowID) == "" {
		return nil, fmt.Errorf("show_id is required")
	}

	if strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("user identity is required")
	}

	if len(req.Seats) == 0 {
		return nil, fmt.Errorf("at least one seat is required")
	}

	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, fmt.Errorf("idempotency_key is required")
	}

	seen := make(map[string]struct{}, len(req.Seats))

	for _, seat := range req.Seats {
		seat = strings.TrimSpace(seat)

		if seat == "" {
			return nil, fmt.Errorf("seat cannot be empty")
		}

		if _, exists := seen[seat]; exists {
			return nil, fmt.Errorf("duplicate seat: %s", seat)
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
