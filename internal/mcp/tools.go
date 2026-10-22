package mcp

import (
	"context"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/metrics"
	"github.com/arvlas/calbot/internal/resolver"
	"github.com/arvlas/calbot/internal/storage/postgres"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultSearchLimit   = 20
	maxSearchLimit       = 100
	defaultFrequentLimit = 20
	maxFrequentLimit     = 100
	defaultEntriesLimit  = 100
	maxEntriesLimit      = 500

	maxNameLen     = 200
	maxPortionName = 50
	maxGrams       = 5000
	maxPortionQty  = 50
	maxPortions    = 5
	maxFoodKcal    = 900
	maxFoodMacro   = 100

	foodSourceUser = "user"
)

const (
	toolSearchFoods   = "search_foods"
	toolGetFood       = "get_food"
	toolLogEntry      = "log_entry"
	toolListEntries   = "list_entries"
	toolFrequentFoods = "frequent_foods"
	toolCreateFood    = "create_food"
)

const (
	descSearchFoods = "Search the shared food catalogue by free text (Ukrainian, Russian or English; typos and word order are tolerated). " +
		"Returns ranked candidates, best first, each with a food and a score; a miss is an empty list, not an error. " +
		"Every catalogue entry states its nutrition per 100 g, not per portion; the food's portions list the named servings and their grams. " +
		"Foods you have logged before rank higher. Searching is optional: to log something you do not find, call log_entry with inline nutrition."

	descGetFood = "Fetch one catalogue food by id, with its nutrition per 100 g and its named portions (each portion's weight is in grams)."

	descLogEntry = "Log something the caller ate. You never need a lookup first: either pass food_id of a catalogue food, " +
		"or pass name plus nutrition for an inline food (the nutrition is then the absolute values for the grams eaten, not per 100 g, and grams is required). " +
		"For a catalogue food the amount is either grams, or portion_name (an exact name from the food's portions) multiplied by portion_qty (default 1); " +
		"when neither is given the food's default portion is used, or 100 g if it has none. " +
		"Nutrition is snapshotted into the entry when it is saved, so a later catalogue correction never rewrites a logged day. " +
		"logged_at is an RFC 3339 timestamp of when it was eaten and defaults to now. Returns the saved entry."

	descListEntries = "List the caller's logged entries between two moments, newest first. from and to accept an RFC 3339 timestamp or a bare date (YYYY-MM-DD, a whole UTC day); " +
		"both default to today (UTC). Each entry carries the grams eaten and the nutrition snapshotted when it was logged (absolute values for those grams). No totals are computed; sum what you need."

	descFrequentFoods = "The caller's most-logged catalogue foods, by lifetime log count, with the count and the time of the last log. " +
		"Useful to log a habitual food by food_id without searching. Nutrition is per 100 g."

	descCreateFood = "Contribute a new food to the shared catalogue, visible to every user. Use it for a food worth reusing; " +
		"for a one-off, call log_entry with inline nutrition instead. kcal, protein, fat and carbs are per 100 g. " +
		"Optional portions name common servings with their weight in grams (at most five, at most one marked default; the first is the default when none is marked). " +
		"Returns the created food, whose id can be passed to log_entry."
)

func (t *tools) register(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: toolSearchFoods, Description: descSearchFoods, Annotations: readOnly()}, t.searchFoods)
	sdk.AddTool(s, &sdk.Tool{Name: toolGetFood, Description: descGetFood, Annotations: readOnly()}, t.getFood)
	sdk.AddTool(s, &sdk.Tool{Name: toolLogEntry, Description: descLogEntry}, t.logEntry)
	sdk.AddTool(s, &sdk.Tool{Name: toolListEntries, Description: descListEntries, Annotations: readOnly()}, t.listEntries)
	sdk.AddTool(s, &sdk.Tool{Name: toolFrequentFoods, Description: descFrequentFoods, Annotations: readOnly()}, t.frequentFoods)
	sdk.AddTool(s, &sdk.Tool{Name: toolCreateFood, Description: descCreateFood}, t.createFood)
}

func readOnly() *sdk.ToolAnnotations { return &sdk.ToolAnnotations{ReadOnlyHint: true} }

type searchFoodsInput struct {
	Query  string `json:"query"            jsonschema:"free-text food name"`
	Locale string `json:"locale,omitempty" jsonschema:"optional language bias for ranking, e.g. uk, ru or en"`
	Limit  int    `json:"limit,omitempty"  jsonschema:"max results, 1-100, default 20"`
}

type searchFoodsOutput struct {
	Results []postgres.Candidate `json:"results"`
}

func (t *tools) searchFoods(ctx context.Context, _ *sdk.CallToolRequest, in searchFoodsInput) (*sdk.CallToolResult, searchFoodsOutput, error) {
	userID, err := currentUser(ctx)
	if err != nil {
		return nil, searchFoodsOutput{}, err
	}
	limit, err := boundedLimit(in.Limit, defaultSearchLimit, maxSearchLimit)
	if err != nil {
		return nil, searchFoodsOutput{}, err
	}

	found, err := t.foods.Search(ctx, postgres.SearchParams{
		Query:  resolver.Normalize(in.Query),
		Locale: in.Locale,
		UserID: &userID,
		Limit:  limit,
	})
	if err != nil {
		return nil, searchFoodsOutput{}, err
	}
	if found == nil {
		found = []postgres.Candidate{}
	}

	metrics.SearchResults.Observe(float64(len(found)))
	return nil, searchFoodsOutput{Results: found}, nil
}

type getFoodInput struct {
	ID int64 `json:"id" jsonschema:"catalogue food id"`
}

func (t *tools) getFood(ctx context.Context, _ *sdk.CallToolRequest, in getFoodInput) (*sdk.CallToolResult, domain.Food, error) {
	if in.ID <= 0 {
		return nil, domain.Food{}, domain.ErrInvalidInput
	}
	food, err := t.foods.GetByID(ctx, in.ID)
	return nil, food, err
}

type inlineNutrition struct {
	Kcal    *float64 `json:"kcal"              jsonschema:"kilocalories for the grams eaten"`
	Protein float64  `json:"protein,omitempty" jsonschema:"grams of protein for the grams eaten"`
	Fat     float64  `json:"fat,omitempty"     jsonschema:"grams of fat for the grams eaten"`
	Carbs   float64  `json:"carbs,omitempty"   jsonschema:"grams of carbohydrate for the grams eaten"`
}

type logEntryInput struct {
	FoodID      *int64           `json:"food_id,omitempty"      jsonschema:"catalogue food id; exclusive with name"`
	Name        string           `json:"name,omitempty"         jsonschema:"inline food name; exclusive with food_id"`
	Nutrition   *inlineNutrition `json:"nutrition,omitempty"    jsonschema:"inline food only: absolute nutrition for the grams eaten"`
	Grams       float64          `json:"grams,omitempty"        jsonschema:"amount eaten in grams, up to 5000; required for an inline food"`
	PortionName string           `json:"portion_name,omitempty" jsonschema:"catalogue food only: exact name of one of the food's portions"`
	PortionQty  float64          `json:"portion_qty,omitempty"  jsonschema:"how many of portion_name, up to 50; default 1"`
	LoggedAt    string           `json:"logged_at,omitempty"    jsonschema:"RFC 3339 time it was eaten; default now"`
}

func (t *tools) logEntry(ctx context.Context, _ *sdk.CallToolRequest, in logEntryInput) (*sdk.CallToolResult, domain.LogEntry, error) {
	userID, err := currentUser(ctx)
	if err != nil {
		return nil, domain.LogEntry{}, err
	}
	if in.Grams < 0 || in.Grams > maxGrams || in.PortionQty < 0 || in.PortionQty > maxPortionQty || len(in.Name) > maxNameLen {
		return nil, domain.LogEntry{}, domain.ErrInvalidInput
	}

	var entry domain.LogEntry
	var path, amount, foodSource string
	switch {
	case in.FoodID != nil && *in.FoodID > 0 && in.Name == "" && in.Nutrition == nil:
		path = domain.LogPathCatalogue
		entry, amount, foodSource, err = t.catalogueEntry(ctx, *in.FoodID, in)
	case in.FoodID == nil && in.Name != "":
		path, amount, foodSource = domain.LogPathInline, domain.AmountGrams, domain.SourceInline
		entry, err = inlineEntry(in)
	default:
		err = domain.ErrInvalidInput
	}
	if err != nil {
		return nil, domain.LogEntry{}, err
	}

	entry.UserID = userID
	entry.LoggedAt = time.Now()
	if in.LoggedAt != "" {
		entry.LoggedAt, err = time.Parse(time.RFC3339, in.LoggedAt)
		if err != nil {
			return nil, domain.LogEntry{}, domain.ErrInvalidInput
		}
	}

	saved, err := t.logs.Add(ctx, entry)
	if err != nil {
		return nil, domain.LogEntry{}, err
	}

	metrics.Entries.WithLabelValues(path, amount, foodSource).Inc()
	return nil, saved, nil
}

func (t *tools) catalogueEntry(ctx context.Context, foodID int64, in logEntryInput) (domain.LogEntry, string, string, error) {
	food, err := t.foods.GetByID(ctx, foodID)
	if err != nil {
		return domain.LogEntry{}, "", "", err
	}
	grams, amount, err := domain.ResolveAmount(food, in.Grams, in.PortionName, in.PortionQty)
	if err != nil {
		return domain.LogEntry{}, "", "", err
	}
	entry := domain.LogEntry{
		FoodID:    &food.ID,
		FoodName:  food.Name,
		Grams:     grams,
		Nutrition: food.Scale(grams),
	}
	return entry, amount, food.SourceID, nil
}

func inlineEntry(in logEntryInput) (domain.LogEntry, error) {
	n := in.Nutrition
	if in.Grams <= 0 || n == nil || n.Kcal == nil || *n.Kcal < 0 {
		return domain.LogEntry{}, domain.ErrInvalidInput
	}
	return domain.LogEntry{
		FoodName: in.Name,
		Grams:    in.Grams,
		Nutrition: domain.Nutrition{
			Kcal:    *n.Kcal,
			Protein: n.Protein,
			Fat:     n.Fat,
			Carbs:   n.Carbs,
		},
	}, nil
}

type listEntriesInput struct {
	From  string `json:"from,omitempty"  jsonschema:"range start: RFC 3339 or YYYY-MM-DD; default start of today (UTC)"`
	To    string `json:"to,omitempty"    jsonschema:"range end: RFC 3339 or YYYY-MM-DD (inclusive of that day); default end of today (UTC)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max entries, 1-500, default 100"`
}

type listEntriesOutput struct {
	Entries []domain.LogEntry `json:"entries"`
}

func (t *tools) listEntries(ctx context.Context, _ *sdk.CallToolRequest, in listEntriesInput) (*sdk.CallToolResult, listEntriesOutput, error) {
	userID, err := currentUser(ctx)
	if err != nil {
		return nil, listEntriesOutput{}, err
	}
	limit, err := boundedLimit(in.Limit, defaultEntriesLimit, maxEntriesLimit)
	if err != nil {
		return nil, listEntriesOutput{}, err
	}

	today := truncateDay(time.Now().UTC())
	from, err := parseTime(in.From, today, false)
	if err != nil {
		return nil, listEntriesOutput{}, err
	}
	to, err := parseTime(in.To, endOfDay(today), true)
	if err != nil {
		return nil, listEntriesOutput{}, err
	}
	if to.Before(from) {
		return nil, listEntriesOutput{}, domain.ErrInvalidInput
	}

	entries, err := t.logs.Range(ctx, userID, from, to, limit, 0)
	if err != nil {
		return nil, listEntriesOutput{}, err
	}
	if entries == nil {
		entries = []domain.LogEntry{}
	}
	return nil, listEntriesOutput{Entries: entries}, nil
}

type frequentFoodsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"max foods, 1-100, default 20"`
}

type frequentFoodsOutput struct {
	Foods []postgres.FrequentFood `json:"foods"`
}

func (t *tools) frequentFoods(ctx context.Context, _ *sdk.CallToolRequest, in frequentFoodsInput) (*sdk.CallToolResult, frequentFoodsOutput, error) {
	userID, err := currentUser(ctx)
	if err != nil {
		return nil, frequentFoodsOutput{}, err
	}
	limit, err := boundedLimit(in.Limit, defaultFrequentLimit, maxFrequentLimit)
	if err != nil {
		return nil, frequentFoodsOutput{}, err
	}

	found, err := t.foods.Frequent(ctx, userID, limit, 0)
	if err != nil {
		return nil, frequentFoodsOutput{}, err
	}
	if found == nil {
		found = []postgres.FrequentFood{}
	}
	return nil, frequentFoodsOutput{Foods: found}, nil
}

type portionInput struct {
	Name      string  `json:"name"                 jsonschema:"serving name, e.g. 'slice' or '1 cup'"`
	Grams     float64 `json:"grams"                jsonschema:"weight of one serving in grams"`
	IsDefault bool    `json:"is_default,omitempty" jsonschema:"mark as the default serving"`
}

type createFoodInput struct {
	Name     string         `json:"name"               jsonschema:"food name"`
	Kcal     float64        `json:"kcal"               jsonschema:"kilocalories per 100 g"`
	Protein  float64        `json:"protein,omitempty"  jsonschema:"grams of protein per 100 g"`
	Fat      float64        `json:"fat,omitempty"      jsonschema:"grams of fat per 100 g"`
	Carbs    float64        `json:"carbs,omitempty"    jsonschema:"grams of carbohydrate per 100 g"`
	Portions []portionInput `json:"portions,omitempty" jsonschema:"named servings, at most 5"`
}

func (t *tools) createFood(ctx context.Context, _ *sdk.CallToolRequest, in createFoodInput) (*sdk.CallToolResult, domain.Food, error) {
	userID, err := currentUser(ctx)
	if err != nil {
		return nil, domain.Food{}, err
	}
	if in.Name == "" || len(in.Name) > maxNameLen ||
		in.Kcal <= 0 || in.Kcal > maxFoodKcal ||
		!within(in.Protein, maxFoodMacro) || !within(in.Fat, maxFoodMacro) || !within(in.Carbs, maxFoodMacro) ||
		len(in.Portions) > maxPortions {
		return nil, domain.Food{}, domain.ErrInvalidInput
	}
	portions, err := portionsFromInput(in.Portions)
	if err != nil {
		return nil, domain.Food{}, err
	}

	created, err := t.foods.CreateContributed(ctx, userID, domain.Food{
		Kind:         domain.KindDish,
		Name:         in.Name,
		State:        domain.StateAsSold,
		Nutrition:    domain.Nutrition{Kcal: in.Kcal, Protein: in.Protein, Fat: in.Fat, Carbs: in.Carbs},
		SourceID:     foodSourceUser,
		LicenseClass: domain.LicenseUser,
		Confidence:   domain.ConfidenceUser,
		Portions:     portions,
	})
	if err != nil {
		return nil, domain.Food{}, err
	}

	// The review queue is observability: its failure must not fail the call.
	_ = t.reviews.Add(ctx, domain.ReviewItem{
		UserID:    &userID,
		RawInput:  in.Name,
		InputType: domain.InputText,
		Reason:    domain.ReviewManualEntry,
	})

	metrics.PersonalFoods.Inc()
	return nil, created, nil
}

// portionsFromInput enforces one default at most, and the first portion as the
// default when none is marked.
func portionsFromInput(in []portionInput) ([]domain.Portion, error) {
	portions := make([]domain.Portion, 0, len(in))
	defaults := 0
	for _, p := range in {
		if p.Name == "" || len(p.Name) > maxPortionName || p.Grams <= 0 || p.Grams > maxGrams {
			return nil, domain.ErrInvalidInput
		}
		if p.IsDefault {
			defaults++
		}
		portions = append(portions, domain.Portion{Name: p.Name, Grams: p.Grams, IsDefault: p.IsDefault})
	}
	if defaults > 1 {
		return nil, domain.ErrInvalidInput
	}
	if defaults == 0 && len(portions) > 0 {
		portions[0].IsDefault = true
	}
	return portions, nil
}

func within(v, hi float64) bool { return v >= 0 && v <= hi }

// boundedLimit treats zero as "not given".
func boundedLimit(v, fallback, hi int) (int, error) {
	switch {
	case v == 0:
		return fallback, nil
	case v < 0 || v > hi:
		return 0, domain.ErrInvalidInput
	}
	return v, nil
}

// parseTime reads an RFC 3339 timestamp or a bare date; for a bare date, end
// selects the day's last instant instead of its midnight.
func parseTime(raw string, fallback time.Time, end bool) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err == nil {
		return t, nil
	}
	day, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, domain.ErrInvalidInput
	}
	if end {
		return endOfDay(day), nil
	}
	return day, nil
}

func truncateDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func endOfDay(day time.Time) time.Time {
	return truncateDay(day).AddDate(0, 0, 1).Add(-time.Microsecond)
}
