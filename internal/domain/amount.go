package domain

// How a logged amount was given. Every logging path reports the same labels.
const (
	AmountGrams          = "grams"
	AmountPortion        = "portion"
	AmountDefaultPortion = "default_portion"
	// AmountFallback is the FallbackGrams assumed for a food that carries no
	// portion at all; it is not a portion the caller or the catalogue chose.
	AmountFallback = "fallback_100g"
)

// Which path an entry came in by, and the food source recorded for an inline one.
const (
	LogPathCatalogue = "catalogue"
	LogPathInline    = "inline"
	SourceInline     = "inline"
)

// FallbackGrams is logged for a catalogue food that lists no portions.
const FallbackGrams = 100

// ResolveAmount turns whichever amount a caller gave into grams, and says which
// kind it was. Every logging path goes through it, so the HTTP handlers and the
// MCP tools cannot drift on what one portion weighs.
func ResolveAmount(food Food, grams float64, portionName string, portionQty float64) (float64, string, error) {
	if grams > 0 {
		return grams, AmountGrams, nil
	}

	if portionName != "" {
		for _, p := range food.Portions {
			if p.Name == portionName {
				if portionQty == 0 {
					portionQty = 1
				}
				return p.Grams * portionQty, AmountPortion, nil
			}
		}
		return 0, "", ErrInvalidInput
	}

	portion, ok := food.DefaultPortion()
	if ok {
		return portion.Grams, AmountDefaultPortion, nil
	}
	return FallbackGrams, AmountFallback, nil
}
