package http

import (
	"net/http"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/domain"
	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/service"
	"github.com/gin-gonic/gin"
)

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