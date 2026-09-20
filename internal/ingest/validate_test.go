package ingest

import (
	"strings"
	"testing"

	"github.com/arvlas/calbot/internal/domain"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name       string
		nutrition  domain.Nutrition
		wantReason string
	}{
		{
			name:      "valid",
			nutrition: domain.Nutrition{Kcal: 170, Protein: 10, Fat: 10, Carbs: 10},
		},
		{
			name:       "Atwater mismatch",
			nutrition:  domain.Nutrition{Kcal: 400, Protein: 5, Fat: 5, Carbs: 5},
			wantReason: "Atwater energy mismatch",
		},
		{
			name:       "macros over 100",
			nutrition:  domain.Nutrition{Kcal: 500, Protein: 40, Fat: 30, Carbs: 31},
			wantReason: "macros exceed 100",
		},
		{
			name:       "kcal over 900",
			nutrition:  domain.Nutrition{Kcal: 901},
			wantReason: "kcal exceeds 900",
		},
		{
			name:       "negative",
			nutrition:  domain.Nutrition{Kcal: 10, Protein: -1},
			wantReason: "negative value",
		},
		{
			name:      "low calorie rounding skips Atwater",
			nutrition: domain.Nutrition{Kcal: 19, Protein: 0, Fat: 0, Carbs: 0},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.nutrition)
			if test.wantReason == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantReason) {
				t.Fatalf("Validate() error = %v, want reason containing %q", err, test.wantReason)
			}
		})
	}
}
