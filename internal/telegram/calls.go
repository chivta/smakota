package telegram

import (
	"context"
	"net/http"
	"strconv"

	"github.com/arvlas/calbot/internal/domain"
)

// The bot decodes into its own shapes rather than importing the resolver's, so
// the two can change independently. Only the JSON tags have to agree.

type Candidate struct {
	Food  domain.Food `json:"food"`
	Score float64     `json:"score"`
}

// Quantity is an amount the resolver found in the phrase itself, in which case
// the portion step is skipped.
type Quantity struct {
	Grams       float64 `json:"grams"`
	Count       float64 `json:"count"`
	PortionName string  `json:"portion_name"`
}

type ResolveResult struct {
	Candidates []Candidate `json:"candidates"`
	Quantity   *Quantity   `json:"quantity,omitempty"`
	Confidence string      `json:"confidence"`
}

// HighConfidence reports whether one candidate is clearly ahead, which is what
// decides between a one-tap confirmation and a list of five.
func (r ResolveResult) HighConfidence() bool {
	return r.Confidence == "high"
}

type RecentFood struct {
	Food      domain.Food `json:"food"`
	Count     int         `json:"count"`
	LastGrams float64     `json:"last_grams"`
}

type DayView struct {
	Entries       []domain.LogEntry `json:"entries"`
	Consumed      domain.Nutrition  `json:"consumed"`
	KcalTarget    int               `json:"kcal_target"`
	ProteinTarget int               `json:"protein_target"`
}

func (c *Client) Resolve(ctx context.Context, tgID int64, input, clarification string, inputType domain.InputType) (ResolveResult, error) {
	body := map[string]any{
		"input":         input,
		"input_type":    inputType,
		"clarification": clarification,
	}
	var out ResolveResult
	err := c.do(ctx, tgID, http.MethodPost, "/v1/resolve", body, &out)
	return out, err
}

func (c *Client) AddEntry(ctx context.Context, tgID, foodID int64, grams float64) (domain.LogEntry, error) {
	body := map[string]any{"food_id": foodID, "grams": grams}
	var out domain.LogEntry
	err := c.do(ctx, tgID, http.MethodPost, "/v1/entries", body, &out)
	return out, err
}

func (c *Client) Day(ctx context.Context, tgID int64, date string) (DayView, error) {
	var out DayView
	err := c.do(ctx, tgID, http.MethodGet, "/v1/days/"+date, nil, &out)
	return out, err
}

func (c *Client) DeleteEntry(ctx context.Context, tgID, entryID int64) error {
	return c.do(ctx, tgID, http.MethodDelete, "/v1/entries/"+strconv.FormatInt(entryID, 10), nil, nil)
}

func (c *Client) UpdateEntry(ctx context.Context, tgID, entryID int64, grams float64) error {
	body := map[string]any{"grams": grams}
	return c.do(ctx, tgID, http.MethodPatch, "/v1/entries/"+strconv.FormatInt(entryID, 10), body, nil)
}

func (c *Client) Recents(ctx context.Context, tgID int64) ([]RecentFood, error) {
	var out []RecentFood
	err := c.do(ctx, tgID, http.MethodGet, "/v1/recents", nil, &out)
	return out, err
}

func (c *Client) RepeatDay(ctx context.Context, tgID int64, date string) (int, error) {
	var out struct {
		Copied int `json:"copied"`
	}
	err := c.do(ctx, tgID, http.MethodPost, "/v1/days/"+date+"/repeat", nil, &out)
	return out.Copied, err
}

func (c *Client) Me(ctx context.Context, tgID int64) (domain.User, error) {
	var out domain.User
	err := c.do(ctx, tgID, http.MethodGet, "/v1/me", nil, &out)
	return out, err
}

type RegisterRequest struct {
	Sex           domain.Sex           `json:"sex"`
	Age           int                  `json:"age"`
	HeightCM      int                  `json:"height_cm"`
	WeightKG      float64              `json:"weight_kg"`
	GoalWeightKG  float64              `json:"goal_weight_kg"`
	ActivityLevel domain.ActivityLevel `json:"activity_level"`
	Timezone      string               `json:"timezone"`
}

type RegisterResult struct {
	User        domain.User `json:"user"`
	Maintenance int         `json:"maintenance"`
	Target      int         `json:"target"`
	Protein     int         `json:"protein"`
}

func (c *Client) Register(ctx context.Context, tgID int64, req RegisterRequest) (RegisterResult, error) {
	var out RegisterResult
	err := c.do(ctx, tgID, http.MethodPost, "/v1/register", req, &out)
	return out, err
}

func (c *Client) CreatePersonalFood(ctx context.Context, tgID int64, name string, kcal float64) (domain.Food, error) {
	body := map[string]any{"name": name, "kcal": kcal}
	var out domain.Food
	err := c.do(ctx, tgID, http.MethodPost, "/v1/foods/personal", body, &out)
	return out, err
}

func (c *Client) AddWeight(ctx context.Context, tgID int64, kg float64) error {
	body := map[string]any{"weight_kg": kg}
	return c.do(ctx, tgID, http.MethodPost, "/v1/weights", body, nil)
}
