package silpo

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		cursor   string
		category string
		offset   int
		wantErr  bool
	}{
		{name: "empty starts over", cursor: ""},
		{name: "done marker starts over", cursor: cursorDone},
		{name: "mid category", cursor: "1edb13c0-69c3-6622-b5d4-53f89724fa3a:200", category: "1edb13c0-69c3-6622-b5d4-53f89724fa3a", offset: 200},
		{name: "no offset", cursor: "abc", wantErr: true},
		{name: "negative offset", cursor: "abc:-1", wantErr: true},
		{name: "bad offset", cursor: "abc:x", wantErr: true},
		{name: "empty category", cursor: ":5", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			category, offset, err := parseCursor(tt.cursor)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if category != tt.category || offset != tt.offset {
				t.Fatalf("got %q/%d, want %q/%d", category, offset, tt.category, tt.offset)
			}
			if tt.category != "" && formatCursor(category, offset) != tt.cursor {
				t.Fatalf("format = %q, want %q", formatCursor(category, offset), tt.cursor)
			}
		})
	}
}

func TestFoodLeafCategories(t *testing.T) {
	const food, nonFood = "napoi-52", "dlia-domu-567"
	categories := []category{
		{ID: "f", Slug: food},
		{ID: "f1", Slug: "soky", ParentID: "f"},
		{ID: "f2", Slug: "vody", ParentID: "f"},
		{ID: "f21", Slug: "mineralni", ParentID: "f2"},
		{ID: "n", Slug: nonFood},
		{ID: "n1", Slug: "myttia", ParentID: "n"},
		{ID: "n11", Slug: "gel", ParentID: "n1"},
		{ID: "x", Slug: "orphan", ParentID: "missing"},
		{ID: "noslug", ParentID: "f"},
		{ID: "cycleA", Slug: "a", ParentID: "cycleB"},
		{ID: "cycleB", Slug: "b", ParentID: "cycleA"},
	}
	tests := []struct {
		name       string
		categories []category
		want       []string
	}{
		{name: "empty", categories: nil, want: nil},
		{name: "mixed tree keeps food leaves only", categories: categories, want: []string{"soky", "mineralni"}},
		{name: "food top level that is itself a leaf", categories: []category{{ID: "f", Slug: food}}, want: []string{food}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := foodLeafCategories(tt.categories)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFetchDetailNutrition(t *testing.T) {
	const live = `{"title":"Яйця","externalProductId":181642,"attributeGroups":[
	{"key":"generalInfo","attributes":[]},
	{"key":"nutrient","attributes":[
	{"attribute":{"key":"calorie"},"value":{"key":"160/669,4","title":"160/669,4"}},
	{"attribute":{"key":"proteins"},"value":{"key":null,"title":13}},
	{"attribute":{"key":"fats"},"value":{"key":null,"title":11.5}},
	{"attribute":{"key":"carbohydrates"},"value":{"key":null,"title":0.9}}]}]}`
	tests := []struct {
		name    string
		payload string
		want    map[string]string
		wantErr bool
	}{
		{name: "live shape", payload: live, want: map[string]string{"calorie": "160/669,4", "proteins": "13", "fats": "11.5", "carbohydrates": "0.9"}},
		{name: "no nutrient group", payload: `{"attributeGroups":[{"key":"generalInfo","attributes":[]}]}`, wantErr: true},
		{name: "no groups", payload: `{}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var detail map[string]any
			if err := json.Unmarshal([]byte(tt.payload), &detail); err != nil {
				t.Fatal(err)
			}
			got, err := nutrientAttributes(detail)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
