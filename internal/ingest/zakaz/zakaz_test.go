package zakaz

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/arvlas/calbot/internal/ingest"
)

func TestFlattenLeaves(t *testing.T) {
	tests := []struct {
		name string
		tree []category
		want []string
	}{
		{name: "empty", tree: nil, want: nil},
		{
			name: "leaves under nested parents, in order",
			tree: []category{
				{ID: "ready-meals", Children: []category{{ID: "salads"}, {ID: "second-courses", Children: []category{{ID: "kebab"}}}}},
				{ID: "bakery", Children: []category{{ID: "own-bakery-novus"}}},
				{ID: "milk"},
			},
			want: []string{"salads", "kebab", "own-bakery-novus", "milk"},
		},
		{
			name: "collections are skipped with their children",
			tree: []category{
				{ID: "low-prices", IsCollection: true},
				{ID: "promo", IsCollection: true, Children: []category{{ID: "inside"}}},
				{ID: "cheese"},
			},
			want: []string{"cheese"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flattenLeaves(tt.tree)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("flattenLeaves() = %v, want %v", got, tt.want)
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
		{name: "empty starts over", cursor: "", want: cursorPosition{}},
		{name: "done starts over", cursor: cursorDone, want: cursorPosition{}},
		{name: "store category page", cursor: "48246401|ready-meals-auchan|3", want: cursorPosition{store: "48246401", category: "ready-meals-auchan", page: 3}},
		{name: "too few parts", cursor: "48246401|ready-meals-auchan", wantErr: true},
		{name: "empty store", cursor: "|salads|1", wantErr: true},
		{name: "empty category", cursor: "482010105||1", wantErr: true},
		{name: "page not a number", cursor: "482010105|salads|x", wantErr: true},
		{name: "page below one", cursor: "482010105|salads|0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCursor(tt.cursor)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCursor(%q) error = %v, wantErr %v", tt.cursor, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseCursor(%q) = %+v, want %+v", tt.cursor, got, tt.want)
			}
			if !tt.wantErr && got.store != "" && formatCursor(got) != tt.cursor {
				t.Errorf("formatCursor(%+v) = %q, want %q", got, formatCursor(got), tt.cursor)
			}
		})
	}
}

func TestToRaw(t *testing.T) {
	tests := []struct {
		name    string
		item    string
		wantOK  bool
		wantErr bool
		want    ingest.RawProduct
	}{
		{
			name:   "counter item with chain-prefixed ean",
			item:   `{"title":"Салат Олів'є","ean":"novus02855969000000","weight":0,"producer":{"trademark":"Майстри Смаку"},"nutrition_facts":{"ingredient_energy":"232.70ккал","ingredient_protein":"5.90г","ingredient_fat":"21.30г","ingredient_carbohydrates":"4.40г"}}`,
			wantOK: true,
			want: ingest.RawProduct{
				Name: "Салат Олів'є", Brand: "Майстри Смаку", Barcode: "novus02855969000000",
				Kcal: "232.70ккал", Protein: "5.90г", Fat: "21.30г", Carbs: "4.40г",
			},
		},
		{
			name:   "missing carbohydrates stays empty for the validator to reject",
			item:   `{"title":"Шашлик курячий","ean":"tavriav02000000243368","weight":300,"nutrition_facts":{"ingredient_energy":"168ккал","ingredient_protein":"16.5г","ingredient_fat":"11г"}}`,
			wantOK: true,
			want: ingest.RawProduct{
				Name: "Шашлик курячий", Barcode: "tavriav02000000243368", Quantity: "300 g",
				Kcal: "168ккал", Protein: "16.5г", Fat: "11г",
			},
		},
		{name: "no nutrition panel is skipped", item: `{"title":"Хліб","ean":"4820000000001","nutrition_facts":{}}`},
		{name: "null nutrition is skipped", item: `{"title":"Хліб","ean":"4820000000001","nutrition_facts":null}`},
		{name: "no ean is skipped", item: `{"title":"Хліб","nutrition_facts":{"ingredient_energy":"200ккал"}}`},
		{name: "malformed json errors", item: `{"title":`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, ok, err := toRaw("482010105", "ready-meals", 2, json.RawMessage(tt.item))
			if (err != nil) != tt.wantErr {
				t.Fatalf("toRaw() error = %v, wantErr %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("toRaw() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if raw.Product != tt.want {
				t.Errorf("Product = %+v, want %+v", raw.Product, tt.want)
			}
			if raw.ExternalID != tt.want.Barcode {
				t.Errorf("ExternalID = %q, want ean %q", raw.ExternalID, tt.want.Barcode)
			}
			if raw.Cursor != "482010105|ready-meals|2" {
				t.Errorf("Cursor = %q", raw.Cursor)
			}
		})
	}
}
