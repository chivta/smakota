package api

import (
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/gin-gonic/gin"
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

func (s *Server) getDay(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	day, err := pathDate(c, "date")
	if err != nil {
		RespondWithError(c, err)
		return
	}

	entries, err := s.logs.Day(c.Request.Context(), userID, day)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	telegramID, err := s.identify(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	user, err := s.users.GetByTelegramID(c.Request.Context(), telegramID)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	view := domain.Day{
		Date:          day,
		Entries:       entries,
		KcalTarget:    user.KcalTarget,
		ProteinTarget: user.ProteinTargetG,
	}
	for _, e := range entries {
		view.Consumed.Kcal += e.Nutrition.Kcal
		view.Consumed.Protein += e.Nutrition.Protein
		view.Consumed.Fat += e.Nutrition.Fat
		view.Consumed.Carbs += e.Nutrition.Carbs
	}

	c.JSON(http.StatusOK, view)
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

// repeatDay backs "повторити вчорашній день": copy every entry from the source
// date onto the target date.
func (s *Server) repeatDay(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	target, err := pathDate(c, "date")
	if err != nil {
		RespondWithError(c, err)
		return
	}

	source := target.AddDate(0, 0, -1)
	copied, err := s.logs.CopyDay(c.Request.Context(), userID, source, target)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"copied": copied})
}

type addWeightRequest struct {
	WeightKG   float64    `json:"weight_kg"   validate:"required,gte=30,lte=300"`
	MeasuredOn *time.Time `json:"measured_on"`
}

func (s *Server) addWeight(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	var req addWeightRequest
	err = bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	on := time.Now()
	if req.MeasuredOn != nil {
		on = *req.MeasuredOn
	}

	err = s.weights.Add(c.Request.Context(), domain.Weight{
		UserID:     userID,
		WeightKG:   req.WeightKG,
		MeasuredOn: truncateDay(on),
	})
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

// getWeightTrend returns the rolling average only. Day-over-day deltas are
// deliberately not exposed.
func (s *Server) getWeightTrend(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	points, err := s.weights.RollingAverage(c.Request.Context(), userID, weightTrendDays)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, points)
}
