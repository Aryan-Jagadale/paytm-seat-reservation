package domain

import "errors"

var ErrShowNotFound = errors.New("show not found")

type Show struct {
	ID           string
	Name         string
	PricePaise   int64
	PerUserLimit int
	Seats        []Seat
}

type Seat struct {
	ID         string
	ShowID     string
	SeatNumber string
	Status     string
}

type ShowSeatCounts struct {
	Total     int
	Available int
	Held      int
	Confirmed int
}

type ShowDetails struct {
	ID           string
	Name         string
	PricePaise   int64
	PerUserLimit int
	Seats        []Seat
	Counts       ShowSeatCounts
}