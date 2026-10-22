package api

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/metrics"
	"github.com/gin-gonic/gin"
)

const (
	defaultEntriesLimit = 100
	maxEntriesLimit     = 500
)

type inlineNutrition struct {
	Kcal    *float64 `json:"kcal"    validate:"required,gte=0,lte=20000"`
	Protein float64  `json:"protein" validate:"gte=0,lte=5000"`
	Fat     float64  `json:"fat"     validate:"gte=0,lte=5000"`
	Carbs   float64  `json:"carbs"   validate:"gte=0,lte=5000"`
}

type addEntryRequest struct {
	FoodID      *int64           `json:"food_id"      validate:"omitempty,gt=0"`
	Name        string           `json:"name"         validate:"max=200"`
	Nutrition   *inlineNutrition `json:"nutrition"`
	Grams       float64          `json:"grams"        validate:"omitempty,gt=0,lte=5000"`
	PortionName string           `json:"portion_name"`
	PortionQty  float64          `json:"portion_qty"  validate:"omitempty,gt=0,lte=50"`
	LoggedAt    *time.Time       `json:"logged_at"`
}

// addEntry logs either a catalogue food, by id, or an inline one, by name and
// nutrition. Logging never requires a catalogue lookup. Either way the
// nutrition is snapshotted, so a later catalogue correction cannot rewrite this
// day.
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

	var entry domain.LogEntry
	var path, amount, foodSource string
	switch {
	case req.FoodID != nil && req.Name == "" && req.Nutrition == nil:
		path = domain.LogPathCatalogue
		entry, amount, foodSource, err = s.catalogueEntry(c.Request.Context(), *req.FoodID, req)
	case req.FoodID == nil && req.Name != "":
		path, amount, foodSource = domain.LogPathInline, domain.AmountGrams, domain.SourceInline
		entry, err = inlineEntry(req)
	default:
		err = domain.ErrInvalidInput
	}
	if err != nil {
		RespondWithError(c, err)
		return
	}

	entry.UserID = userID
	entry.LoggedAt = time.Now()
	if req.LoggedAt != nil {
		entry.LoggedAt = *req.LoggedAt
	}

	saved, err := s.logs.Add(c.Request.Context(), entry)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	metrics.Entries.WithLabelValues(path, amount, foodSource).Inc()

	c.JSON(http.StatusCreated, saved)
}

// catalogueEntry also returns the amount kind and the food's source, the
// labels of metrics.Entries.
func (s *Server) catalogueEntry(ctx context.Context, foodID int64, req addEntryRequest) (domain.LogEntry, string, string, error) {
	food, err := s.foods.GetByID(ctx, foodID)
	if err != nil {
		return domain.LogEntry{}, "", "", err
	}

	grams, amount, err := domain.ResolveAmount(food, req.Grams, req.PortionName, req.PortionQty)
	if err != nil {
		return domain.LogEntry{}, "", "", err
	}

	entry := domain.LogEntry{
		FoodID:    &food.ID,
		FoodName:  food.Name,
		Grams:     grams,
		Nutrition: food.Scale(grams),
	}
	return entry, amount, food.SourceID, nil
}

// inlineEntry takes the nutrition as absolute values for the given grams, the
// same shape the catalogue path snapshots.
func inlineEntry(req addEntryRequest) (domain.LogEntry, error) {
	if req.Grams <= 0 || req.Nutrition == nil || req.Nutrition.Kcal == nil {
		return domain.LogEntry{}, domain.ErrInvalidInput
	}

	return domain.LogEntry{
		FoodName: req.Name,
		Grams:    req.Grams,
		Nutrition: domain.Nutrition{
			Kcal:    *req.Nutrition.Kcal,
			Protein: req.Nutrition.Protein,
			Fat:     req.Nutrition.Fat,
			Carbs:   req.Nutrition.Carbs,
		},
	}, nil
}

// listEntries is the history read: a flat page of entries, newest first. It
// computes no totals; callers sum what they need.
func (s *Server) listEntries(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	today := truncateDay(time.Now().UTC())
	from, err := queryTime(c, "from", today, false)
	if err != nil {
		RespondWithError(c, err)
		return
	}
	to, err := queryTime(c, "to", endOfDay(today), true)
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
