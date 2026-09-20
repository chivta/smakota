package domain

import "time"

// Kind separates the two entity types in the catalogue. Products come from
// barcode databases and retailer catalogues; dishes are the curated Ukrainian
// list of home cooking and raw ingredients.
type Kind string

const (
	KindProduct Kind = "product"
	KindDish    Kind = "dish"
)

// State records which preparation the nutrition values describe. Entries that
// can be eaten either way must say which, and the UI shows it.
type State string

const (
	StateRaw    State = "raw"
	StateCooked State = "cooked"
	StateAsSold State = "as_sold"
)

// LicenseClass decides what may leave the host. Sources whose terms forbid
// reproduction are LicenseLookupOnly: the resolver may render one row to one
// user, but nothing may bulk-export them.
type LicenseClass string

const (
	LicenseRedistributable LicenseClass = "redistributable"
	LicenseLookupOnly      LicenseClass = "lookup_only"
	LicenseUser            LicenseClass = "user"
)

// Exportable reports whether rows of this class may cross the process boundary
// in bulk, through a dump, an export endpoint or a list response.
func (l LicenseClass) Exportable() bool {
	return l == LicenseRedistributable
}

// Confidence ranks sources against each other during promote. A staged row
// never overwrites a live row of higher confidence.
const (
	ConfidenceScraped      = 40 // Tier 2 retailer and menu crawls
	ConfidenceOpenDB       = 70 // Open Food Facts and friends
	ConfidenceReference    = 85 // USDA, CoFID, CIQUAL
	ConfidenceHandVerified = 95 // curated by hand
	ConfidenceUser         = 100
)

// Nutrition is always per 100 g. Every adapter normalizes to this before staging.
type Nutrition struct {
	Kcal    float64 `json:"kcal"`
	Protein float64 `json:"protein"`
	Fat     float64 `json:"fat"`
	Carbs   float64 `json:"carbs"`
}

// Portion is a named amount a user can pick instead of typing grams.
type Portion struct {
	ID        int64   `json:"-"`
	FoodID    int64   `json:"-"`
	Name      string  `json:"name"`
	Grams     float64 `json:"grams"`
	IsDefault bool    `json:"is_default"`
}

// Alias carries one spelling of a food: Ukrainian, Russian, a common
// misspelling, or a voice-transcription error.
type Alias struct {
	ID     int64  `json:"-"`
	FoodID int64  `json:"-"`
	Alias  string `json:"alias"`
	Lang   string `json:"lang"`
}

// Food is one catalogue entry, product or dish.
type Food struct {
	ID           int64        `json:"id"`
	Kind         Kind         `json:"kind"`
	Name         string       `json:"name"`
	Brand        string       `json:"brand,omitempty"`
	Barcode      string       `json:"barcode,omitempty"`
	State        State        `json:"state"`
	Nutrition    Nutrition    `json:"nutrition"`
	PackageG     *float64     `json:"package_g,omitempty"`
	SourceID     string       `json:"source_id"`
	SourceURL    string       `json:"source_url,omitempty"`
	LicenseClass LicenseClass `json:"-"`
	Confidence   int          `json:"-"`
	OwnerUserID  *int64       `json:"-"`
	FetchedAt    *time.Time   `json:"-"`
	Portions     []Portion    `json:"portions,omitempty"`
	Aliases      []Alias      `json:"-"`
}

// DefaultPortion returns the entry's default named portion. Every dish has at
// least one; products may have none, in which case ok is false.
func (f Food) DefaultPortion() (Portion, bool) {
	for _, p := range f.Portions {
		if p.IsDefault {
			return p, true
		}
	}
	if len(f.Portions) > 0 {
		return f.Portions[0], true
	}
	return Portion{}, false
}

// Scale returns the nutrition of the given weight of this food.
func (f Food) Scale(grams float64) Nutrition {
	r := grams / 100
	return Nutrition{
		Kcal:    f.Nutrition.Kcal * r,
		Protein: f.Nutrition.Protein * r,
		Fat:     f.Nutrition.Fat * r,
		Carbs:   f.Nutrition.Carbs * r,
	}
}
