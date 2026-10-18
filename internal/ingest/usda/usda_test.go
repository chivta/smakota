package usda

import (
	"testing"

	"github.com/arvlas/calbot/internal/ingest"
)

func TestRawProduct(t *testing.T) {
	tests := []struct {
		name string
		food food
		want ingest.RawProduct
	}{
		{
			name: "kcal row wins, extra nutrients ignored",
			food: food{Description: " Cheese, cheddar ", FoodNutrients: []foodNutrient{
				{Name: "Protein", Value: 23.3, Unit: "G"},
				{Name: "Total lipid (fat)", Value: 33.1, Unit: "G"},
				{Name: "Carbohydrate, by difference", Value: 3.09, Unit: "G"},
				{Name: "Energy", Value: 1670, Unit: "kJ"},
				{Name: "Energy", Value: 403, Unit: "KCAL"},
				{Name: "Sodium, Na", Value: 653, Unit: "MG"},
			}},
			want: ingest.RawProduct{Name: "Cheese, cheddar", Kcal: "403", Kilojoules: "1670", Protein: "23.3", Fat: "33.1", Carbs: "3.09"},
		},
		{
			name: "similar names do not match",
			food: food{Description: "Oil", FoodNutrients: []foodNutrient{
				{Name: "Energy (Atwater General Factors)", Value: 884, Unit: "KCAL"},
				{Name: "Fatty acids, total saturated", Value: 10, Unit: "G"},
			}},
			want: ingest.RawProduct{Name: "Oil"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rawProduct(tt.food); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
