package api

import (
	"math"
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/metrics"
	"github.com/arvlas/calbot/internal/resolver"
	"github.com/arvlas/calbot/internal/storage/postgres"
	"github.com/gin-gonic/gin"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100

	defaultFrequentLimit = 20
	maxFrequentLimit     = 100
)

// Outcome labels of metrics.ResolverAttempts.
const (
	searchOutcomeHit   = "hit"
	searchOutcomeEmpty = "empty"
)

func (s *Server) getMe(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	user, err := s.users.GetByID(c.Request.Context(), userID)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, user)
}

type updateMeRequest struct {
	KcalTarget     *int     `json:"kcal_target"            validate:"omitempty,gte=800,lte=6000"`
	ProteinTargetG *int     `json:"protein_target_g"       validate:"omitempty,gte=20,lte=400"`
	WeightKG       *float64 `json:"weight_kg"              validate:"omitempty,gte=30,lte=300"`
}

func (s *Server) updateMe(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	var req updateMeRequest
	err = bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	user, err := s.users.GetByID(c.Request.Context(), userID)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	applyIf(req.KcalTarget, &user.KcalTarget)
	applyIf(req.ProteinTargetG, &user.ProteinTargetG)
	applyIf(req.WeightKG, &user.WeightKG)

	err = s.users.Update(c.Request.Context(), user)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, user)
}

// searchFoods ranks catalogue foods against a free-text query, with the
// caller's history bonus. It has no side effects, and a miss is an empty array.
func (s *Server) searchFoods(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	limit, err := queryInt(c, "limit", defaultSearchLimit, 1, maxSearchLimit)
	if err != nil {
		RespondWithError(c, err)
		return
	}
	offset, err := queryInt(c, "offset", 0, 0, math.MaxInt32)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	started := time.Now()
	found, err := s.foods.Search(c.Request.Context(), postgres.SearchParams{
		Query:  resolver.Normalize(c.Query("q")),
		Locale: c.Query("locale"),
		UserID: &userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondWithError(c, err)
		return
	}

	outcome := searchOutcomeHit
	if len(found) == 0 {
		outcome = searchOutcomeEmpty
	}
	input := string(domain.InputText)
	metrics.ResolverAttempts.WithLabelValues(input, outcome).Inc()
	metrics.ResolverLatency.WithLabelValues(input).Observe(time.Since(started).Seconds())

	metrics.SearchResults.Observe(float64(len(found)))

	c.JSON(http.StatusOK, found)
}

// frequentFoods ranks the caller's foods by lifetime log count. Unlike search
// it applies no recency decay.
func (s *Server) frequentFoods(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	limit, err := queryInt(c, "limit", defaultFrequentLimit, 1, maxFrequentLimit)
	if err != nil {
		RespondWithError(c, err)
		return
	}
	offset, err := queryInt(c, "offset", 0, 0, math.MaxInt32)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	found, err := s.foods.Frequent(c.Request.Context(), userID, limit, offset)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, found)
}

func (s *Server) getFood(c *gin.Context) {
	id, err := pathInt(c, "id")
	if err != nil {
		RespondWithError(c, err)
		return
	}

	food, err := s.foods.GetByID(c.Request.Context(), id)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, food)
}

// getByBarcode is the Mini App scanner's lookup. A miss is a plain 404.
func (s *Server) getByBarcode(c *gin.Context) {
	food, err := s.foods.GetByBarcode(c.Request.Context(), c.Param("code"))
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, food)
}

type personalFoodRequest struct {
	Name    string  `json:"name"    validate:"required,max=200"`
	Kcal    float64 `json:"kcal"    validate:"required,gt=0,lte=900"`
	Protein float64 `json:"protein" validate:"gte=0,lte=100"`
	Fat     float64 `json:"fat"     validate:"gte=0,lte=100"`
	Carbs   float64 `json:"carbs"   validate:"gte=0,lte=100"`
}

// createPersonalFood is the escape hatch after two failed refinements. The entry
// belongs to this user and ranks ahead of the catalogue for them.
func (s *Server) createPersonalFood(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	var req personalFoodRequest
	err = bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	food := domain.Food{
		Kind:         domain.KindDish,
		Name:         req.Name,
		State:        domain.StateAsSold,
		Nutrition:    domain.Nutrition{Kcal: req.Kcal, Protein: req.Protein, Fat: req.Fat, Carbs: req.Carbs},
		SourceID:     "user",
		LicenseClass: domain.LicenseUser,
		Confidence:   domain.ConfidenceUser,
		OwnerUserID:  &userID,
	}

	created, err := s.foods.CreatePersonal(c.Request.Context(), userID, food)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	item := domain.ReviewItem{
		UserID:    &userID,
		RawInput:  req.Name,
		InputType: domain.InputText,
		Reason:    domain.ReviewManualEntry,
	}
	logSoftError(s.reviews.Add(c.Request.Context(), item), "queue manual entry")
	metrics.PersonalFoods.Inc()

	c.JSON(http.StatusCreated, created)
}
