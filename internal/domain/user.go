package domain

import "time"

type Sex string

const (
	SexMale   Sex = "male"
	SexFemale Sex = "female"
)

// ActivityLevel maps onto the Mifflin-St Jeor activity multipliers.
type ActivityLevel string

const (
	ActivitySedentary ActivityLevel = "sedentary"
	ActivityLight     ActivityLevel = "light"
	ActivityModerate  ActivityLevel = "moderate"
	ActivityHigh      ActivityLevel = "high"
	ActivityVeryHigh  ActivityLevel = "very_high"
)

// ActivityMultiplier returns the factor applied to BMR to reach maintenance.
func (a ActivityLevel) Multiplier() float64 {
	switch a {
	case ActivitySedentary:
		return 1.2
	case ActivityLight:
		return 1.375
	case ActivityModerate:
		return 1.55
	case ActivityHigh:
		return 1.725
	case ActivityVeryHigh:
		return 1.9
	}
	return 1.2
}

// DayEditWindow is how long a past day stays editable. Beyond it, edits are
// rejected with ErrDayLocked.
const DayEditWindow = 7 * 24 * time.Hour

// ProteinPerKg is the default protein target, in grams per kg of bodyweight.
const ProteinPerKg = 1.8

// DeficitRatio is the default cut: 20% under maintenance.
const DeficitRatio = 0.8

type User struct {
	ID             int64         `json:"id"`
	TelegramID     int64         `json:"-"`
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

// BMR is Mifflin-St Jeor.
func (u User) BMR() float64 {
	base := 10*u.WeightKG + 6.25*float64(u.HeightCM) - 5*float64(u.Age)
	if u.Sex == SexFemale {
		return base - 161
	}
	return base + 5
}

// Maintenance is BMR scaled by activity.
func (u User) Maintenance() int {
	return int(u.BMR() * u.ActivityLevel.Multiplier())
}

// SuggestedTarget is maintenance minus the default deficit.
func (u User) SuggestedTarget() int {
	return int(float64(u.Maintenance()) * DeficitRatio)
}

// SuggestedProtein is the protein target in grams.
func (u User) SuggestedProtein() int {
	return int(u.WeightKG * ProteinPerKg)
}
