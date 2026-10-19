package ingest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arvlas/calbot/internal/domain"
)

// maxPackageG keeps a mistyped package weight out of foods.package_g, which
// holds at most 999999.99 and fails the whole promote batch beyond that.
const maxPackageG = 100_000

var (
	numberPattern  = regexp.MustCompile(`[-+]?\d+(?:[.,]\d+)?`)
	rangeSeparator = regexp.MustCompile(`(\d)\s*-\s*(\d)`)
)

// Normalize converts an adapter row to the catalogue's per-100 g format.
func Normalize(source string, provenance Provenance, raw Raw) (Normalized, error) {
	if strings.TrimSpace(raw.ExternalID) == "" {
		return Normalized{}, fmt.Errorf("missing external ID")
	}
	if strings.TrimSpace(raw.Product.Name) == "" {
		return Normalized{}, fmt.Errorf("missing product name")
	}

	nutrition, err := NormalizeNutrition(NutritionInput{
		Kcal:       raw.Product.Kcal,
		Kilojoules: raw.Product.Kilojoules,
		Protein:    raw.Product.Protein,
		Fat:        raw.Product.Fat,
		Carbs:      raw.Product.Carbs,
	})
	if err != nil {
		return Normalized{}, err
	}

	packageG, err := parsePackageGrams(raw.Product.Quantity)
	if err != nil {
		return Normalized{}, fmt.Errorf("parse package quantity: %w", err)
	}
	fetchedAt := time.Now().UTC()
	food := domain.Food{
		Kind:      domain.KindProduct,
		Name:      strings.TrimSpace(raw.Product.Name),
		Brand:     strings.TrimSpace(raw.Product.Brand),
		Barcode:   strings.TrimSpace(raw.Product.Barcode),
		State:     domain.StateAsSold,
		Nutrition: nutrition,
		PackageG:  packageG,
		SourceID:  source,
		SourceURL: raw.SourceURL,
		FetchedAt: &fetchedAt,

		LicenseClass: provenance.LicenseClass,
		Confidence:   provenance.Confidence,
	}
	return food, nil
}

// NutritionInput carries the text values printed by a source per 100 g.
type NutritionInput struct {
	Kcal       string
	Kilojoules string
	Protein    string
	Fat        string
	Carbs      string
}

// NormalizeNutrition parses messy source values into nutrition per 100 g.
func NormalizeNutrition(input NutritionInput) (domain.Nutrition, error) {
	kcal, err := parseEnergy(input.Kcal, input.Kilojoules)
	if err != nil {
		return domain.Nutrition{}, fmt.Errorf("parse energy: %w", err)
	}
	protein, err := parseNumber(input.Protein)
	if err != nil {
		return domain.Nutrition{}, fmt.Errorf("parse protein: %w", err)
	}
	fat, err := parseNumber(input.Fat)
	if err != nil {
		return domain.Nutrition{}, fmt.Errorf("parse fat: %w", err)
	}
	carbs, err := parseNumber(input.Carbs)
	if err != nil {
		return domain.Nutrition{}, fmt.Errorf("parse carbohydrates: %w", err)
	}
	return domain.Nutrition{Kcal: kcal, Protein: protein, Fat: fat, Carbs: carbs}, nil
}

func parseEnergy(kcalText, kilojouleText string) (float64, error) {
	kcalText = strings.TrimSpace(kcalText)
	if kcalText != "" {
		if strings.Contains(kcalText, "/") {
			parts := strings.SplitN(kcalText, "/", 2)
			kcal, err := parseNumber(parts[0])
			if err != nil {
				return 0, err
			}
			_, err = parseNumber(parts[1])
			if err != nil {
				return 0, fmt.Errorf("invalid kcal/kJ pair: %w", err)
			}
			return kcal, nil
		}
		value, err := parseNumber(kcalText)
		if err != nil {
			return 0, err
		}
		if strings.Contains(strings.ToLower(kcalText), "kj") || strings.Contains(strings.ToLower(kcalText), "кдж") {
			return value / 4.184, nil
		}
		return value, nil
	}
	if strings.TrimSpace(kilojouleText) == "" {
		return 0, fmt.Errorf("missing kcal and kJ")
	}
	kilojoules, err := parseNumber(kilojouleText)
	if err != nil {
		return 0, err
	}
	return kilojoules / 4.184, nil
}

func parseNumber(text string) (float64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("empty value")
	}
	parseText := rangeSeparator.ReplaceAllString(text, "$1 $2")
	matches := numberPattern.FindAllString(parseText, -1)
	if len(matches) == 0 || len(matches) > 2 {
		return 0, fmt.Errorf("unsupported value %q", text)
	}
	values := make([]float64, 0, len(matches))
	for _, match := range matches {
		value, err := strconv.ParseFloat(strings.ReplaceAll(match, ",", "."), 64)
		if err != nil {
			return 0, fmt.Errorf("parse %q: %w", text, err)
		}
		values = append(values, value)
	}
	if len(values) == 2 {
		if values[1] < values[0] {
			return 0, fmt.Errorf("descending range %q", text)
		}
		return (values[0] + values[1]) / 2, nil
	}
	if strings.HasPrefix(text, "<") {
		return values[0] / 2, nil
	}
	return values[0], nil
}

func parsePackageGrams(text string) (*float64, error) {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return nil, nil
	}
	grams, err := parseNumber(text)
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, "kg") || strings.Contains(text, "кг") {
		grams *= 1000
	}
	if grams <= 0 {
		return nil, fmt.Errorf("package weight must be positive")
	}
	if grams > maxPackageG {
		return nil, fmt.Errorf("package weight exceeds %d g", maxPackageG)
	}
	return &grams, nil
}
