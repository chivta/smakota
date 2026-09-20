// Package seed loads the hand-verified curated food catalogue.
package seed

import (
	_ "embed"
	"fmt"
	"math"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"gopkg.in/yaml.v3"
)

const minimumCatalogueSize = 300

//go:embed dishes.yaml
var catalogueYAML []byte

var allowedPortionNames = map[string]struct{}{
	"тарілка": {},
	"шматок":  {},
	"склянка": {},
	"порція":  {},
	"ложка":   {},
	"штука":   {},
}

type catalogueEntry struct {
	Name      string             `yaml:"name"`
	Kind      domain.Kind        `yaml:"kind"`
	State     domain.State       `yaml:"state"`
	Nutrition domain.Nutrition   `yaml:"nutrition"`
	Portions  []cataloguePortion `yaml:"portions"`
	Aliases   []catalogueAlias   `yaml:"aliases"`
}

type cataloguePortion struct {
	Name    string  `yaml:"name"`
	Grams   float64 `yaml:"grams"`
	Default bool    `yaml:"default"`
}

type catalogueAlias struct {
	Alias string `yaml:"alias"`
	Lang  string `yaml:"lang"`
}

// Load validates the embedded curated catalogue and maps it to domain foods.
func Load() ([]domain.Food, error) {
	var entries []catalogueEntry
	err := yaml.Unmarshal(catalogueYAML, &entries)
	if err != nil {
		return nil, fmt.Errorf("parse curated catalogue: %w", err)
	}
	if len(entries) < minimumCatalogueSize {
		return nil, fmt.Errorf("validate curated catalogue: got %d entries, need at least %d", len(entries), minimumCatalogueSize)
	}

	foods := make([]domain.Food, 0, len(entries))
	seenNames := make(map[string]struct{}, len(entries))
	tupleCounts := make(map[string]int, len(entries))
	for i, entry := range entries {
		err = validateEntry(entry, seenNames)
		if err != nil {
			return nil, fmt.Errorf("validate curated catalogue entry %d %q: %w", i+1, entry.Name, err)
		}

		key := nutritionKey(entry.Nutrition)
		tupleCounts[key]++
		if tupleCounts[key] > 4 {
			return nil, fmt.Errorf("validate curated catalogue: nutrition tuple %s is shared by more than four entries", key)
		}

		portions := make([]domain.Portion, 0, len(entry.Portions))
		for _, portion := range entry.Portions {
			portions = append(portions, domain.Portion{
				Name:      portion.Name,
				Grams:     portion.Grams,
				IsDefault: portion.Default,
			})
		}

		aliases := make([]domain.Alias, 0, len(entry.Aliases))
		for _, alias := range entry.Aliases {
			aliases = append(aliases, domain.Alias{Alias: alias.Alias, Lang: alias.Lang})
		}

		foods = append(foods, domain.Food{
			Kind:         entry.Kind,
			Name:         entry.Name,
			State:        entry.State,
			Nutrition:    entry.Nutrition,
			SourceID:     "curated",
			LicenseClass: domain.LicenseRedistributable,
			Confidence:   domain.ConfidenceHandVerified,
			Portions:     portions,
			Aliases:      aliases,
		})
	}
	if float64(len(tupleCounts))/float64(len(entries)) < 0.85 {
		return nil, fmt.Errorf("validate curated catalogue: got %d distinct nutrition tuples for %d entries", len(tupleCounts), len(entries))
	}

	return foods, nil
}

func validateEntry(entry catalogueEntry, seenNames map[string]struct{}) error {
	name := strings.TrimSpace(entry.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	nameKey := strings.ToLower(name)
	_, exists := seenNames[nameKey]
	if exists {
		return fmt.Errorf("duplicate name")
	}
	seenNames[nameKey] = struct{}{}

	switch entry.Kind {
	case domain.KindDish:
		if entry.State != domain.StateRaw && entry.State != domain.StateCooked {
			return fmt.Errorf("dish state must be raw or cooked, got %q", entry.State)
		}
	case domain.KindProduct:
		if entry.State != domain.StateAsSold {
			return fmt.Errorf("product state must be as_sold, got %q", entry.State)
		}
	default:
		return fmt.Errorf("invalid kind %q", entry.Kind)
	}
	if isCookedName(nameKey) && entry.State != domain.StateCooked {
		return fmt.Errorf("prepared food name requires cooked state")
	}
	if err := validateNutrition(entry.Nutrition); err != nil {
		return err
	}
	if err := validatePortions(entry.Portions); err != nil {
		return err
	}
	if err := validateAliases(nameKey, entry.Aliases); err != nil {
		return err
	}
	return nil
}

func validateNutrition(nutrition domain.Nutrition) error {
	if nutrition.Kcal <= 0 || nutrition.Kcal > 900 {
		return fmt.Errorf("kcal must be between 0 and 900")
	}
	if nutrition.Protein < 0 || nutrition.Fat < 0 || nutrition.Carbs < 0 {
		return fmt.Errorf("macronutrients cannot be negative")
	}
	if nutrition.Protein+nutrition.Fat+nutrition.Carbs > 100 {
		return fmt.Errorf("macronutrients exceed 100 g")
	}
	if nutrition.Kcal > 20 {
		atwater := 4*nutrition.Protein + 9*nutrition.Fat + 4*nutrition.Carbs
		if math.Abs(nutrition.Kcal-atwater) > nutrition.Kcal*0.2 {
			return fmt.Errorf("Atwater check failed: kcal %.1f, calculated %.1f", nutrition.Kcal, atwater)
		}
	}
	return nil
}

func validatePortions(portions []cataloguePortion) error {
	if len(portions) == 0 {
		return fmt.Errorf("at least one portion is required")
	}
	defaults := 0
	seen := make(map[string]struct{}, len(portions))
	for _, portion := range portions {
		_, ok := allowedPortionNames[portion.Name]
		if !ok {
			return fmt.Errorf("unsupported portion name %q", portion.Name)
		}
		if portion.Grams <= 0 {
			return fmt.Errorf("portion %q must have positive grams", portion.Name)
		}
		_, exists := seen[portion.Name]
		if exists {
			return fmt.Errorf("duplicate portion name %q", portion.Name)
		}
		seen[portion.Name] = struct{}{}
		if portion.Default {
			defaults++
		}
	}
	if defaults != 1 {
		return fmt.Errorf("expected exactly one default portion, got %d", defaults)
	}
	return nil
}

func validateAliases(nameKey string, aliases []catalogueAlias) error {
	if len(aliases) == 0 {
		return fmt.Errorf("at least one alias is required")
	}
	languages := make(map[string]bool, 2)
	useful := make(map[string]struct{}, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		aliasKey := strings.ToLower(strings.TrimSpace(alias.Alias))
		if aliasKey == "" {
			return fmt.Errorf("alias text is required")
		}
		if alias.Lang != "uk" && alias.Lang != "ru" {
			return fmt.Errorf("alias %q has unsupported language %q", alias.Alias, alias.Lang)
		}
		key := alias.Lang + "\x00" + aliasKey
		_, exists := seen[key]
		if exists {
			return fmt.Errorf("duplicate alias %q for language %q", alias.Alias, alias.Lang)
		}
		seen[key] = struct{}{}
		languages[alias.Lang] = true
		if aliasKey != nameKey {
			useful[aliasKey] = struct{}{}
		}
	}
	if !languages["uk"] || !languages["ru"] {
		return fmt.Errorf("both Ukrainian and Russian aliases are required")
	}
	if len(useful) < 2 {
		return fmt.Errorf("at least two distinct aliases different from the name are required")
	}
	return nil
}

func isCookedName(name string) bool {
	for _, marker := range []string{"варен", "смажен", "печен", "тушков"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func nutritionKey(n domain.Nutrition) string {
	return fmt.Sprintf("%.3f|%.3f|%.3f|%.3f", n.Kcal, n.Protein, n.Fat, n.Carbs)
}
