package domain

import (
	"strings"
	"time"
)

type Sex string

const (
	SexMale   Sex = "male"
	SexFemale Sex = "female"
)

// ActivityLevel is stored profile data.
type ActivityLevel string

const (
	ActivitySedentary ActivityLevel = "sedentary"
	ActivityLight     ActivityLevel = "light"
	ActivityModerate  ActivityLevel = "moderate"
	ActivityHigh      ActivityLevel = "high"
	ActivityVeryHigh  ActivityLevel = "very_high"
)

// DayEditWindow is how long a past day stays editable. Beyond it, edits are
// rejected with ErrDayLocked.
const DayEditWindow = 7 * 24 * time.Hour

type User struct {
	ID             int64         `json:"id"`
	Email          string        `json:"email,omitempty"`
	Sex            Sex           `json:"sex"`
	Age            int           `json:"age"`
	HeightCM       int           `json:"height_cm"`
	WeightKG       float64       `json:"weight_kg"`
	GoalWeightKG   float64       `json:"goal_weight_kg"`
	ActivityLevel  ActivityLevel `json:"activity_level"`
	KcalTarget     int           `json:"kcal_target"`
	ProteinTargetG int           `json:"protein_target_g"`
	Timezone       string        `json:"timezone"`
	OnboardedAt    *time.Time    `json:"-"`
}

// NormalizeEmail is the stored and looked-up form of an email address.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
