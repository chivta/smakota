package mcdonalds

import (
	"math"
	"os"
	"slices"
	"testing"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
)

func fixtureItems(t *testing.T) ([]item, []string) {
	t.Helper()
	text, err := os.ReadFile("testdata/consist.txt")
	if err != nil {
		t.Fatal(err)
	}
	return parseItems(string(text))
}

func TestParseItemsReadsEnDashAndCommaValues(t *testing.T) {
	items, _ := fixtureItems(t)
	want := []item{
		{Name: "ЧІКЕН ДЖУНІОР", ServingG: 110, Kcal: 280, Protein: 9, Fat: 11, Carbs: 36},
		{Name: "ФІШ РОЛ", ServingG: 190, Kcal: 436, Protein: 18, Fat: 19.4, Carbs: 45.9},
	}
	if !slices.Equal(items, want) {
		t.Fatalf("got %+v, want %+v", items, want)
	}
}

func TestParseItemsSkipsItemsWithoutServingMass(t *testing.T) {
	_, skipped := fixtureItems(t)
	want := []string{"СИРОП БРАУНІ", "ВОДА ГАЗОВАНА велика"}
	if !slices.Equal(skipped, want) {
		t.Fatalf("got %v, want %v", skipped, want)
	}
}

func TestRawForScalesServingToPer100g(t *testing.T) {
	items, _ := fixtureItems(t)
	raw := rawFor(items[0], "https://example.test/consist.pdf")

	// 280 kcal in 110 g is 254.5 kcal per 100 g, not 280.
	want := ingest.RawProduct{Name: "ЧІКЕН ДЖУНІОР", Kcal: "254.55", Protein: "8.18", Fat: "10.00", Carbs: "32.73"}
	if raw.Product != want {
		t.Fatalf("got %+v, want %+v", raw.Product, want)
	}

	food, err := ingest.Normalize("mcdonalds", (&Adapter{}).Provenance(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = ingest.Validate(food.Nutrition); err != nil {
		t.Fatal(err)
	}
	if food.Kind != domain.KindDish || food.State != domain.StateCooked {
		t.Fatalf("got kind %q state %q", food.Kind, food.State)
	}
	portion, ok := food.DefaultPortion()
	if !ok || portion.Name != "порція" || portion.Grams != 110 {
		t.Fatalf("got portion %+v", portion)
	}
	if got := food.Scale(portion.Grams).Kcal; math.Abs(got-280) > 0.1 {
		t.Fatalf("one portion is %.2f kcal, want 280", got)
	}
}
