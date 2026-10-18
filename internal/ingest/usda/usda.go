// Package usda imports redistributable generic foods from USDA FoodData
// Central, which is CC0.
package usda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
	"golang.org/x/time/rate"
)

const (
	searchEndpoint = "https://api.nal.usda.gov/fdc/v1/foods/search"
	detailsURL     = "https://fdc.nal.usda.gov/food-details/%d/nutrients"
	pageSize       = 25
	// usdaRPS stays low because DEMO_KEY allows only a few dozen requests an hour.
	usdaRPS = rate.Limit(1)
	// dataTypes keeps to generic foods; Branded rows are US retail products.
	dataTypes = "Foundation,SR Legacy"

	// cursorDone is saved when every query is done so the next run starts over.
	cursorDone = "0"

	nutrientEnergy  = "Energy"
	nutrientProtein = "Protein"
	nutrientFat     = "Total lipid (fat)"
	nutrientCarbs   = "Carbohydrate, by difference"
	unitKcal        = "KCAL"
	unitKilojoules  = "KJ"
)

// Adapter runs a configured list of generic food queries.
type Adapter struct {
	http    *ingest.HTTPClient
	apiKey  string
	queries []string
}

// New constructs a USDA adapter.
func New(httpClient *ingest.HTTPClient, apiKey string, queries []string) *Adapter {
	return &Adapter{http: httpClient, apiKey: apiKey, queries: queries}
}

// Name returns the source identifier stored with catalogue rows.
func (a *Adapter) Name() string { return "usda" }

// USDA FoodData Central is CC0, and a reference table outranks a scraped row
// during promote.
func (a *Adapter) Provenance() ingest.Provenance {
	return ingest.Provenance{
		LicenseClass: domain.LicenseRedistributable,
		Confidence:   domain.ConfidenceReference,
	}
}

type searchResponse struct {
	Foods []json.RawMessage `json:"foods"`
}

type food struct {
	FDCID         int            `json:"fdcId"`
	Description   string         `json:"description"`
	FoodNutrients []foodNutrient `json:"foodNutrients"`
}

type foodNutrient struct {
	Name  string  `json:"nutrientName"`
	Value float64 `json:"value"`
	Unit  string  `json:"unitName"`
}

// Fetch stores the index of the next query in its cursor.
func (a *Adapter) Fetch(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	a.http.ResetCache()
	start, err := parseCursor(cursor)
	if err != nil {
		return "", err
	}
	for i := start; i < len(a.queries); i++ {
		err = a.fetchQuery(ctx, a.queries[i], strconv.Itoa(i+1), emit)
		if err != nil {
			return "", err
		}
	}
	return cursorDone, nil
}

func (a *Adapter) fetchQuery(ctx context.Context, query, nextCursor string, emit func(ingest.Raw) error) error {
	params := url.Values{}
	params.Set("query", query)
	params.Set("pageSize", strconv.Itoa(pageSize))
	params.Set("dataType", dataTypes)
	params.Set("api_key", a.apiKey)
	response, err := a.http.Get(ctx, a.Name(), searchEndpoint+"?"+params.Encode(), usdaRPS, true)
	if err != nil {
		return err
	}
	var result searchResponse
	decodeErr := json.NewDecoder(response.Body).Decode(&result)
	response.Body.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode USDA query %q: %w", query, decodeErr)
	}

	for _, item := range result.Foods {
		var f food
		err = json.Unmarshal(item, &f)
		if err != nil {
			return fmt.Errorf("decode USDA food for query %q: %w", query, err)
		}
		err = emit(ingest.Raw{
			ExternalID: strconv.Itoa(f.FDCID),
			Payload:    item,
			SourceURL:  fmt.Sprintf(detailsURL, f.FDCID),
			Cursor:     nextCursor,
			Product:    rawProduct(f),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// rawProduct matches nutrients by name. Energy is reported in kcal for most
// rows and in kJ for a few; both are passed and normalization prefers kcal.
func rawProduct(f food) ingest.RawProduct {
	product := ingest.RawProduct{Name: strings.TrimSpace(f.Description)}
	for _, nutrient := range f.FoodNutrients {
		value := strconv.FormatFloat(nutrient.Value, 'f', -1, 64)
		switch nutrient.Name {
		case nutrientEnergy:
			switch strings.ToUpper(nutrient.Unit) {
			case unitKcal:
				product.Kcal = value
			case unitKilojoules:
				product.Kilojoules = value
			}
		case nutrientProtein:
			product.Protein = value
		case nutrientFat:
			product.Fat = value
		case nutrientCarbs:
			product.Carbs = value
		}
	}
	return product
}

func parseCursor(cursor string) (int, error) {
	if cursor == "" || cursor == cursorDone {
		return 0, nil
	}
	index, err := strconv.Atoi(cursor)
	if err != nil || index < 0 {
		return 0, fmt.Errorf("invalid USDA cursor %q", cursor)
	}
	return index, nil
}
