package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReservationRepository struct {
	pool *pgxpool.Pool
}

func NewReservationRepository(pool *pgxpool.Pool) *ReservationRepository {
	return &ReservationRepository{
		pool: pool,
	}
}

func (r *ReservationRepository) Reserve(
	ctx context.Context,
	showID string,
	userID string,
	seats []string,
	idempotencyKey string,
) (*domain.ReserveResult, error) {

	sort.Strings(seats)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reservation transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// ---------------------------------------------------------
	// 1. Check existing idempotency key
	// ---------------------------------------------------------

	var existing domain.Reservation

	err = tx.QueryRow(
		ctx,
		`SELECT id, show_id, user_id, idempotency_key, status, amount_paise
		 FROM reservations
		 WHERE show_id = $1
		   AND user_id = $2
		   AND idempotency_key = $3`,
		showID,
		userID,
		idempotencyKey,
	).Scan(
		&existing.ID,
		&existing.ShowID,
		&existing.UserID,
		&existing.IdempotencyKey,
		&existing.Status,
		&existing.AmountPaise,
	)

	if err == nil {
		existingSeats, err := r.getReservationSeats(
			ctx,
			tx,
			existing.ID,
		)
		if err != nil {
			return nil, err
		}

		if !sameSeats(existingSeats, seats) {
			return nil, domain.ErrIdempotencyConflict
		}

		return &domain.ReserveResult{
			Reservation: &existing,
			Replayed:    true,
		}, nil
	}

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check idempotency: %w", err)
	}

	// ---------------------------------------------------------
	// 2. Create reservation
	// ---------------------------------------------------------

	var reservation domain.Reservation

	err = tx.QueryRow(
		ctx,
		`INSERT INTO reservations
			(show_id, user_id, idempotency_key, status, amount_paise)
		 VALUES ($1, $2, $3, 'confirmed', 0)
		 ON CONFLICT (show_id, user_id, idempotency_key) DO NOTHING
		 RETURNING id, show_id, user_id, idempotency_key, status, amount_paise`,
		showID,
		userID,
		idempotencyKey,
	).Scan(
		&reservation.ID,
		&reservation.ShowID,
		&reservation.UserID,
		&reservation.IdempotencyKey,
		&reservation.Status,
		&reservation.AmountPaise,
	)

	// Another concurrent request created this idempotency key.
	if errors.Is(err, pgx.ErrNoRows) {

		err = tx.QueryRow(
			ctx,
			`SELECT id, show_id, user_id, idempotency_key, status, amount_paise
			 FROM reservations
			 WHERE show_id = $1
			   AND user_id = $2
			   AND idempotency_key = $3`,
			showID,
			userID,
			idempotencyKey,
		).Scan(
			&reservation.ID,
			&reservation.ShowID,
			&reservation.UserID,
			&reservation.IdempotencyKey,
			&reservation.Status,
			&reservation.AmountPaise,
		)

		if err != nil {
			return nil, fmt.Errorf(
				"fetch existing reservation: %w",
				err,
			)
		}

		existingSeats, err := r.getReservationSeats(
			ctx,
			tx,
			reservation.ID,
		)
		if err != nil {
			return nil, err
		}

		if !sameSeats(existingSeats, seats) {
			return nil, domain.ErrIdempotencyConflict
		}

		return &domain.ReserveResult{
			Reservation: &reservation,
			Replayed:    true,
		}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("create reservation: %w", err)
	}

	// ---------------------------------------------------------
	// 3. Initialize user's quota row
	// ---------------------------------------------------------

	_, err = tx.Exec(
		ctx,
		`INSERT INTO show_user_limits
			(show_id, user_id, reserved_count)
		 VALUES ($1, $2, 0)
		 ON CONFLICT (show_id, user_id) DO NOTHING`,
		showID,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"initialize user limit: %w",
			err,
		)
	}

	// ---------------------------------------------------------
	// 4. Lock user's quota row
	// ---------------------------------------------------------

	var reservedCount int

	err = tx.QueryRow(
		ctx,
		`SELECT reserved_count
		 FROM show_user_limits
		 WHERE show_id = $1
		   AND user_id = $2
		 FOR UPDATE`,
		showID,
		userID,
	).Scan(&reservedCount)

	if err != nil {
		return nil, fmt.Errorf(
			"check user limit: %w",
			err,
		)
	}

	// ---------------------------------------------------------
	// 5. Get show configuration
	// ---------------------------------------------------------

	var perUserLimit int
	var pricePaise int64

	err = tx.QueryRow(
		ctx,
		`SELECT per_user_limit, price_paise
		 FROM shows
		 WHERE id = $1`,
		showID,
	).Scan(
		&perUserLimit,
		&pricePaise,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("show not found")
		}

		return nil, fmt.Errorf(
			"get show configuration: %w",
			err,
		)
	}

	// ---------------------------------------------------------
	// 6. Check per-user limit
	// ---------------------------------------------------------

	if reservedCount+len(seats) > perUserLimit {
		return nil, domain.ErrPerUserLimitExceeded
	}

	// ---------------------------------------------------------
	// 7. Calculate amount
	// ---------------------------------------------------------

	reservation.AmountPaise = pricePaise * int64(len(seats))

	_, err = tx.Exec(
		ctx,
		`UPDATE reservations
		 SET amount_paise = $1,
		     updated_at = NOW()
		 WHERE id = $2`,
		reservation.AmountPaise,
		reservation.ID,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"update reservation amount: %w",
			err,
		)
	}

	// ---------------------------------------------------------
	// 8. Atomically claim every requested seat
	// ---------------------------------------------------------

	for _, seatNumber := range seats {

		result, err := tx.Exec(
			ctx,
			`UPDATE seats
			 SET status = 'confirmed',
			     user_id = $1
			 WHERE show_id = $2
			   AND seat_number = $3
			   AND status = 'available'`,
			userID,
			showID,
			seatNumber,
		)

		if err != nil {
			return nil, fmt.Errorf(
				"claim seat %s: %w",
				seatNumber,
				err,
			)
		}

		if result.RowsAffected() != 1 {
			return nil, fmt.Errorf(
				"%w: %s",
				domain.ErrSeatUnavailable,
				seatNumber,
			)
		}

		// Get seat ID.
		var seatID string

		err = tx.QueryRow(
			ctx,
			`SELECT id
			 FROM seats
			 WHERE show_id = $1
			   AND seat_number = $2`,
			showID,
			seatNumber,
		).Scan(&seatID)

		if err != nil {
			return nil, fmt.Errorf(
				"get seat %s: %w",
				seatNumber,
				err,
			)
		}

		// Link seat to reservation.
		_, err = tx.Exec(
			ctx,
			`INSERT INTO reservation_seats
				(reservation_id, seat_id)
			 VALUES ($1, $2)`,
			reservation.ID,
			seatID,
		)

		if err != nil {
			return nil, fmt.Errorf(
				"link seat %s to reservation: %w",
				seatNumber,
				err,
			)
		}
	}

	// ---------------------------------------------------------
	// 9. Increment user's reservation count
	// ---------------------------------------------------------

	_, err = tx.Exec(
		ctx,
		`UPDATE show_user_limits
		 SET reserved_count = reserved_count + $1
		 WHERE show_id = $2
		   AND user_id = $3`,
		len(seats),
		showID,
		userID,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"update user reservation count: %w",
			err,
		)
	}

	// ---------------------------------------------------------
	// 10. Commit
	// ---------------------------------------------------------

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf(
			"commit reservation transaction: %w",
			err,
		)
	}

	return &domain.ReserveResult{
		Reservation: &reservation,
		Replayed:    false,
	}, nil
}

func (r *ReservationRepository) getReservationSeats(
	ctx context.Context,
	tx pgx.Tx,
	reservationID string,
) ([]string, error) {

	rows, err := tx.Query(
		ctx,
		`SELECT s.seat_number
		 FROM reservation_seats rs
		 JOIN seats s ON s.id = rs.seat_id
		 WHERE rs.reservation_id = $1
		 ORDER BY s.seat_number`,
		reservationID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get reservation seats: %w",
			err,
		)
	}
	defer rows.Close()

	var seats []string

	for rows.Next() {
		var seat string

		if err := rows.Scan(&seat); err != nil {
			return nil, fmt.Errorf(
				"scan reservation seat: %w",
				err,
			)
		}

		seats = append(seats, seat)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate reservation seats: %w",
			err,
		)
	}

	return seats, nil
}

func sameSeats(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func (r *ReservationRepository) Cancel(
	ctx context.Context,
	reservationID string,
	userID string,
) error {

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cancellation transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Lock the reservation row.
	var (
		reservationUserID string
		status            string
		showID            string
	)

	err = tx.QueryRow(
		ctx,
		`SELECT user_id, status, show_id
         FROM reservations
         WHERE id = $1
         FOR UPDATE`,
		reservationID,
	).Scan(
		&reservationUserID,
		&status,
		&showID,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrReservationNotFound
		}

		return fmt.Errorf("get reservation: %w", err)
	}

	// 2. Verify ownership.
	if reservationUserID != userID {
		return domain.ErrNotReservationOwner
	}

	// 3. Only confirmed reservations can be cancelled.
	if status != "confirmed" {
		return fmt.Errorf("reservation cannot be cancelled in status %q", status)
	}

	// 4. Lock the seats belonging to this reservation.
	rows, err := tx.Query(
		ctx,
		`SELECT s.id
FROM reservation_seats rs
JOIN seats s ON s.id = rs.seat_id
WHERE rs.reservation_id = $1
FOR UPDATE OF s;`,
		reservationID,
	)
	if err != nil {
		return fmt.Errorf("get reservation seats: %w", err)
	}
	defer rows.Close()

	var seatIDs []string

	for rows.Next() {
		var seatID string

		if err := rows.Scan(&seatID); err != nil {
			return fmt.Errorf("scan reservation seat: %w", err)
		}

		seatIDs = append(seatIDs, seatID)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate reservation seats: %w", err)
	}

	// 5. Mark reservation as cancelled.
	_, err = tx.Exec(
		ctx,
		`UPDATE reservations
         SET status = 'cancelled',
             updated_at = NOW()
         WHERE id = $1`,
		reservationID,
	)
	if err != nil {
		return fmt.Errorf("cancel reservation: %w", err)
	}

	// 6. Release the seats.
	for _, seatID := range seatIDs {
		_, err = tx.Exec(
			ctx,
			`UPDATE seats
             SET status = 'available',
                 user_id = NULL,
                 hold_expires_at = NULL
             WHERE id = $1`,
			seatID,
		)

		if err != nil {
			return fmt.Errorf("release seat: %w", err)
		}
	}

	// 7. Decrement user's reservation count.
	_, err = tx.Exec(
		ctx,
		`UPDATE show_user_limits
         SET reserved_count = reserved_count - $1
         WHERE show_id = $2
           AND user_id = $3`,
		len(seatIDs),
		showID,
		userID,
	)

	if err != nil {
		return fmt.Errorf("decrement user reservation count: %w", err)
	}

	// 8. Commit everything atomically.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cancellation transaction: %w", err)
	}

	return nil
}
