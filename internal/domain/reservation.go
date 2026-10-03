package domain

import "errors"

var (
	ErrSeatUnavailable      = errors.New("seat unavailable")
	ErrPerUserLimitExceeded = errors.New("per-user reservation limit exceeded")
	ErrIdempotencyConflict  = errors.New("idempotency key already used with different seats")

	ErrReservationNotFound = errors.New("reservation not found")
	ErrNotReservationOwner = errors.New("user does not own reservation")

	ErrInvalidReservationRequest = errors.New("invalid reservation request")
)

type Reservation struct {
	ID             string
	ShowID         string
	UserID         string
	IdempotencyKey string
	Status         string
	AmountPaise    int64
	Seats          []Seat
}

type ReserveResult struct {
	Reservation *Reservation
	Replayed    bool
}
