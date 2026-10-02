package repository

import (
	"context"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ShowRepository struct {
	pool *pgxpool.Pool
}

func NewShowRepository(pool *pgxpool.Pool) *ShowRepository {
	return &ShowRepository{
		pool: pool,
	}
}

func (r *ShowRepository) CreateShow(
	ctx context.Context,
	show domain.Show,
) (*domain.Show, error) {

	tx, err := r.pool.Begin(ctx)

	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Create show
	err = tx.QueryRow(
		ctx,
		`INSERT INTO shows (name, price_paise, per_user_limit)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
		show.Name,
		show.PricePaise,
		show.PerUserLimit,
	).Scan(&show.ID)

	if err != nil {
		return nil, err
	}

	for i := range show.Seats {
		seat := &show.Seats[i]

		err = tx.QueryRow(
			ctx,
			`INSERT INTO seats (show_id, seat_number, status)
         VALUES ($1, $2, 'available')
         RETURNING id`,
			show.ID,
			seat.SeatNumber,
		).Scan(&seat.ID)

		if err != nil {
			return nil, err
		}

		seat.ShowID = show.ID
		seat.Status = "available"
	}

	
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &show, nil
}
