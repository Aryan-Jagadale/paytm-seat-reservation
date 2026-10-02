package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/domain"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/repository"
)

type ShowService struct {
	repo *repository.ShowRepository
}


func NewShowService(repo *repository.ShowRepository) *ShowService {
	return &ShowService{
		repo: repo,
	}
}

func (s *ShowService) CreateShow(ctx context.Context,show domain.Show,) (*domain.Show, error) {

	
	if strings.TrimSpace(show.Name) == "" {
		return nil, fmt.Errorf("show name is required")
	}

	
	if len(show.Seats) == 0 {
		return nil, fmt.Errorf("at least one seat is required")
	}


	if show.PricePaise < 0 {
		return nil, fmt.Errorf("price_paise cannot be negative")
	}


	if show.PerUserLimit == 0 {
		show.PerUserLimit = 4
	}

	if show.PerUserLimit < 0 {
		return nil, fmt.Errorf("per_user_limit cannot be negative")
	}

	
	seen := make(map[string]struct{})

	for _, seat := range show.Seats {
		seatNumber := strings.TrimSpace(seat.SeatNumber)

		if seatNumber == "" {
			return nil, fmt.Errorf("seat number cannot be empty")
		}

		if _, exists := seen[seatNumber]; exists {
			return nil, fmt.Errorf("duplicate seat: %s", seatNumber)
		}

		seen[seatNumber] = struct{}{}
	}

	return s.repo.CreateShow(ctx, show)
}

func (s *ShowService) GetShow( ctx context.Context,showID string,) (*domain.ShowDetails, error) {

	if strings.TrimSpace(showID) == "" {
		return nil, fmt.Errorf("show_id is required")
	}

	return s.repo.GetShow(ctx, showID)
}