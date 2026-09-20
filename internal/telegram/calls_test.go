package telegram

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/resolver"
	"github.com/arvlas/calbot/internal/storage/postgres"
)

// The bot decodes API responses into its own structs so the two can change
// independently, which means only the JSON tags hold them together. Nothing in
// the compiler checks that, and a silent mismatch decodes to a zero value: a
// recent food rendered with its grams but no name, and a food_id of 0 that
// fails on save.
//
// Each case marshals what the API really returns and asserts the fields the bot
// relies on survive the trip.

func TestRecentsDecodesFromAPIShape(t *testing.T) {
	sent := []postgres.RecentFood{{
		Food:      domain.Food{ID: 42, Name: "Куряче філе, варене"},
		Count:     7,
		LastGrams: 150,
	}}

	var got []RecentFood
	roundTrip(t, sent, &got)

	if len(got) != 1 {
		t.Fatalf("got %d recents, want 1", len(got))
	}
	if got[0].Food.ID != 42 {
		t.Errorf("food id = %d, want 42: a zero id fails on save", got[0].Food.ID)
	}
	if got[0].Food.Name != "Куряче філе, варене" {
		t.Errorf("name = %q, want the food's name", got[0].Food.Name)
	}
	if got[0].LastGrams != 150 {
		t.Errorf("last grams = %v, want 150", got[0].LastGrams)
	}
}

func TestResolveDecodesFromAPIShape(t *testing.T) {
	sent := resolver.Result{
		Candidates: []resolver.Candidate{{
			Food:  domain.Food{ID: 7, Name: "Борщ", Nutrition: domain.Nutrition{Kcal: 49}},
			Score: 0.91,
		}},
		Quantity:   &resolver.Quantity{Grams: 350},
		Confidence: resolver.ConfidenceHigh,
	}

	var got ResolveResult
	roundTrip(t, sent, &got)

	if len(got.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got.Candidates))
	}
	if got.Candidates[0].Food.ID != 7 || got.Candidates[0].Food.Name != "Борщ" {
		t.Errorf("candidate food = %+v, want id 7 named Борщ", got.Candidates[0].Food)
	}
	if got.Candidates[0].Food.Nutrition.Kcal != 49 {
		t.Errorf("kcal = %v, want 49", got.Candidates[0].Food.Nutrition.Kcal)
	}
	if !got.HighConfidence() {
		t.Errorf("confidence = %q, want the high band to be recognised", got.Confidence)
	}
	if got.Quantity == nil || got.Quantity.Grams != 350 {
		t.Errorf("quantity = %+v, want 350 g", got.Quantity)
	}
}

func TestDayDecodesFromAPIShape(t *testing.T) {
	sent := domain.Day{
		Date: time.Now(),
		Entries: []domain.LogEntry{{
			ID: 3, FoodName: "Гречка, варена", Grams: 200,
			Nutrition: domain.Nutrition{Kcal: 220, Protein: 8},
		}},
		Consumed:      domain.Nutrition{Kcal: 220, Protein: 8},
		KcalTarget:    1800,
		ProteinTarget: 117,
	}

	var got DayView
	roundTrip(t, sent, &got)

	if len(got.Entries) != 1 || got.Entries[0].FoodName != "Гречка, варена" {
		t.Errorf("entries = %+v, want one named entry", got.Entries)
	}
	if got.KcalTarget != 1800 || got.ProteinTarget != 117 {
		t.Errorf("targets = %d/%d, want 1800/117", got.KcalTarget, got.ProteinTarget)
	}
	if got.Consumed.Kcal != 220 {
		t.Errorf("consumed = %v, want 220", got.Consumed.Kcal)
	}
}

func roundTrip(t *testing.T, sent, into any) {
	t.Helper()

	encoded, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal API response: %v", err)
	}

	err = json.Unmarshal(encoded, into)
	if err != nil {
		t.Fatalf("decode into the bot's shape: %v", err)
	}
}
