package http

import (
	"net/http"
	"errors"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/domain"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/service"
	"github.com/gin-gonic/gin"
)


type showResponse struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	PricePaise   int64              `json:"price_paise"`
	PerUserLimit int                `json:"per_user_limit"`
	Seats        []seatResponse     `json:"seats"`
	Counts       showCountsResponse `json:"counts"`
}

type seatResponse struct {
	ID         string `json:"id"`
	SeatNumber string `json:"seat_number"`
	Status     string `json:"status"`
}

type showCountsResponse struct {
	Total     int `json:"total"`
	Available int `json:"available"`
	Held      int `json:"held"`
	Confirmed int `json:"confirmed"`
}

type ShowHandler struct {
	service *service.ShowService
}

func NewShowHandler(service *service.ShowService) *ShowHandler {
	return &ShowHandler{
		service: service,
	}
}

type createShowRequest struct {
	Name          string   `json:"name"`
	Seats         []string `json:"seats"`
	PricePaise    int64    `json:"price_paise"`
	PerUserLimit  int      `json:"per_user_limit"`
}

func (h *ShowHandler) CreateShow(c *gin.Context) {
	var req createShowRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	seats := make([]domain.Seat, len(req.Seats))

	for i, seatNumber := range req.Seats {
		seats[i] = domain.Seat{
			SeatNumber: seatNumber,
		}
	}

	show := domain.Show{
		Name:          req.Name,
		PricePaise:    req.PricePaise,
		PerUserLimit:  req.PerUserLimit,
		Seats:         seats,
	}

	created, err := h.service.CreateShow(c.Request.Context(), show)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, created)
}


func (h *ShowHandler) GetShow(c *gin.Context) {
	showID := c.Param("id")

	show, err := h.service.GetShow(
		c.Request.Context(),
		showID,
	)

	if err != nil {
		if errors.Is(err, domain.ErrShowNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "show_not_found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error",
		})
		return
	}

	seats := make([]seatResponse, 0, len(show.Seats))

	for _, seat := range show.Seats {
		seats = append(seats, seatResponse{
			ID:         seat.ID,
			SeatNumber: seat.SeatNumber,
			Status:     seat.Status,
		})
	}

	c.JSON(http.StatusOK, showResponse{
		ID:           show.ID,
		Name:         show.Name,
		PricePaise:   show.PricePaise,
		PerUserLimit: show.PerUserLimit,
		Seats:        seats,
		Counts: showCountsResponse{
			Total:     show.Counts.Total,
			Available: show.Counts.Available,
			Held:      show.Counts.Held,
			Confirmed: show.Counts.Confirmed,
		},
	})
}