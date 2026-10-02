package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/domain"
	"github.com/jackc/pgx/v5"
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


func (r *ShowRepository) GetShow(ctx context.Context,showID string,) (*domain.ShowDetails, error) {

	var show domain.ShowDetails

	err := r.pool.QueryRow(
		ctx,
		`SELECT id, name, price_paise, per_user_limit
		 FROM shows
		 WHERE id = $1`,
		showID,
	).Scan(
		&show.ID,
		&show.Name,
		&show.PricePaise,
		&show.PerUserLimit,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrShowNotFound
		}

		return nil, fmt.Errorf("get show: %w", err)
	}

	rows, err := r.pool.Query(
		ctx,
		`SELECT id, show_id, seat_number, status
		 FROM seats
		 WHERE show_id = $1
		 ORDER BY seat_number`,
		showID,
	)

	if err != nil {
		return nil, fmt.Errorf("get show seats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var seat domain.Seat

		if err := rows.Scan(
			&seat.ID,
			&seat.ShowID,
			&seat.SeatNumber,
			&seat.Status,
		); err != nil {
			return nil, fmt.Errorf("scan show seat: %w", err)
		}

		show.Seats = append(show.Seats, seat)

		show.Counts.Total++

		switch seat.Status {
		case "available":
			show.Counts.Available++
		case "held":
			show.Counts.Held++
		case "confirmed":
			show.Counts.Confirmed++
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate show seats: %w", err)
	}

	return &show, nil
}