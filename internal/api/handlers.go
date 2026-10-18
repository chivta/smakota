package api

import (
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/resolver"
	"github.com/gin-gonic/gin"
)

func (s *Server) getMe(c *gin.Context) {
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

	c.JSON(http.StatusOK, user)
}

type registerRequest struct {
	Sex           domain.Sex           `json:"sex"            validate:"required,oneof=male female"`
	Age           int                  `json:"age"            validate:"required,gte=14,lte=100"`
	HeightCM      int                  `json:"height_cm"      validate:"required,gte=120,lte=230"`
	WeightKG      float64              `json:"weight_kg"      validate:"required,gte=30,lte=300"`
	GoalWeightKG  float64              `json:"goal_weight_kg" validate:"required,gte=30,lte=300"`
	ActivityLevel domain.ActivityLevel `json:"activity_level" validate:"required,oneof=sedentary light moderate high very_high"`
	Timezone      string               `json:"timezone"`
}

// register completes onboarding. It computes maintenance and the deficit target
// from Mifflin-St Jeor and returns both, so the client can show the rationale
// and offer a manual override.
func (s *Server) register(c *gin.Context) {
	telegramID, err := s.identify(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	var req registerRequest
	err = bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	now := time.Now()
	user := domain.User{
		TelegramID:           telegramID,
		Sex:                  req.Sex,
		Age:                  req.Age,
		HeightCM:             req.HeightCM,
		WeightKG:             req.WeightKG,
		GoalWeightKG:         req.GoalWeightKG,
		ActivityLevel:        req.ActivityLevel,
		Timezone:             defaultString(req.Timezone, "Europe/Kyiv"),
		NudgeAt:              "20:00",
		NudgeEnabled:         true,
		WeeklySummaryEnabled: true,
		OnboardedAt:          &now,
	}
	user.KcalTarget = user.SuggestedTarget()
	user.ProteinTargetG = user.SuggestedProtein()

	created, err := s.users.Create(c.Request.Context(), user)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"user":        created,
		"maintenance": created.Maintenance(),
		"target":      created.KcalTarget,
		"protein":     created.ProteinTargetG,
	})
}

type updateMeRequest struct {
	KcalTarget           *int     `json:"kcal_target"            validate:"omitempty,gte=800,lte=6000"`
	ProteinTargetG       *int     `json:"protein_target_g"       validate:"omitempty,gte=20,lte=400"`
	WeightKG             *float64 `json:"weight_kg"              validate:"omitempty,gte=30,lte=300"`
	NudgeAt              *string  `json:"nudge_at"`
	NudgeEnabled         *bool    `json:"nudge_enabled"`
	WeeklySummaryEnabled *bool    `json:"weekly_summary_enabled"`
}

func (s *Server) updateMe(c *gin.Context) {
	telegramID, err := s.identify(c)
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

	user, err := s.users.GetByTelegramID(c.Request.Context(), telegramID)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	applyIf(req.KcalTarget, &user.KcalTarget)
	applyIf(req.ProteinTargetG, &user.ProteinTargetG)
	applyIf(req.WeightKG, &user.WeightKG)
	applyIf(req.NudgeAt, &user.NudgeAt)
	applyIf(req.NudgeEnabled, &user.NudgeEnabled)
	applyIf(req.WeeklySummaryEnabled, &user.WeeklySummaryEnabled)

	err = s.users.Update(c.Request.Context(), user)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, user)
}

type resolveRequest struct {
	Input         string           `json:"input"         validate:"required,max=500"`
	InputType     domain.InputType `json:"input_type"    validate:"required,oneof=text voice photo barcode recent"`
	Clarification string           `json:"clarification" validate:"max=500"`
}

// resolve runs one pass of the shared pipeline every input mode converges on.
// It records the attempt for the acceptance metrics, and queues a review item
// when nothing clears the floor.
func (s *Server) resolve(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	var req resolveRequest
	err = bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	started := time.Now()
	result, err := s.resolver.Resolve(c.Request.Context(), resolver.Request{
		RawInput:      req.Input,
		InputType:     req.InputType,
		UserID:        userID,
		Clarification: req.Clarification,
	})
	if err != nil {
		RespondWithError(c, err)
		return
	}

	attempt := domain.Attempt{
		UserID:         userID,
		InputType:      req.InputType,
		LatencyMS:      int(time.Since(started).Milliseconds()),
		RefinementUsed: req.Clarification != "",
		Resolved:       len(result.Candidates) > 0,
	}
	logSoftError(s.attempts.Add(c.Request.Context(), attempt), "record attempt")

	if len(result.Candidates) == 0 {
		item := domain.ReviewItem{
			UserID:    &userID,
			RawInput:  req.Input,
			InputType: req.InputType,
			Reason:    domain.ReviewUnresolved,
		}
		logSoftError(s.reviews.Add(c.Request.Context(), item), "queue review item")
		RespondWithError(c, domain.ErrUnresolved)
		return
	}

	c.JSON(http.StatusOK, result)
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

// getByBarcode is the Mini App scanner's lookup. A miss queues a review item so
// the barcode reaches the expansion list even if the user gives up.
func (s *Server) getByBarcode(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	code := c.Param("code")
	food, err := s.foods.GetByBarcode(c.Request.Context(), code)
	if err != nil {
		item := domain.ReviewItem{
			UserID:    &userID,
			RawInput:  code,
			InputType: domain.InputBarcode,
			Reason:    domain.ReviewBarcodeMiss,
		}
		logSoftError(s.reviews.Add(c.Request.Context(), item), "queue barcode miss")
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

	c.JSON(http.StatusCreated, created)
}
