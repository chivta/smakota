package ingest

import (
	"math"
	"testing"

	"github.com/arvlas/calbot/internal/domain"
)

func TestNormalizeNutrition(t *testing.T) {
	tests := []struct {
		name  string
		input NutritionInput
		want  domain.Nutrition
	}{
		{
			name:  "Silpo kcal and kJ pair",
			input: NutritionInput{Kcal: "748/3075", Protein: "0,5 г", Fat: "82 г", Carbs: "1 г"},
			want:  domain.Nutrition{Kcal: 748, Protein: 0.5, Fat: 82, Carbs: 1},
		},
		{
			name:  "kJ only",
			input: NutritionInput{Kilojoules: "418,4 kJ", Protein: "10", Fat: "2", Carbs: "10"},
			want:  domain.Nutrition{Kcal: 100, Protein: 10, Fat: 2, Carbs: 10},
		},
		{
			name:  "comma decimal",
			input: NutritionInput{Kcal: "100", Protein: "12,5 г", Fat: "2,5 г", Carbs: "5,0 г"},
			want:  domain.Nutrition{Kcal: 100, Protein: 12.5, Fat: 2.5, Carbs: 5},
		},
		{
			name:  "range midpoint",
			input: NutritionInput{Kcal: "10-12 kcal", Protein: "1", Fat: "1", Carbs: "1"},
			want:  domain.Nutrition{Kcal: 11, Protein: 1, Fat: 1, Carbs: 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeNutrition(test.input)
			if err != nil {
				t.Fatalf("NormalizeNutrition() error = %v", err)
			}
			if !nutritionClose(got, test.want) {
				t.Fatalf("NormalizeNutrition() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func nutritionClose(got, want domain.Nutrition) bool {
	return math.Abs(got.Kcal-want.Kcal) < 0.001 &&
		math.Abs(got.Protein-want.Protein) < 0.001 &&
		math.Abs(got.Fat-want.Fat) < 0.001 &&
		math.Abs(got.Carbs-want.Carbs) < 0.001
}

func TestNormalizeNutritionRejectsUnsupportedText(t *testing.T) {
	_, err := NormalizeNutrition(NutritionInput{Kcal: "many", Protein: "1", Fat: "1", Carbs: "1"})
	if err == nil {
		t.Fatal("NormalizeNutrition() error = nil, want parsing error")
	}
}

func TestParsePackageGrams(t *testing.T) {
	tests := []struct {
		text    string
		want    float64
		wantErr bool
	}{
		{"250 g", 250, false},
		{"1,5 kg", 1500, false},
		{"100000 g", 100000, false},
		{"100001 g", 0, true},
		{"5000000", 0, true},
		{"0 g", 0, true},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			got, err := parsePackageGrams(test.text)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %v", err, test.wantErr)
			}
			if err == nil && *got != test.want {
				t.Fatalf("grams = %v, want %v", *got, test.want)
			}
		})
	}
}
