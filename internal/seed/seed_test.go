package seed

import (
	"math"
	"strings"
	"testing"
	"unicode"

	"github.com/arvlas/calbot/internal/domain"
)

func TestLoadCatalogue(t *testing.T) {
	foods, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(foods) < 300 {
		t.Fatalf("Load() returned %d foods, want at least 300", len(foods))
	}

	assertAnchors(t, foods)
	assertCatalogueQuality(t, foods)
	assertNaturalAliases(t, foods)
}

func assertAnchors(t *testing.T, foods []domain.Food) {
	t.Helper()
	anchors := map[string]domain.Nutrition{
		"Цукор":                {Kcal: 400, Protein: 0, Fat: 0, Carbs: 100},
		"Соняшникова олія":     {Kcal: 884, Protein: 0, Fat: 100, Carbs: 0},
		"Масло вершкове 82%":   {Kcal: 748, Protein: 0.5, Fat: 82, Carbs: 0.8},
		"Яйце куряче, варене":  {Kcal: 155, Protein: 13, Fat: 11, Carbs: 1.1},
		"Куряче філе, варене":  {Kcal: 165, Protein: 31, Fat: 3.6, Carbs: 0},
		"Свинина, смажена":     {Kcal: 297, Protein: 27, Fat: 21, Carbs: 0},
		"Яловичина, варена":    {Kcal: 254, Protein: 27, Fat: 15, Carbs: 0},
		"Гречка, варена":       {Kcal: 110, Protein: 4, Fat: 1.1, Carbs: 20},
		"Рис білий, варений":   {Kcal: 130, Protein: 2.7, Fat: 0.3, Carbs: 28},
		"Макарони, варені":     {Kcal: 131, Protein: 5, Fat: 1.1, Carbs: 25},
		"Вівсянка на воді":     {Kcal: 71, Protein: 2.5, Fat: 1.5, Carbs: 12},
		"Картопля, варена":     {Kcal: 87, Protein: 2, Fat: 0.1, Carbs: 20},
		"Молоко 2,5%":          {Kcal: 52, Protein: 2.9, Fat: 2.5, Carbs: 4.7},
		"Сметана 20%":          {Kcal: 206, Protein: 2.8, Fat: 20, Carbs: 3.4},
		"Сир твердий (Гауда)":  {Kcal: 356, Protein: 25, Fat: 27, Carbs: 2.2},
		"Хліб пшеничний":       {Kcal: 265, Protein: 9, Fat: 3.2, Carbs: 49},
		"Борщ":                 {Kcal: 49, Protein: 1.5, Fat: 2.2, Carbs: 6},
		"Вареники з картоплею": {Kcal: 180, Protein: 4.5, Fat: 4, Carbs: 31},
		"Банан":                {Kcal: 89, Protein: 1.1, Fat: 0.3, Carbs: 23},
		"Яблуко":               {Kcal: 52, Protein: 0.3, Fat: 0.2, Carbs: 14},
		"Огірок":               {Kcal: 15, Protein: 0.7, Fat: 0.1, Carbs: 3.6},
		"Помідор":              {Kcal: 18, Protein: 0.9, Fat: 0.2, Carbs: 3.9},
		"Морква, сира":         {Kcal: 41, Protein: 0.9, Fat: 0.2, Carbs: 10},
		"Мед":                  {Kcal: 304, Protein: 0.3, Fat: 0, Carbs: 82},
		"Snickers":             {Kcal: 488, Protein: 8.2, Fat: 24, Carbs: 60},
	}

	byName := make(map[string]domain.Food, len(foods))
	for _, food := range foods {
		byName[food.Name] = food
	}
	for name, want := range anchors {
		food, ok := byName[name]
		if !ok {
			t.Errorf("anchor %q is missing", name)
			continue
		}
		assertWithinThreePercent(t, name+" kcal", food.Nutrition.Kcal, want.Kcal)
		assertWithinThreePercent(t, name+" protein", food.Nutrition.Protein, want.Protein)
		assertWithinThreePercent(t, name+" fat", food.Nutrition.Fat, want.Fat)
		assertWithinThreePercent(t, name+" carbs", food.Nutrition.Carbs, want.Carbs)
	}
}

func assertCatalogueQuality(t *testing.T, foods []domain.Food) {
	t.Helper()
	bannedAliasParts := []string{
		"готовий",
		"готовый",
		"готове",
		"без приготування",
		"без приготовления",
		"сырое",
		"приготовлено",
	}
	diaryAliases := map[string]struct{}{
		"Snickers\x00Snickers Snickers":                            {},
		"Snickers Peanut Butter\x00Snickers Peanut Butter Crunchy": {},
		"Курка, запечена\x00Курка запечена в духовці або на вогні": {},
	}
	seenNames := make(map[string]struct{}, len(foods))
	tuples := make(map[string]int, len(foods))
	for _, food := range foods {
		nameKey := strings.ToLower(strings.TrimSpace(food.Name))
		if _, exists := seenNames[nameKey]; exists {
			t.Errorf("duplicate food name %q", food.Name)
		}
		seenNames[nameKey] = struct{}{}

		key := nutritionKey(food.Nutrition)
		tuples[key]++
		if tuples[key] > 4 {
			t.Errorf("nutrition tuple %s is shared by more than four entries", key)
		}

		if food.Nutrition.Kcal > 20 {
			atwater := 4*food.Nutrition.Protein + 9*food.Nutrition.Fat + 4*food.Nutrition.Carbs
			if math.Abs(food.Nutrition.Kcal-atwater) > food.Nutrition.Kcal*0.2 {
				t.Errorf("food %q fails Atwater check: kcal %.1f, calculated %.1f", food.Name, food.Nutrition.Kcal, atwater)
			}
		}

		defaults := 0
		for _, portion := range food.Portions {
			if portion.IsDefault {
				defaults++
			}
		}
		if defaults != 1 {
			t.Errorf("food %q has %d default portions, want 1", food.Name, defaults)
		}

		languages := make(map[string]bool, 2)
		useful := make(map[string]struct{}, len(food.Aliases))
		normalizedName := normalizeAliasForQualityCheck(food.Name)
		for _, alias := range food.Aliases {
			languages[alias.Lang] = true
			aliasKey := strings.ToLower(strings.TrimSpace(alias.Alias))
			if aliasKey != nameKey {
				useful[aliasKey] = struct{}{}
			}
			for _, banned := range bannedAliasParts {
				if strings.Contains(aliasKey, banned) {
					t.Errorf("food %q alias %q contains filler %q", food.Name, alias.Alias, banned)
				}
			}
			normalizedAlias := normalizeAliasForQualityCheck(alias.Alias)
			_, isDiaryAlias := diaryAliases[food.Name+"\x00"+alias.Alias]
			if strings.HasPrefix(normalizedAlias, normalizedName+" ") && !isDiaryAlias {
				t.Errorf("food %q alias %q is the entry name with a suffix", food.Name, alias.Alias)
			}
		}
		if len(food.Aliases) < 4 || len(food.Aliases) > 8 {
			t.Errorf("food %q has %d aliases, want 4 to 8", food.Name, len(food.Aliases))
		}
		if !languages["uk"] || !languages["ru"] {
			t.Errorf("food %q must have both uk and ru aliases", food.Name)
		}
		if len(useful) < 2 {
			t.Errorf("food %q has %d useful aliases, want at least 2", food.Name, len(useful))
		}

		if isCookedName(nameKey) && food.State != domain.StateCooked {
			t.Errorf("prepared food %q has state %q, want cooked", food.Name, food.State)
		}
		if food.Kind == domain.KindDish && food.State != domain.StateRaw && food.State != domain.StateCooked {
			t.Errorf("dish %q has state %q, want raw or cooked", food.Name, food.State)
		}
	}
	if float64(len(tuples))/float64(len(foods)) < 0.85 {
		t.Errorf("got %d distinct nutrition tuples for %d foods, want at least 85%%", len(tuples), len(foods))
	}
}

func assertNaturalAliases(t *testing.T, foods []domain.Food) {
	t.Helper()
	want := map[string]string{
		"варена курка":       "Куряче філе, варене",
		"вареная курица":     "Куряче філе, варене",
		"яйцо":               "Яйце куряче, варене",
		"сахар":              "Цукор",
		"борщ":               "Борщ",
		"гречка":             "Гречка, варена",
		"подсолнечное масло": "Соняшникова олія",
		"сливочное масло":    "Масло вершкове 82%",
		"творог":             "Сир кисломолочний 5%",
		"картошка":           "Картопля, варена",
	}

	aliasesByName := make(map[string]map[string]struct{}, len(foods))
	for _, food := range foods {
		aliases := make(map[string]struct{}, len(food.Aliases))
		for _, alias := range food.Aliases {
			aliases[strings.ToLower(strings.TrimSpace(alias.Alias))] = struct{}{}
		}
		aliasesByName[food.Name] = aliases
	}
	for alias, name := range want {
		aliases, ok := aliasesByName[name]
		if !ok {
			t.Errorf("natural alias target %q is missing", name)
			continue
		}
		if _, ok = aliases[alias]; !ok {
			t.Errorf("food %q is missing natural alias %q", name, alias)
		}
	}
}

func normalizeAliasForQualityCheck(value string) string {
	var normalized strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			normalized.WriteRune(char)
			continue
		}
		normalized.WriteByte(' ')
	}
	return strings.Join(strings.Fields(normalized.String()), " ")
}

func assertWithinThreePercent(t *testing.T, field string, got, want float64) {
	t.Helper()
	if want == 0 {
		if got != 0 {
			t.Errorf("%s = %.2f, want 0", field, got)
		}
		return
	}
	if math.Abs(got-want)/want > 0.03 {
		t.Errorf("%s = %.2f, want %.2f within 3%%", field, got, want)
	}
}
