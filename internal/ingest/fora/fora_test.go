package fora

import "testing"

func TestNutritionOf(t *testing.T) {
	tests := []struct {
		name   string
		params []parameter
		want   nutrition
		wantOK bool
	}{
		{
			name: "full panel",
			params: []parameter{
				{Key: "country", Value: "Україна"},
				{Key: "calorie", Value: "157/660"},
				{Key: "proteins", Value: "12.7"},
				{Key: "fats", Value: "11.5"},
				{Key: "carbohydrates", Value: "0.7"},
			},
			want:   nutrition{Kcal: "157/660", Protein: "12.7", Fat: "11.5", Carbs: "0.7"},
			wantOK: true,
		},
		{
			name:   "values are trimmed",
			params: []parameter{{Key: "calorie", Value: " 52 "}},
			want:   nutrition{Kcal: "52"},
			wantOK: true,
		},
		{
			name:   "partial panel still counts",
			params: []parameter{{Key: "fats", Value: "3"}},
			want:   nutrition{Fat: "3"},
			wantOK: true,
		},
		{
			name:   "non-food parameters only",
			params: []parameter{{Key: "trademark", Value: "Фора"}, {Key: "country", Value: "Китай"}},
		},
		{
			name:   "blank nutrition value",
			params: []parameter{{Key: "calorie", Value: "  "}},
		},
		{name: "no parameters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := nutritionOf(tt.params)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("got %+v/%v, want %+v/%v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestCursorRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		cursor  string
		want    cursorPosition
		wantErr bool
	}{
		{name: "empty starts over"},
		{name: "done marker starts over", cursor: cursorDone},
		{name: "store start", cursor: "3433|0", want: cursorPosition{filial: 3433}},
		{name: "mid store", cursor: "3410|240", want: cursorPosition{filial: 3410, offset: 240}},
		{name: "no offset", cursor: "3433", wantErr: true},
		{name: "negative offset", cursor: "3433|-60", wantErr: true},
		{name: "bad offset", cursor: "3433|x", wantErr: true},
		{name: "bad store", cursor: "x|60", wantErr: true},
		{name: "zero store", cursor: "0|60", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCursor(tt.cursor)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if tt.want.filial != 0 && formatCursor(got) != tt.cursor {
				t.Fatalf("format = %q, want %q", formatCursor(got), tt.cursor)
			}
		})
	}
}

// Own-production items carry energy under kalorii, kJ first and labelled,
// while packaged goods use calorie as a bare kcal/kJ pair. Reading only the
// latter dropped the whole own-production line as having no energy.
func TestEnergyOf(t *testing.T) {
	tests := []struct {
		name   string
		params []parameter
		want   string
	}{
		{
			name:   "packaged pair passes through",
			params: []parameter{{Key: "calorie", Value: "157/660"}},
			want:   "157/660",
		},
		{
			name:   "own production kcal extracted from labelled kJ-first string",
			params: []parameter{{Key: "kalorii", Value: "1143кДж (kJ) / 272 ккал (kcal)"}},
			want:   "272",
		},
		{
			name:   "decimal kcal",
			params: []parameter{{Key: "kalorii", Value: "900кДж (kJ) / 215.4 ккал (kcal)"}},
			want:   "215.4",
		},
		{
			name: "calorie wins when both present",
			params: []parameter{
				{Key: "kalorii", Value: "1143кДж (kJ) / 272 ккал (kcal)"},
				{Key: "calorie", Value: "157/660"},
			},
			want: "157/660",
		},
		{
			name:   "unrecognised shape handed over so the row is rejected with a reason",
			params: []parameter{{Key: "kalorii", Value: "енергія невідома"}},
			want:   "енергія невідома",
		},
		{
			name:   "neither key",
			params: []parameter{{Key: "country", Value: "Україна"}},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := energyOf(tt.params)
			if got != tt.want {
				t.Errorf("energyOf() = %q, want %q", got, tt.want)
			}
		})
	}
}
