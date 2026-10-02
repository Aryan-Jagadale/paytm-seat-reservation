package domain

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