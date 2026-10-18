package api

import (
	"math"
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/gin-gonic/gin"
)

const (
	defaultEntriesLimit = 100
	maxEntriesLimit     = 500
)

type addEntryRequest struct {
	FoodID      int64      `json:"food_id"      validate:"required,gt=0"`
	Grams       float64    `json:"grams"        validate:"omitempty,gt=0,lte=5000"`
	PortionName string     `json:"portion_name"`
	PortionQty  float64    `json:"portion_qty"  validate:"omitempty,gt=0,lte=50"`
	LoggedAt    *time.Time `json:"logged_at"`
}

// addEntry accepts either grams or a count of a named portion, and snapshots
// the nutrition so a later catalogue correction cannot rewrite this day.
func (s *Server) addEntry(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	var req addEntryRequest
	err = bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	food, err := s.foods.GetByID(c.Request.Context(), req.FoodID)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	grams, err := resolveGrams(food, req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	at := time.Now()
	if req.LoggedAt != nil {
		at = *req.LoggedAt
	}

	entry := domain.LogEntry{
		UserID:    userID,
		FoodID:    food.ID,
		FoodName:  food.Name,
		Grams:     grams,
		Nutrition: food.Scale(grams),
		LoggedAt:  at,
		Day:       truncateDay(at),
	}

	saved, err := s.logs.Add(c.Request.Context(), entry)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, saved)
}

// resolveGrams turns whichever amount the client sent into grams. A named
// portion count multiplies that portion's grams.
func resolveGrams(food domain.Food, req addEntryRequest) (float64, error) {
	if req.Grams > 0 {
		return req.Grams, nil
	}

	if req.PortionName != "" {
		for _, p := range food.Portions {
			if p.Name == req.PortionName {
				qty := req.PortionQty
				if qty == 0 {
					qty = 1
				}
				return p.Grams * qty, nil
			}
		}
		return 0, domain.ErrInvalidInput
	}

	portion, ok := food.DefaultPortion()
	if ok {
		return portion.Grams, nil
	}
	return 100, nil
}

// listEntries is the history read: a flat page of entries, newest first. It
// computes no totals; callers sum what they need.
func (s *Server) listEntries(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	today := truncateDay(time.Now())
	from, err := queryDate(c, "from", today)
	if err != nil {
		RespondWithError(c, err)
		return
	}
	to, err := queryDate(c, "to", today)
	if err != nil {
		RespondWithError(c, err)
		return
	}
	limit, err := queryInt(c, "limit", defaultEntriesLimit, 1, maxEntriesLimit)
	if err != nil {
		RespondWithError(c, err)
		return
	}
	offset, err := queryInt(c, "offset", 0, 0, math.MaxInt32)
	if err != nil {
		RespondWithError(c, err)
		return
	}
	if to.Before(from) {
		RespondWithError(c, domain.ErrInvalidInput)
		return
	}

	entries, err := s.logs.Range(c.Request.Context(), userID, from, to, limit, offset)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, entries)
}

type updateEntryRequest struct {
	Grams float64 `json:"grams" validate:"required,gt=0,lte=5000"`
}

func (s *Server) updateEntry(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	id, err := pathInt(c, "id")
	if err != nil {
		RespondWithError(c, err)
		return
	}

	var req updateEntryRequest
	err = bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	err = s.logs.UpdateGrams(c.Request.Context(), userID, id, req.Grams)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) deleteEntry(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	id, err := pathInt(c, "id")
	if err != nil {
		RespondWithError(c, err)
		return
	}

	err = s.logs.Delete(c.Request.Context(), userID, id)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
