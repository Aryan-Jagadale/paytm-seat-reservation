package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)



type Metrics struct {
	Registry *prometheus.Registry

	ReservationsConfirmed prometheus.Counter
	ReservationsDeclined  *prometheus.CounterVec
	SeatsAvailable        prometheus.Gauge
}


type AvailableSeatCounter interface {
	CountAvailableSeats(ctx context.Context) (int64, error)
}



func New() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		Registry: registry,

		ReservationsConfirmed: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "reservations_confirmed_total",
				Help: "Total number of newly confirmed reservations.",
			},
		),

		ReservationsDeclined: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "reservations_declined_total",
				Help: "Total number of reservation requests declined or replayed.",
			},
			[]string{"reason"},
		),

		SeatsAvailable: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "seats_available",
				Help: "Current number of available seats.",
			},
		),
	}

	registry.MustRegister(m.ReservationsConfirmed)
	registry.MustRegister(m.ReservationsDeclined)
	registry.MustRegister(m.SeatsAvailable)

	return m
}


func (m *Metrics) RefreshSeatsAvailable(ctx context.Context, counter AvailableSeatCounter) error {
	count, err := counter.CountAvailableSeats(ctx)
	if err != nil {
		return err
	}

	m.SeatsAvailable.Set(float64(count))

	return nil
}